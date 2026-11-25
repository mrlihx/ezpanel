package file

import (
	"context"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

type Module struct {
	db     *gorm.DB
	cfg    config.Config
	policy *pathsafe.Policy
	tasks  *task.Manager
	auth   *auth.Service
	rename func(string, string) error
	remove func(context.Context, string) error
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, a *auth.Service) (*Module, error) {
	p, e := pathsafe.NewFileManager(cfg.InstallDir)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(cfg.Layout().TrashDir(), "files"), 0700); e != nil {
		return nil, e
	}
	return &Module{db: db, cfg: cfg, policy: p, tasks: tasks, auth: a, rename: os.Rename, remove: removeTree}, nil
}
func (m *Module) Name() string { return "file" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/files")
	r.Use(m.streamSession)
	r.GET("/list", m.list)
	r.GET("/directories", m.list)
	r.GET("/properties", m.properties)
	r.GET("/size", m.directorySize)
	r.POST("/size", m.directorySize)
	r.PUT("/remark", m.saveRemark)
	r.GET("/content", m.content)
	r.PUT("/content", m.save)
	r.POST("/create", m.create)
	r.POST("/rename", m.renameFile)
	r.POST("/transfer/preview", m.transfer)
	r.POST("/copy", m.transfer)
	r.POST("/move", m.transfer)
	r.POST("/delete", m.delete)
	r.POST("/chmod", m.permissions)
	r.POST("/chown", m.permissions)
	r.POST("/upload", m.upload)
	r.POST("/uploads", m.beginUpload)
	r.PATCH("/uploads/:id", m.appendUpload)
	r.POST("/uploads/:id/finish", m.finishUpload)
	r.POST("/remote", m.remote)
	r.GET("/download", m.download)
	r.HEAD("/download", m.download)
	r.GET("/preview", m.previewFile)
	r.HEAD("/preview", m.previewFile)
	r.POST("/compress", m.compress)
	r.POST("/extract", m.extract)
	r.GET("/search", m.search)
	r.GET("/content-search", m.searchContent)
	r.GET("/workspace", m.workspace)
	r.PUT("/workspace", m.saveWorkspace)
	r.GET("/identities", m.identities)
	r.GET("/trash/config", m.trashConfig)
	r.PUT("/trash/config", m.setTrashConfig)
	r.GET("/trash", m.trashList)
	r.POST("/trash/restore", m.restore)
	r.DELETE("/trash", m.purge)
	r.POST("/trash/empty", m.empty)
}
func fail(c *gin.Context, e error) { code, msg := errorResult(e); response.Fail(c, code, msg) }
func errorResult(e error) (int, string) {
	if e != nil {
		switch e.Error() {
		case "路径必须为绝对路径", "每批需要1至100项", "不能同时选择父项与子项":
			return response.Invalid, e.Error()
		case "元数据写入失败，数据保留在回收站暂存区，需人工处理", "源删除失败，源与回收站副本可能同时存在，需人工检查":
			return response.System, e.Error()
		}
	}
	switch {
	case e == nil:
		return 0, "ok"
	case errors.Is(e, pathsafe.ErrProtected):
		return response.Forbidden, "受保护路径，不能执行此操作"
	case errors.Is(e, errArchivePassword):
		return response.Invalid, errArchivePassword.Error()
	case errors.Is(e, errConflict), os.IsExist(e):
		return response.Conflict, "目标已存在或类型冲突"
	case os.IsNotExist(e):
		return response.NotFound, "文件不存在"
	case errors.Is(e, errLimit):
		return response.TooLarge, "超过容量或数量上限"
	case os.IsPermission(e):
		return response.Forbidden, "文件权限不足"
	case errors.Is(e, context.Canceled), errors.Is(e, context.DeadlineExceeded):
		return response.System, "操作已中断，目标可能部分改变"
	default:
		return response.System, "文件操作失败，请检查路径、权限或磁盘空间"
	}
}
func (m *Module) submit(c *gin.Context, action string, targets []string, op func(context.Context, string) error) {
	session := auth.Current(c)
	ip := ""
	if m.auth != nil {
		ip = m.auth.ClientIP(c)
	}
	id, e := m.tasks.Submit(task.Meta{Module: "file", Action: action, Target: strings.Join(targets, ", "), Username: session.User.Username, IP: ip}, func(parent context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(parent, time.Hour)
		defer cancel()
		result := task.BatchResult{Results: make([]task.ItemResult, 0, len(targets))}
		for _, target := range targets {
			e := ctx.Err()
			if e == nil {
				e = op(ctx, target)
			}
			code, msg := errorResult(e)
			result.Results = append(result.Results, task.ItemResult{Target: target, Code: code, Msg: msg})
			if e != nil {
				result.FailedCount++
			} else {
				result.SuccessCount++
			}
			log.Line(target + ": " + msg)
		}
		return result, nil
	})
	if e != nil {
		fail(c, e)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
func (m *Module) list(c *gin.Context) {
	page, size, ok := response.Page(c, 500, 100)
	if !ok {
		return
	}
	key, order := c.DefaultQuery("sort", "name"), c.DefaultQuery("order", "asc")
	if key != "name" && key != "size" && key != "mtime" || order != "asc" && order != "desc" {
		response.Fail(c, response.Invalid, "排序参数无效")
		return
	}
	path, e := m.policy.List(c.Query("path"))
	if e != nil {
		fail(c, e)
		return
	}
	entries, e := os.ReadDir(path)
	if e != nil {
		fail(c, e)
		return
	}
	infos := make([]os.FileInfo, 0, len(entries))
	directoriesOnly := strings.HasSuffix(c.FullPath(), "/directories")
	keyword := strings.ToLower(c.Query("keyword"))
	extension := c.Query("extension")
	for _, v := range entries {
		if directoriesOnly && (!v.IsDir() || !strings.Contains(strings.ToLower(v.Name()), keyword)) {
			continue
		}
		if !directoriesOnly && extension != "" && !v.IsDir() && filepath.Ext(v.Name()) != extension {
			continue
		}
		info, e := v.Info()
		if e != nil {
			fail(c, e)
			return
		}
		infos = append(infos, info)
	}
	sort.Slice(infos, func(i, j int) bool {
		a, b := infos[i], infos[j]
		if a.IsDir() != b.IsDir() {
			return a.IsDir()
		}
		cmp := strings.Compare(a.Name(), b.Name())
		if key == "size" && !a.IsDir() && a.Size() != b.Size() {
			if a.Size() < b.Size() {
				cmp = -1
			} else {
				cmp = 1
			}
		}
		if key == "mtime" && !a.ModTime().Equal(b.ModTime()) {
			cmp = a.ModTime().Compare(b.ModTime())
		}
		if order == "desc" {
			return cmp > 0
		}
		return cmp < 0
	})
	total := len(infos)
	start := total
	if page-1 <= total/size {
		start = min((page-1)*size, total)
	}
	end := min(start+size, total)
	rows := make([]Entry, 0, end-start)
	for _, info := range infos[start:end] {
		rows = append(rows, m.entry(filepath.Join(path, info.Name()), info))
	}
	if e := m.loadRemarks(rows); e != nil {
		fail(c, e)
		return
	}
	response.OK(c, gin.H{"list": rows, "total": total, "path": path})
}
func (m *Module) content(c *gin.Context) {
	path, e := m.policy.Check(c.Query("path"), false)
	if e != nil {
		fail(c, e)
		return
	}
	info, e := os.Stat(path)
	if e != nil {
		fail(c, e)
		return
	}
	if !info.Mode().IsRegular() || info.Size() > textLimit {
		response.Fail(c, response.Forbidden, "不能编辑此文件，请下载查看")
		return
	}
	f, e := os.Open(path)
	if e != nil {
		fail(c, e)
		return
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, textLimit+1))
	if e != nil {
		fail(c, e)
		return
	}
	if len(b) > textLimit || !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		response.Fail(c, response.Forbidden, "非UTF-8文本或超过2MiB，请下载查看")
		return
	}
	response.OK(c, gin.H{"content": string(b)})
}
func (m *Module) save(c *gin.Context) {
	var r struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if !response.Bind(c, &r) {
		return
	}
	if len(r.Content) > textLimit {
		response.Fail(c, response.TooLarge, "文本不能超过2MiB")
		return
	}
	if !utf8.ValidString(r.Content) || strings.ContainsRune(r.Content, 0) {
		response.Fail(c, response.Invalid, "仅支持UTF-8文本")
		return
	}
	if _, e := m.policy.CheckLink(r.Path, true); e != nil {
		fail(c, e)
		return
	}
	if _, e := os.Lstat(r.Path); e != nil {
		fail(c, e)
		return
	}
	if e := m.atomic(c.Request.Context(), r.Path, strings.NewReader(r.Content), true, textLimit, 0755); e != nil {
		fail(c, e)
		return
	}
	response.OK(c, nil)
}
func (m *Module) create(c *gin.Context) {
	var r struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}
	if !response.Bind(c, &r) {
		return
	}
	if r.Type != "file" && r.Type != "dir" {
		response.Fail(c, response.Invalid, "类型无效")
		return
	}
	p, e := m.policy.CheckLink(r.Path, true)
	if e != nil {
		fail(c, e)
		return
	}
	if r.Type == "dir" {
		e = os.Mkdir(p, 0755)
		if e == nil {
			_ = os.Chmod(p, 0755)
			inheritParentOwner(p)
		}
		if e == nil {
			e = os.Chmod(p, 0755)
		}
	} else {
		var f *os.File
		f, e = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0755)
		if e == nil {
			// 显式设置，避免面板进程的 umask 改变新建项权限。
			e = f.Chmod(0755)
			e = errors.Join(e, f.Close())
		}
	}
	if e != nil {
		fail(c, e)
		return
	}
	response.OK(c, nil)
}
func (m *Module) renameFile(c *gin.Context) {
	var r struct {
		Path    string `json:"path"`
		NewName string `json:"newName"`
	}
	if !response.Bind(c, &r) {
		return
	}
	if !nameValid(r.NewName) {
		response.Fail(c, response.Invalid, "名称必须是单个文件名")
		return
	}
	p, e := m.policy.CheckLink(r.Path, true)
	if e != nil {
		fail(c, e)
		return
	}
	dst, e := m.policy.CheckLink(filepath.Join(filepath.Dir(p), r.NewName), true)
	if e != nil {
		fail(c, e)
		return
	}
	if _, e = os.Lstat(dst); !os.IsNotExist(e) {
		fail(c, errConflict)
		return
	}
	if e = os.Rename(p, dst); e != nil {
		fail(c, e)
		return
	}
	if e = m.moveRemarks(p, dst); e != nil {
		response.Fail(c, response.System, "文件已重命名，但备注更新失败，请刷新后重新设置")
		return
	}
	response.OK(c, nil)
}
func (m *Module) search(c *gin.Context) {
	p, e := m.policy.Check(c.Query("path"), false)
	if e != nil {
		fail(c, e)
		return
	}
	keyword := c.Query("keyword")
	if keyword == "" {
		response.Fail(c, response.Invalid, "请输入文件名")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	rows := []Entry{}
	truncated := false
	e = filepath.Walk(p, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if ctx.Err() != nil || len(rows) >= 500 {
			truncated = true
			return filepath.SkipAll
		}
		if _, e := m.policy.Check(path, false); e != nil {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		for _, v := range []string{"/proc", "/sys", "/dev"} {
			if pathsafe.Contains(v, path) {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if path != p && strings.Contains(strings.ToLower(info.Name()), strings.ToLower(keyword)) {
			rows = append(rows, m.entry(path, info))
		}
		return nil
	})
	if e != nil {
		fail(c, e)
		return
	}
	if e := m.loadRemarks(rows); e != nil {
		fail(c, e)
		return
	}
	response.OK(c, gin.H{"list": rows, "truncated": truncated})
}
func (m *Module) permissions(c *gin.Context) {
	var r struct {
		Paths     []string `json:"paths"`
		Mode      string   `json:"mode"`
		Owner     string   `json:"owner"`
		Group     string   `json:"group"`
		Recursive bool     `json:"recursive"`
	}
	if !response.Bind(c, &r) {
		return
	}
	paths, e := m.sources(r.Paths, true)
	if e != nil {
		fail(c, e)
		return
	}
	uid, gid := -1, -1
	var mode uint64
	chmod := strings.HasSuffix(c.FullPath(), "chmod")
	if chmod {
		if len(r.Mode) != 4 || r.Mode[0] != '0' {
			response.Fail(c, response.Invalid, "权限必须为0000至0777")
			return
		}
		mode, e = strconv.ParseUint(r.Mode, 8, 9)
	} else {
		var u *user.User
		var g *user.Group
		u, e = user.Lookup(r.Owner)
		if e == nil {
			uid, e = strconv.Atoi(u.Uid)
		}
		if e == nil {
			g, e = user.LookupGroup(r.Group)
		}
		if e == nil {
			gid, e = strconv.Atoi(g.Gid)
		}
	}
	if e != nil {
		response.Fail(c, response.Invalid, "权限或用户/组无效")
		return
	}
	if r.Recursive {
		for _, p := range paths {
			if e = m.policy.CheckTree(p); e != nil {
				fail(c, e)
				return
			}
		}
	}
	result := task.BatchResult{Results: []task.ItemResult{}}
	for _, p := range paths {
		e = filepath.Walk(p, func(path string, info os.FileInfo, e error) error {
			if e != nil {
				return e
			}
			if e = c.Request.Context().Err(); e != nil {
				return e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil
			}
			if chmod {
				e = os.Chmod(path, os.FileMode(mode))
			} else {
				e = os.Chown(path, uid, gid)
			}
			if e == nil && info.IsDir() && !r.Recursive {
				return filepath.SkipDir
			}
			return e
		})
		code, msg := errorResult(e)
		result.Results = append(result.Results, task.ItemResult{Target: p, Code: code, Msg: msg})
		if e == nil {
			result.SuccessCount++
		} else {
			result.FailedCount++
		}
	}
	response.OK(c, result)
}
func (m *Module) transfer(c *gin.Context) {
	var r struct {
		Paths      []string          `json:"paths"`
		Dest       string            `json:"dest"`
		Overwrite  bool              `json:"overwrite"`
		Names      map[string]string `json:"names"`
		Overwrites map[string]bool   `json:"overwrites"`
	}
	if !response.Bind(c, &r) {
		return
	}
	paths, e := m.sources(r.Paths, true)
	if e != nil {
		fail(c, e)
		return
	}
	dest, e := m.policy.Check(r.Dest, true)
	if e != nil {
		fail(c, e)
		return
	}
	info, e := os.Stat(dest)
	if e != nil {
		fail(c, e)
		return
	}
	if !info.IsDir() {
		response.Fail(c, response.Invalid, "目标必须为目录")
		return
	}
	preview := strings.HasSuffix(c.FullPath(), "/preview")
	targets := make(map[string]string, len(paths))
	overwrites := make(map[string]bool, len(paths))
	reserved := map[string]bool{}
	for p, name := range r.Names {
		selected := false
		for _, source := range paths {
			if source == p {
				selected = true
				break
			}
		}
		if !selected || !nameValid(name) {
			response.Fail(c, response.Invalid, "重命名必须使用所选文件和单个文件名")
			return
		}
	}
	executable := false
	for _, p := range paths {
		if !preview {
			if e = m.policy.CheckTree(p); e != nil {
				fail(c, e)
				return
			}
		}
		name := filepath.Base(p)
		if renamed, ok := r.Names[p]; ok {
			name = renamed
		}
		dst := filepath.Join(dest, name)
		if reserved[dst] {
			response.Fail(c, response.Invalid, "多个文件的目标名称重复，请修改名称或分批粘贴")
			return
		}
		reserved[dst] = true
		targets[p] = dst
		overwrites[p] = r.Overwrite || r.Overwrites[p]

		for _, source := range paths {
			if pathsafe.Contains(source, dst) && !(preview && source == p && p == dst) {
				response.Fail(c, response.Invalid, "目标不能是所选文件或其后代")
				return
			}
		}
		if _, e = m.policy.CheckLink(dst, true); e != nil {
			fail(c, e)
			return
		}
	}
	if preview {
		conflicts := []gin.H{}
		for _, p := range paths {
			dst := targets[p]
			old, err := os.Lstat(dst)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				fail(c, err)
				return
			}
			src, err := os.Lstat(p)
			if err != nil {
				fail(c, err)
				return
			}
			canOverwrite := p != dst && src.Mode().IsRegular() && old.Mode().IsRegular()
			if overwrites[p] && canOverwrite {
				continue
			}
			name := filepath.Base(dst)
			extension := ""
			if !src.IsDir() {
				extension = filepath.Ext(name)
				if strings.HasSuffix(strings.ToLower(name), ".tar.gz") {
					extension = name[len(name)-7:]
				}
			}
			base := strings.TrimSuffix(name, extension)
			suggested := ""
			for n := 1; suggested == ""; n++ {
				suffix := "-副本"
				if n > 1 {
					suffix += "(" + strconv.Itoa(n) + ")"
				}
				candidate := base + suffix + extension
				target := filepath.Join(dest, candidate)
				if reserved[target] {
					continue
				}
				if _, err = os.Lstat(target); os.IsNotExist(err) {
					suggested = candidate
					reserved[target] = true
				} else if err != nil {
					fail(c, err)
					return
				}
			}
			conflicts = append(conflicts, gin.H{"source": p, "entry": m.entry(dst, old), "canOverwrite": canOverwrite, "suggestedName": suggested})
		}
		response.OK(c, gin.H{"conflicts": conflicts})
		return
	}
	for _, p := range paths {
		src, err := os.Lstat(p)
		if err != nil {
			fail(c, err)
			return
		}
		old, e := os.Lstat(targets[p])
		if os.IsNotExist(e) || e == nil && overwrites[p] && src.Mode().IsRegular() && old.Mode().IsRegular() {
			executable = true
		}
	}
	if !executable {
		fail(c, errConflict)
		return
	}
	move := strings.HasSuffix(c.FullPath(), "move")
	action := "copy"
	if move {
		action = "move"
	}
	m.submit(c, action, paths, func(ctx context.Context, p string) error {
		if e := m.policy.CheckTree(p); e != nil {
			return e
		}
		dst := targets[p]
		if _, e := m.policy.CheckLink(dst, true); e != nil {
			return e
		}
		srcInfo, e := os.Lstat(p)
		if e != nil {
			return e
		}
		old, e := os.Lstat(dst)
		if e == nil && (!overwrites[p] || !srcInfo.Mode().IsRegular() || !old.Mode().IsRegular()) {
			return errConflict
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		if move {
			if err := ctx.Err(); err != nil {
				return err
			}
			err := renameTransfer(p, dst, overwrites[p])
			switch {
			case err == nil:
				return m.moveRemarks(p, dst)
			case errors.Is(err, os.ErrExist):
				return errConflict
			case !errors.Is(err, syscall.EXDEV):
				return err
			}
		}
		if srcInfo.Mode().IsRegular() {
			in, e := os.Open(p)
			if e != nil {
				return e
			}
			e = m.atomic(ctx, dst, in, overwrites[p], 1<<63-2, 0755)
			in.Close()
			if e != nil {
				return e
			}
			if e = metadata(dst, srcInfo); e != nil {
				return e
			}
			if move {
				if e := os.Remove(p); e != nil {
					return e
				}
				return m.moveRemarks(p, dst)
			}
			return nil
		}
		tmp := filepath.Join(dest, ".panel-"+store.ID()+".part")
		defer os.RemoveAll(tmp)
		if e = copyTree(ctx, p, tmp); e != nil {
			return e
		}
		if e = renameTransfer(tmp, dst, false); e != nil {
			return e
		}
		if move {
			if e := removeTree(ctx, p); e != nil {
				return e
			}
			return m.moveRemarks(p, dst)
		}
		return nil
	})
}
