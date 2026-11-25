package database

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"
)

const importUploadLimit = 2 << 30

func (m *Module) importRoutes(r *gin.RouterGroup) {
	r.GET("", m.listImportFiles)
	r.POST("/upload", m.uploadImportFile)
	r.POST("/local", m.copyLocalImportFile)
	r.DELETE("", m.deleteImportFile)
	r.POST("/run", m.startImport)
	r.GET("/logs", m.importLogs)
}

func (m *Module) importTarget(c *gin.Context) (store.DBServer, string, bool) {
	name := c.Param("name")
	if len(name) > 255 || !filepath.IsLocal(name) || name == "." || strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 || protectedDB(name) {
		response.Fail(c, response.Invalid, "请选择业务数据库")
		return store.DBServer{}, "", false
	}
	s, ok := m.row(c)
	return s, name, ok
}

func validImportFile(name string) bool {
	if len(name) > 255 || filepath.Base(name) != name || strings.ContainsRune(name, '\\') || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return false
	}
	name = strings.ToLower(name)
	return strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".gz") || strings.HasSuffix(name, ".tgz")
}

func (m *Module) importDirectory(sid, database string, create bool) (string, *os.Root, error) {
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", nil, err
	}
	base, err := policy.Check(m.cfg.Current().BackupRoot, true)
	if err != nil {
		return "", nil, err
	}
	dir := filepath.Join(base, "database-import", sid, database)
	resolved, err := policy.Check(dir, true)
	if err != nil || resolved != dir {
		return "", nil, errors.New("导入暂存目录不可用")
	}
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", nil, err
		}
	}
	root, err := os.OpenRoot(dir)
	return dir, root, err
}

func (m *Module) localImportBackups(ctx context.Context, sid, database string) *gorm.DB {
	return m.db.WithContext(ctx).Model(&store.DBBackup{}).
		Where("server_id = ? AND database = ? AND storage_type = ?", sid, database, "local").
		Where("id NOT IN (SELECT backup_id FROM database_trash)")
}

func (m *Module) listImportFiles(c *gin.Context) {
	s, name, ok := m.importTarget(c)
	if !ok {
		return
	}
	page, size, ok := response.Page(c, 100, 10)
	if !ok {
		return
	}
	type file struct {
		Name     string    `json:"name"`
		ModTime  time.Time `json:"modTime"`
		Size     int64     `json:"size"`
		BackupID string    `json:"backupId,omitempty"`
	}
	keyword := strings.ToLower(strings.TrimSpace(c.Query("keyword")))
	files := []file{}
	dir, root, err := m.importDirectory(s.ID, name, false)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		failure(c, errors.New("导入暂存目录不可读"))
		return
	}
	if root != nil {
		defer root.Close()
		entries, err := fs.ReadDir(root.FS(), ".")
		if err != nil {
			failure(c, errors.New("导入文件列表读取失败"))
			return
		}
		for _, entry := range entries {
			filename := entry.Name()
			if !entry.Type().IsRegular() || !validImportFile(filename) || !strings.Contains(strings.ToLower(filename), keyword) {
				continue
			}
			info, err := entry.Info()
			if err == nil {
				files = append(files, file{Name: filename, ModTime: info.ModTime(), Size: info.Size()})
			}
		}
	}
	var backups []store.DBBackup
	if err := m.localImportBackups(c.Request.Context(), s.ID, name).Select("id", "file_path").Order("id").Find(&backups).Error; err != nil {
		failure(c, errors.New("本机备份列表读取失败"))
		return
	}
	for _, backup := range backups {
		filename := filepath.Base(backup.FilePath)
		if !filepath.IsAbs(backup.FilePath) || !validImportFile(filename) || !strings.Contains(strings.ToLower(filename), keyword) {
			continue
		}
		info, err := os.Lstat(backup.FilePath)
		if err == nil && info.Mode().IsRegular() {
			files = append(files, file{Name: filename, ModTime: info.ModTime(), Size: info.Size(), BackupID: backup.ID})
		}
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].ModTime.After(files[j].ModTime) })
	total := len(files)
	start := total
	if page-1 <= total/size {
		start = min((page-1)*size, total)
	}
	response.OK(c, gin.H{"directory": dir, "list": files[start:min(start+size, total)], "total": total})
}

