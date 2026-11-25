package database

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

var errClient = errors.New("缺少兼容数据库客户端，请安装MySQL客户端工具")

func clientPath(kind string, dump bool) (string, error) {
	names := []string{"mysql", "mariadb"}
	if kind == "mariadb" || kind == "" {
		names = []string{"mariadb", "mysql"}
	}
	if dump {
		for i := range names {
			if names[i] == "mariadb" {
				names[i] = "mariadb-dump"
			} else {
				names[i] = "mysqldump"
			}
		}
	}
	for _, name := range names {
		if p, e := executor.LookPath(name); e == nil {
			return p, nil
		}
	}
	return "", errClient
}
func usesContainerClient(s store.DBServer) bool {
	return s.InstanceID != "" || (s.IsLocal && strings.HasSuffix(filepath.ToSlash(s.SocketPath), "/apps/mysql/default/run/mysql.sock"))
}

func clientExecutable(s store.DBServer, dump bool) (string, error) {
	if usesContainerClient(s) {
		path, err := executor.LookPath("docker")
		if err != nil {
			return "", errors.New("找不到 Docker，无法运行容器内的数据库工具")
		}
		return path, nil
	}
	return clientPath(s.Type, dump)
}

func (m *Module) client(ctx context.Context, s store.DBServer, dump bool) (string, []string, []string, error) {
	path, err := clientExecutable(s, dump)
	if err != nil {
		return "", nil, nil, err
	}
	if usesContainerClient(s) {
		command := "mysql"
		if dump {
			command = "mysqldump"
		}
		password := ""
		if s.PasswordEnc != "" {
			password, err = store.Decrypt(m.cfg.AESKey, s.PasswordEnc)
			if err != nil {
				return "", nil, nil, err
			}
		}
		env := append(executor.DefaultEnv(), "MYSQL_PWD="+password)
		container := "ezpanel-mysql"
		connection := []string{"--protocol=SOCKET", "--socket=" + s.SocketPath}
		if s.InstanceID != "" {
			instance, err := m.packages.RunningDatabaseInstance(ctx, s.Type, s.InstanceID, s.Port)
			if err != nil {
				return "", nil, nil, err
			}
			if instance.ContainerIP == "" {
				return "", nil, nil, errors.New("选中实例未连接 Docker 网络")
			}
			container = instance.ContainerID
			connection = []string{"--protocol=TCP", "--host=" + instance.ContainerIP, "--port=" + strconv.Itoa(instance.ContainerPort)}
		}
		args := []string{"exec", "-i", "-e", "MYSQL_PWD", container, command, "--no-defaults", "--user=" + s.Username, "--default-character-set=utf8mb4"}
		return path, append(args, connection...), env, nil
	}
	help, e := executor.Run(ctx, 10*time.Second, path, "--no-defaults", "--help")
	if e != nil {
		return "", nil, nil, errors.New("无法检查数据库客户端功能")
	}
	text := help.Stdout + help.Stderr
	isMaria := strings.Contains(strings.ToLower(text), "mariadb")
	args := []string{"--no-defaults", "--user=" + s.Username, "--default-character-set=utf8mb4"}
	// mysql 与 mysqldump 支持的参数不同，以实际客户端能力为准。
	if strings.Contains(text, "--connect-timeout") {
		args = append(args, "--connect-timeout=5")
	}
	if s.Network == "unix" {
		args = append(args, "--protocol=SOCKET", "--socket="+s.SocketPath)
	} else {
		args = append(args, "--protocol=TCP", "--host="+s.Host, "--port="+strconv.Itoa(s.Port))
		if s.TLSMode == "verify" {
			if s.TLSServerName != "" && s.TLSServerName != s.Host {
				return "", nil, nil, errors.New("此客户端不能覆盖TLS主机名，请使用与证书匹配的连接地址进行备份")
			}
			if isMaria {
				if !strings.Contains(text, "ssl-verify-server-cert") {
					return "", nil, nil, errors.New("客户端不支持TLS身份校验，禁止降级")
				}
				args = append(args, "--ssl", "--ssl-verify-server-cert")
			} else {
				if !strings.Contains(text, "ssl-mode") {
					return "", nil, nil, errors.New("客户端不支持TLS身份校验，禁止降级")
				}
				args = append(args, "--ssl-mode=VERIFY_IDENTITY")
			}
			if s.TLSCAPath != "" {
				args = append(args, "--ssl-ca="+s.TLSCAPath)
			}
		} else {
			if isMaria {
				args = append(args, "--ssl=0")
			} else {
				if !strings.Contains(text, "ssl-mode") {
					return "", nil, nil, errors.New("客户端无法明确禁用TLS，请安装兼容版本")
				}
				args = append(args, "--ssl-mode=DISABLED")
			}
		}
	}
	env := executor.DefaultEnv()
	password := ""
	if s.PasswordEnc != "" {
		password, e = store.Decrypt(m.cfg.AESKey, s.PasswordEnc)
		if e != nil {
			return "", nil, nil, e
		}
	}
	env = append(env, "MYSQL_PWD="+password)
	return path, args, env, nil
}