func (m *Module) stageImportFile(ctx context.Context, sid, database, filename string, input io.Reader) error {
	_, root, err := m.importDirectory(sid, database, true)
	if err != nil {
		return err
	}
	defer root.Close()
	tmp := "." + store.ID() + ".part"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer root.Remove(tmp)
	err = copyImportFile(ctx, f, input, importUploadLimit)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return root.Link(tmp, filename)
}

func respondImportFile(c *gin.Context, err error) {
	var maxBytes *http.MaxBytesError
	switch {
	case err == nil:
		response.OK(c, nil)
	case errors.Is(err, os.ErrExist):
		response.Fail(c, response.Conflict, "暂存目录已有同名文件，请重命名后重试")
	case errors.Is(err, errImportTooLarge), errors.As(err, &maxBytes):
		response.Fail(c, response.TooLarge, "导入文件不能超过 2 GiB")
	default:
		response.Fail(c, response.System, "保存导入文件失败，请检查文件、备份目录及磁盘空间")
	}
}

func (m *Module) uploadImportFile(c *gin.Context) {
	s, name, ok := m.importTarget(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, importUploadLimit+(1<<20))
	reader, err := c.Request.MultipartReader()
	if err != nil {
		response.Fail(c, response.Invalid, "上传格式无效")
		return
	}
	part, err := reader.NextPart()
	if err != nil {
		response.Fail(c, response.Invalid, "请选择一个备份文件")
		return
	}
	filename := part.FileName()
	if part.FormName() != "file" || !validImportFile(filename) {
		response.Fail(c, response.Invalid, "备份文件格式或文件名无效")
		return
	}
	respondImportFile(c, m.stageImportFile(c.Request.Context(), s.ID, name, filename, part))
}

func (m *Module) copyLocalImportFile(c *gin.Context) {
	s, database, ok := m.importTarget(c)
	if !ok {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if !response.Bind(c, &input) {
		return
	}
	filename := filepath.Base(input.Path)
	if !validImportFile(filename) {
		response.Fail(c, response.Invalid, "备份文件格式或文件名无效")
		return
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		failure(c, err)
		return
	}
	path, err := policy.Check(input.Path, false)
	if err != nil {
		response.Fail(c, response.Invalid, "请选择可用的服务器绝对路径")
		return
	}
	f, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		response.Fail(c, response.NotFound, "服务器备份文件不存在或无法读取")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "请选择普通备份文件")
		return
	}
	respondImportFile(c, m.stageImportFile(c.Request.Context(), s.ID, database, filename, f))
}

func (m *Module) deleteImportFile(c *gin.Context) {
	s, database, ok := m.importTarget(c)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validImportFile(in.Name) {
		response.Fail(c, response.Invalid, "文件名无效")
		return
	}
	release, err := m.tasks.Acquire(LockKey(s.ID))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	_, root, err := m.importDirectory(s.ID, database, false)
	if err == nil {
		defer root.Close()
		info, statErr := root.Lstat(in.Name)
		err = statErr
		if err == nil && info.Mode().IsRegular() {
			err = root.Remove(in.Name)
		} else if err == nil {
			err = os.ErrNotExist
		}
	}
	if errors.Is(err, os.ErrNotExist) {
		response.Fail(c, response.NotFound, "暂存文件不存在")
		return
	}
	if err != nil {
		failure(c, errors.New("暂存文件删除失败"))
		return
	}
	response.OK(c, nil)
}

func (m *Module) startImport(c *gin.Context) {
	s, database, ok := m.importTarget(c)
	if !ok {
		return
	}
	var in struct {
		Name            string `json:"name"`
		BackupID        string `json:"backupId"`
		ConfirmDatabase string `json:"confirmDatabase"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.ConfirmDatabase != database || (in.BackupID == "" && !validImportFile(in.Name)) || (in.BackupID != "" && in.Name != "") {
		response.Fail(c, response.Invalid, "请选择备份文件并输入完整的目标数据库名称")
		return
	}
	if _, err := clientExecutable(s, false); err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	id, err := m.tasks.Submit(task.Meta{
		Module: "database", Action: "import", Target: s.Name + "/" + database,
		ResourceID: s.ID + "/" + database, Username: auth.Current(c).User.Username,
		IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID),
	}, func(ctx context.Context, log *task.Log) (any, error) {
		s, err := m.server(s.ID)
		if err != nil {
			return nil, err
		}
		err = m.importSQL(ctx, s, database, in.Name, in.BackupID, log)
		if err != nil {
			log.Line(err.Error())
		}
		return nil, err
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}

func (m *Module) openImportSource(ctx context.Context, sid, database, filename, backupID string) (*os.File, string, error) {
	const flags = os.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK
	if backupID == "" {
		_, root, err := m.importDirectory(sid, database, false)
		if err != nil {
			return nil, "", errors.New("导入暂存目录不可读")
		}
		defer root.Close()
		file, err := root.OpenFile(filename, flags, 0)
		if err != nil {
			return nil, "", errors.New("备份文件不存在或无法读取")
		}
		return file, filename, nil
	}
	var backup store.DBBackup
	if err := m.localImportBackups(ctx, sid, database).Select("file_path").First(&backup, "id = ?", backupID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", errors.New("本机备份不存在或不属于目标数据库")
		}
		return nil, "", errors.New("本机备份记录读取失败")
	}
	filename = filepath.Base(backup.FilePath)
	if !validImportFile(filename) {
		return nil, "", errors.New("本机备份文件格式无效")
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return nil, "", err
	}
	source, err := policy.CheckLink(backup.FilePath, false)
	if err != nil {
		return nil, "", errors.New("本机备份文件路径不可用")
	}
	f, err := os.OpenFile(source, flags, 0)
	if err != nil {
		return nil, "", errors.New("备份文件不存在或无法读取")
	}
	return f, filename, nil
}

func (m *Module) importSQL(ctx context.Context, s store.DBServer, database, filename, backupID string, log *task.Log) error {
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	f, filename, err := m.openImportSource(ctx, s.ID, database, filename, backupID)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("请选择普通备份文件")
	}
	log.Line("正在校验导入文件 " + filename)
	// 完整校验后再将 SQL 交给 MySQL，压缩包损坏时不会先执行部分语句。
	if err := os.MkdirAll(m.cfg.Layout().TempDir(), 0700); err != nil {
		return errors.New("无法创建临时目录")
	}
	sql, err := os.CreateTemp(m.cfg.Layout().TempDir(), ".mysql-import-*.part")
	if err != nil {
		return errors.New("无法创建 SQL 临时文件")
	}
	defer os.Remove(sql.Name())
	defer sql.Close()
	cfg := m.cfg.Current()
	if err := readImportSQL(ctx, sql, f, info.Size(), filename, cfg.ExtractMaxBytes, cfg.ExtractMaxEntries); err != nil {
		return errors.New("备份读取失败，未执行 SQL：" + err.Error())
	}
	if _, err := sql.Seek(0, io.SeekStart); err != nil {
		return errors.New("无法读取已校验的 SQL 临时文件")
	}
	db, err := m.connect(ctx, s, false)
	if err != nil {
		return err
	}
	err = databaseExists(ctx, db, database)
	db.Close()
	if err != nil {
		return err
	}
	path, args, env, err := m.client(ctx, s, false)
	if err != nil {
		return err
	}
	log.EnvSecrets(env)
	args = append(args, "--batch", "--binary-mode", "--local-infile=0", "--skip-reconnect", "--", database)
	log.Line("开始向 " + s.Name + "/" + database + " 导入 SQL")
	result, err := executor.Pipe(ctx, time.Hour, env, sql, nil, path, args...)
	log.ExitCode(result.ExitCode)
	if result.Stderr != "" {
		log.Line(result.Stderr)
	}
	if err != nil {
		return errors.New("SQL 导入失败，已执行的语句可能已生效，请查看日志")
	}
	log.Line("数据库导入完成")
	return nil
}

func (m *Module) importLogs(c *gin.Context) {
	s, database, ok := m.importTarget(c)
	if !ok {
		return
	}
	page, size, ok := response.Page(c, 100, 10)
	if !ok {
		return
	}
	query := m.db.Model(&store.Task{}).Where("module = ? AND action = ? AND resource_id = ?", "database", "import", s.ID+"/"+database)
	var total int64
	var rows []store.Task
	if err := query.Count(&total).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	if int64(page-1) <= total/int64(size) {
		if err := query.Omit("result").Order("started_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, task.Summary(row))
	}
	response.OK(c, gin.H{"list": items, "total": total})
}