// BackupDatabaseNames 返回当前连接账号可见的业务库。
func (m *Module) BackupDatabaseNames(ctx context.Context, serverID string) ([]string, error) {
	server, err := m.server(serverID)
	if err != nil {
		return nil, err
	}
	db, err := m.connect(ctx, server, false)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "SELECT SCHEMA_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
	if err != nil {
		return nil, errors.New("数据库枚举失败，请检查连接账号权限")
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if !protectedDB(name) {
			names = append(names, name)
		}
	}
	return names, rows.Err()
}

// Backup 由手工备份和计划任务共用；调用方必须持有 LockKey(serverID)。
func (m *Module) Backup(ctx context.Context, sid, name, cronID string, log *task.Log, directory string, temporary bool) (store.DBBackup, error) {
	return m.backupDatabase(ctx, sid, name, cronID, log, false, directory, temporary)
}

func (m *Module) backupDatabase(ctx context.Context, sid, name, cronID string, log *task.Log, complete bool, directory string, temporary bool) (store.DBBackup, error) {
	var backup store.DBBackup
	s, e := m.server(sid)
	if e != nil {
		return backup, e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	db, e := m.connect(ctx, s, false)
	if e != nil {
		return backup, e
	}
	e = databaseExists(ctx, db, name)
	db.Close()
	if e != nil {
		return backup, e
	}
	path, args, env, e := m.client(ctx, s, true)
	if e != nil {
		return backup, e
	}
	log.EnvSecrets(env)
	if complete {
		args = append(args, "--routines", "--events")
	}
	args = append(args, "--single-transaction", "--quick", "--", name)
	if directory == "" {
		directory = m.cfg.Current().BackupRoot
	}
	engine := s.Type
	if engine == "" {
		engine = "mysql"
	}
	safeName := pathsafe.BackupName(name)
	dir := filepath.Join(directory, "database", engine, s.InstanceID, safeName)
	if !temporary {
		policy, err := pathsafe.New(m.cfg.InstallDir)
		if err != nil {
			return backup, err
		}
		if _, err = policy.Check(dir, true); err != nil {
			return backup, errors.New("数据库备份目录不可用或指向受保护目录")
		}
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return backup, errors.New("备份目录不可写")
	}
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		location = time.UTC
	}
	filename := safeName + "_" + engine + "_data_" + time.Now().In(location).Format("20060102") + "_" + strconv.Itoa(rand.IntN(900000)+100000) + ".sql.gz"
	final := filepath.Join(dir, filename)
	part := final + ".part"
	f, e := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return backup, errors.New("无法创建备份文件")
	}
	defer func() { f.Close(); os.Remove(part) }()
	gz := gzip.NewWriter(f)
	started := time.Now()
	log.Line("开始导出并压缩数据库 " + s.Name + "/" + name + " " + started.In(location).Format("2006-01-02 15:04:05 MST"))
	result, e := executor.Pipe(ctx, time.Hour, env, nil, struct {
		io.Writer
		io.Closer
	}{gz, f}, path, args...)
	log.ExitCode(result.ExitCode)
	if result.Stderr != "" {
		log.Line(result.Stderr)
	}
	closeErr := gz.Close()
	if e != nil {
		return backup, errors.New("数据库备份失败；请检查客户端兼容性、TLS及日志")
	}
	if closeErr != nil {
		return backup, closeErr
	}
	if e = f.Sync(); e != nil {
		return backup, e
	}
	if e = f.Close(); e != nil {
		return backup, e
	}
	if e = os.Link(part, final); e != nil {
		return backup, e
	}
	st, e := os.Stat(final)
	if e != nil {
		os.Remove(final)
		return backup, e
	}
	backup = store.DBBackup{BackupFile: store.BackupFile{FileName: filename, StorageType: "local", Method: "manual"}, ID: store.ID(), ServerID: &s.ID, ServerName: s.Name, Database: name, FilePath: final, Size: st.Size(), CreatedAt: time.Now().UTC()}
	if cronID != "" {
		backup.CronJobID = &cronID
		backup.Method = "automatic"
	}
	if e = m.db.Create(&backup).Error; e != nil {
		os.Remove(final)
		return backup, errors.New("备份登记失败，未保留未登记文件")
	}
	finished := time.Now()
	log.Line(fmt.Sprintf("数据库压缩完成 %s，耗时 %.2f 秒，归档大小 %d 字节", finished.In(location).Format("2006-01-02 15:04:05 MST"), finished.Sub(started).Seconds(), st.Size()))
	if temporary {
		log.Line("│ 远程上传归档已准备 " + filename)
	} else {
		log.Line("│ 本地备份 " + final)
	}
	return backup, nil
}
func (m *Module) backupHandler(c *gin.Context) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	var p struct {
		Database string `json:"database"`
	}
	if c.ShouldBindJSON(&p) != nil || !identifier.MatchString(p.Database) {
		response.Fail(c, response.Invalid, "数据库名称无效")
		return
	}
	if _, e := clientExecutable(s, true); e != nil {
		response.Fail(c, response.Forbidden, e.Error())
		return
	}
	id, e := m.tasks.Submit(task.Meta{Module: "database", Action: "backup", Target: s.Name + "/" + p.Database, ResourceID: s.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		b, e := m.Backup(ctx, s.ID, p.Database, "", log, "", false)
		if e != nil {
			log.Line(e.Error())
			return nil, e
		}
		return gin.H{"backupId": b.ID}, nil
	})
	if e != nil {
		failure(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) backups(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	query := m.db.Model(&store.DBBackup{}).Where("id NOT IN (SELECT backup_id FROM database_trash)")
	sid := c.Param("sid")
	if sid == "" {
		sid = c.Query("serverId")
	}
	if sid != "" {
		query = query.Where("server_id = ?", sid)
	}
	if name := c.Query("database"); name != "" {
		query = query.Where("database = ?", name)
	}
	var total int64
	var rows []store.DBBackup
	if query.Count(&total).Error != nil || query.Order("created_at DESC,id DESC").Offset((page-1)*size).Limit(size).Find(&rows).Error != nil {
		failure(c, errors.New("备份列表读取失败"))
		return
	}
	response.OK(c, gin.H{"list": rows, "total": total})
}
func (m *Module) backupRow(c *gin.Context) (store.DBBackup, bool) {
	var b store.DBBackup
	e := m.db.First(&b, "id = ?", c.Param("id")).Error
	if e != nil {
		failure(c, e)
	}
	if e == nil && b.StorageType == "remote" {
		response.Fail(c, response.Forbidden, "远程备份请通过统一备份列表操作")
		return b, false
	}
	return b, e == nil
}
func (m *Module) download(c *gin.Context) {
	b, ok := m.backupRow(c)
	if !ok {
		return
	}
	f, e := os.Open(b.FilePath)
	if e != nil {
		response.Fail(c, response.NotFound, "备份文件不存在或不可读")
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		response.Fail(c, response.NotFound, "备份文件不可读")
		return
	}
	c.Header("Content-Disposition", `attachment; filename="`+filepath.Base(b.FilePath)+`"`)
	c.DataFromReader(200, st.Size(), "application/gzip", f, nil)
}
func (m *Module) deleteBackup(c *gin.Context) {
	var references int64
	if m.db.Model(&store.DatabaseTrash{}).Where("backup_id = ?", c.Param("id")).Count(&references).Error != nil {
		failure(c, errors.New("回收站引用读取失败"))
		return
	}
	if references > 0 {
		response.Fail(c, response.Forbidden, "此备份用于数据库回收，请在回收站恢复或彻底删除")
		return
	}
	b, ok := m.backupRow(c)
	if !ok {
		return
	}
	key := "backup:" + b.ID
	if b.ServerID != nil {
		key = LockKey(*b.ServerID)
	}
	release, e := m.tasks.Acquire(key)
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	if e = os.Remove(b.FilePath); e != nil && !os.IsNotExist(e) {
		failure(c, errors.New("备份文件删除失败，元数据保留"))
		return
	}
	if e = m.db.Delete(&b).Error; e != nil {
		failure(c, errors.New("文件已删除，但元数据清理失败，请重试"))
		return
	}
	response.OK(c, nil)
}
func (m *Module) restoreHandler(c *gin.Context) {
	b, ok := m.backupRow(c)
	if !ok {
		return
	}
	var references int64
	if err := m.db.Model(&store.DatabaseTrash{}).Where("backup_id = ?", b.ID).Count(&references).Error; err != nil {
		failure(c, err)
		return
	}
	if references > 0 {
		response.Fail(c, response.Invalid, "请从数据库回收站执行恢复，避免覆盖同名新库")
		return
	}

	var p struct {
		ConfirmDatabase string `json:"confirmDatabase"`
	}
	if c.ShouldBindJSON(&p) != nil || p.ConfirmDatabase != b.Database {
		response.Fail(c, response.Invalid, "须精确确认目标数据库名称")
		return
	}
	if b.ServerID == nil {
		response.Fail(c, response.Forbidden, "原服务器已删除，无法恢复")
		return
	}
	s, e := m.server(*b.ServerID)
	if e != nil {
		failure(c, e)
		return
	}
	if _, e = clientExecutable(s, false); e != nil {
		response.Fail(c, response.Forbidden, e.Error())
		return
	}
	id, e := m.tasks.Submit(task.Meta{Module: "database", Action: "restore", Target: s.Name + "/" + b.Database, ResourceID: s.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		e := m.restore(ctx, b.ID, nil, log)
		if e != nil {
			log.Line(e.Error())
			return gin.H{"backupId": b.ID, "database": b.Database, "targetMayBePartiallyChanged": true}, e
		}
		return gin.H{"backupId": b.ID, "database": b.Database}, nil
	})
	if e != nil {
		failure(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) restore(ctx context.Context, id string, trash *store.DatabaseTrash, log *task.Log) error {
	var b store.DBBackup
	if e := m.db.First(&b, "id = ?", id).Error; e != nil {
		return e
	}
	if b.ServerID == nil {
		return errors.New("服务器已删除")
	}
	s, e := m.server(*b.ServerID)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	f, e := os.Open(b.FilePath)
	if e != nil {
		return errors.New("登记备份文件不可读")
	}
	defer f.Close()
	gz, e := gzip.NewReader(f)
	if e != nil {
		return errors.New("备份gzip无效")
	}
	buf := make([]byte, 64<<10)
	for {
		if e = ctx.Err(); e != nil {
			gz.Close()
			return e
		}
		_, e = gz.Read(buf)
		if e == io.EOF {
			break
		}
		if e != nil {
			gz.Close()
			return errors.New("备份完整性检查失败，未开始导入")
		}
	}
	gz.Close()
	if _, e = f.Seek(0, 0); e != nil {
		return e
	}
	gz, e = gzip.NewReader(f)
	if e != nil {
		return e
	}
	defer gz.Close()
	path, args, env, e := m.client(ctx, s, false)
	if e != nil {
		return e
	}
	db, e := m.connect(ctx, s, trash != nil)
	if e != nil {
		return e
	}
	if trash != nil {
		// 先检查归档和客户端，再新建恢复目标。同名库存在时拒绝覆盖。
		_, e = db.ExecContext(ctx, fmt.Sprintf("CREATE DATABASE `%s` CHARACTER SET `%s` COLLATE `%s`", b.Database, trash.Charset, trash.Collation))
		if e != nil {
			e = errors.New("创建恢复目标失败；同名库存在时不会覆盖，请先检查并移走同名库")
		}
	} else {
		e = databaseExists(ctx, db, b.Database)
	}
	db.Close()
	if e != nil {
		return e
	}
	if trash != nil {
		if e := m.db.Model(trash).Update("state", trashImporting).Error; e != nil {
			return fmt.Errorf("已创建目标库，但无法登记导入状态。请检查目标库后重试：%w", e)
		}
		log.Line("备份完整性检查通过，开始导入新建数据库 " + b.Database)
	} else {
		log.Line("备份完整性检查通过，开始覆盖目标数据；导入失败时目标库可能部分改变")
	}
	log.EnvSecrets(env)
	args = append(args, "--", b.Database)
	result, e := executor.Pipe(ctx, time.Hour, env, struct {
		io.Reader
		io.Closer
	}{gz, f}, nil, path, args...)
	log.ExitCode(result.ExitCode)
	if result.Stderr != "" {
		log.Line(result.Stderr)
	}
	if e != nil {
		if trash != nil {
			if stateErr := m.db.Model(trash).Update("state", trashImportFailed).Error; stateErr != nil {
				log.Line("未能登记 SQL 导入失败状态")
			}
			return errors.New("SQL 导入失败，已保留备份和可能部分恢复的目标库；请检查或移走该库后再重试")
		}
		return errors.New("恢复失败，目标可能已部分改变；保留原备份，请人工检查")
	}
	if trash != nil {
		if e := m.db.Model(trash).Update("state", trashRegistrationPending).Error; e != nil {
			return fmt.Errorf("SQL 已导入，但无法保存完成状态。请核对目标库和任务日志：%w", e)
		}
	}
	return nil
}

// 同服务器批量备份作为一个任务顺序执行，共用已有服务器锁。
func (m *Module) batchBackup(c *gin.Context) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	var in struct {
		Databases []string `json:"databases"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Databases) == 0 || len(in.Databases) > 100 {
		response.Fail(c, response.Invalid, "请选择1–100个业务数据库")
		return
	}
	seen := map[string]bool{}
	names := []string{}
	for _, name := range in.Databases {
		if !identifier.MatchString(name) || protectedDB(name) {
			response.Fail(c, response.Invalid, "数据库名称无效或为系统数据库")
			return
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	if _, err := clientExecutable(s, true); err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: "backup", Target: s.Name + "/批量备份", ResourceID: s.ID, Username: session.User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(s.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		result := task.BatchResult{Results: []task.ItemResult{}}
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			_, err := m.Backup(ctx, s.ID, name, "", log, "", false)
			item := task.ItemResult{Target: name}
			if err != nil {
				item.Code = response.System
				item.Msg = response.Summary(err.Error())
				result.FailedCount++
				log.Line(name + "：" + item.Msg)
			} else {
				result.SuccessCount++
				item.Msg = "备份完成"
			}
			result.Results = append(result.Results, item)
		}
		if result.FailedCount > 0 {
			return result, fmt.Errorf("部分数据库备份失败（成功 %d，失败 %d）", result.SuccessCount, result.FailedCount)
		}
		return result, nil
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
