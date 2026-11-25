package website

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

var wsgiEntry = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*:[A-Za-z_][A-Za-z0-9_]*$`)

const (
	goBinaryCommand = `exec "$PROJECT_FILE"`
	goSourceCommand = `GO111MODULE="${GO111MODULE:-auto}" exec go run "${PROJECT_FILE%/*}"`
)

func (in *input) validateRuntime(old store.Website) error {
	in.RuntimeEntry = strings.TrimSpace(in.RuntimeEntry)
	in.RuntimeCommand = strings.TrimSpace(in.RuntimeCommand)
	in.RuntimeLaunch = strings.TrimSpace(in.RuntimeLaunch)
	if in.ProjectType != "go" && in.ProjectType != "nodejs" {
		in.RuntimeFile = ""
	} else if in.RuntimeFile != "" {
		in.RuntimeFile = strings.TrimPrefix(in.RuntimeFile, "./")
		if !executableChars.MatchString(in.RuntimeFile) || !filepath.IsLocal(in.RuntimeFile) ||
			filepath.Clean(in.RuntimeFile) != in.RuntimeFile || in.RuntimeFile == "." {
			return errors.New("项目执行文件必须是项目目录内的相对路径")
		}
		if in.ProjectType == "go" {
			switch in.RuntimeCommand {
			case "", goBinaryCommand, goSourceCommand:
				in.RuntimeCommand = goBinaryCommand
				// 按包运行，编译入口文件所在目录的其他源码。
				if filepath.Ext(in.RuntimeFile) == ".go" {
					in.RuntimeCommand = goSourceCommand
				}
			}
		}
	}
	if old.RuntimeCommand == "" && in.RuntimeCommand == "" && in.RuntimeLaunch == "" && in.RuntimeEntry == "" && in.RuntimePort == 0 {
		return nil
	}
	if in.ProjectType == "nodejs" {
		if err := in.nodeLaunch(old); err != nil {
			return err
		}
		in.RuntimeEntry = ""
	} else if in.ProjectType != "python" {
		in.RuntimeLaunch, in.RuntimeEntry = "", ""
	} else {
		if in.RuntimeLaunch == "" && !(old.RuntimeLaunch == "" && old.RuntimePort > 0) {
			in.RuntimeLaunch = old.RuntimeLaunch
			if in.RuntimeLaunch == "" {
				in.RuntimeLaunch = "command"
			}
		}
		switch in.RuntimeLaunch {
		case "command":
			in.RuntimePort = 0
		case "":
			// 旧项目在保存运行设置前继续使用已配置的端口。
		case "uwsgi", "gunicorn":
			if len(in.RuntimeEntry) > 255 || !wsgiEntry.MatchString(in.RuntimeEntry) {
				return errors.New("请选择有效的 Python 入口文件并填写应用对象，例如 app 或 application")
			}
			if in.RuntimeWorkers < 0 || in.RuntimeWorkers > 128 || in.RuntimeThreads < 0 || in.RuntimeThreads > 128 || in.RuntimeTimeout < 0 || in.RuntimeTimeout > 3600 {
				return errors.New("进程数、线程数或超时设置超出范围")
			}
			in.RuntimeCommand = wsgiCommand(in.RuntimeLaunch, in.RuntimeEntry, in.RuntimePort, in.RuntimeWorkers, in.RuntimeThreads, in.RuntimeTimeout, old.RuntimeRequirements)
		default:
			return errors.New("Python 启动方式无效")
		}
	}
	if in.RuntimeLaunch != "uwsgi" && in.RuntimeLaunch != "gunicorn" {
		in.RuntimeEntry = ""
	}
	if !(in.ProjectType == "python" && in.RuntimeLaunch == "command") && (in.RuntimePort < 1 || in.RuntimePort > 65535) {
		return errors.New("项目端口需为 1 至 65535")
	}
	if in.RuntimeCommand == "" || len(in.RuntimeCommand) > 8192 || strings.ContainsRune(in.RuntimeCommand, 0) {
		return errors.New("请填写有效的启动命令")
	}
	return nil
}

type pythonWSGIConfig struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func wsgiConfig(mode, entry string, port, workers, threads, timeout int) pythonWSGIConfig {
	workers, threads = max(1, workers), max(1, threads)
	if mode == "uwsgi" {
		return pythonWSGIConfig{Path: "/tmp/uwsgi.ini", Content: fmt.Sprintf(`[uwsgi]
module = %s
http-socket = 0.0.0.0:%d
master = true
die-on-term = true
need-app = true
processes = %d
threads = %d
harakiri = %d
`, entry, port, workers, threads, timeout)}
	}
	if timeout == 0 {
		timeout = 30
	}
	return pythonWSGIConfig{Path: "/tmp/gunicorn.conf.py", Content: fmt.Sprintf(`wsgi_app = %q
bind = "0.0.0.0:%d"
workers = %d
threads = %d
timeout = %d
`, entry, port, workers, threads, timeout)}
}

func wsgiCommand(mode, entry string, port, workers, threads, timeout int, requirements string) string {
	file := "'./" + strings.ReplaceAll(pythonRequirementsFile(requirements), "'", "'\\''") + "'"
	// 每个依赖文件首次启动时导入，切换文件后重新安装。
	dependencies := "requirements=" + file + `; if [ -f "$requirements" ] && [ "$(cat "$VIRTUAL_ENV/.requirements" 2>/dev/null)" != "$requirements" ]; then python -m pip install -r "$requirements" && printf '%s\n' "$requirements" > "$VIRTUAL_ENV/.requirements" || exit; fi; `
	config := wsgiConfig(mode, entry, port, workers, threads, timeout)
	write := "cat > " + config.Path + " <<'EZPANEL_WSGI' || exit\n" + config.Content + "EZPANEL_WSGI\n"
	if mode == "uwsgi" {
		return dependencies + write + "python -m pip install --only-binary=:all: pyuwsgi==2.0.30.post1 && exec pyuwsgi --ini " + config.Path
	}
	return dependencies + write + "python -m pip install gunicorn && exec gunicorn --config " + config.Path + " '" + entry + "'"
}

func projectRuntimeCommand(row store.Website) string {
	if row.RuntimeLaunch == "uwsgi" || row.RuntimeLaunch == "gunicorn" {
		return wsgiCommand(row.RuntimeLaunch, row.RuntimeEntry, row.RuntimePort, row.RuntimeWorkers, row.RuntimeThreads, row.RuntimeTimeout, row.RuntimeRequirements)
	}
	return row.RuntimeCommand
}

func (m *Module) runtimeLogs(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.RuntimeVersion == "" {
		response.Fail(c, response.Invalid, "该站点没有运行环境")
		return
	}
	text, running, failed, err := m.packages.ProjectLogs(c.Request.Context(), row.ID)
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"content": text, "running": running, "failedAttempt": failed})
}

func (m *Module) runtimeEnv(row store.Website) string {
	if row.RuntimeEnv == "" {
		return ""
	}
	value, _ := store.Decrypt(m.cfg.AESKey, row.RuntimeEnv)
	return value
}

func (m *Module) runtimeChanged(row, old store.Website) bool {
	return row.Root != old.Root || row.RuntimeEnvironment != old.RuntimeEnvironment ||
		row.RuntimePort != old.RuntimePort || row.RuntimeCommand != old.RuntimeCommand ||
		row.RuntimeFile != old.RuntimeFile || row.RuntimeRegistry != old.RuntimeRegistry || m.runtimeEnv(row) != m.runtimeEnv(old)
}

// 修改启动配置时先保留旧容器，等新路由和记录发布后再删除。
func (m *Module) changeProject(ctx context.Context, row *store.Website, old store.Website, remove bool, commit func() error) error {
	if row.RuntimeEnvironment == "" {
		return m.change(ctx, *row, remove, nil, nil, commit)
	}

	env := m.runtimeEnv(*row)
	if row.RuntimeEnv != "" && env == "" {
		return errors.New("无法读取项目环境变量")
	}
	existing := old.ProxyTarget != ""
	if row.ProjectType == "python" && row.Enabled && !remove {
		row.RuntimeCommand = projectRuntimeCommand(*row)
	}
	changed := row.RuntimePending || m.runtimeChanged(*row, old)
	if row.ProjectType == "python" && row.Enabled && !remove && existing && !old.Enabled {
		mounted, err := m.packages.ProjectUsesPythonEnvironment(ctx, row.ID)
		if err != nil {
			return err
		}
		changed = changed || !mounted
	}
	var runtime pkgmgr.RuntimeEnvironment
	if !remove && (row.RuntimeEnvironment != old.RuntimeEnvironment || (row.Enabled && (!existing || changed))) {
		release, err := m.tasks.Acquire("software-install:" + row.ProjectType)
		if err != nil {
			return err
		}
		defer release()
		runtime, err = m.packages.RuntimeEnvironment(row.ProjectType, row.RuntimeEnvironment)
		if err != nil {
			return err
		}
		row.RuntimeVersion = runtime.Version
	}
	if !remove && row.RuntimeLaunch == "uwsgi" {
		parts := strings.SplitN(row.RuntimeVersion, ".", 3)
		if len(parts) < 2 || parts[0] != "3" {
			return errors.New("uWSGI 支持 Python 3.8 至 3.14")
		}
		minor, _ := strconv.Atoi(parts[1])
		if minor < 8 || minor > 14 {
			return errors.New("uWSGI 支持 Python 3.8 至 3.14")
		}
	}
	replacing := existing && changed && row.Enabled && !remove
	created, started, stopped := false, false, false
	recovery := context.WithoutCancel(ctx)
	rollback := func() {
		if created {
			_, _ = m.packages.ProjectAction(recovery, row.ID, "remove")
		}
		if replacing {
			_ = m.packages.RenameProject(recovery, row.ID+"-previous", row.ID)
		}
		if started {
			_, _ = m.packages.ProjectAction(recovery, row.ID, "stop")
		}
		if stopped && old.Enabled {
			_, _ = m.packages.ProjectAction(recovery, row.ID, "start")
		}
	}
	if replacing {
		if err := m.packages.RenameProject(ctx, row.ID, row.ID+"-previous"); err != nil {
			return err
		}
	}
	switch {
	case row.Enabled && !remove && (!existing || replacing):
		if row.RuntimeFile != "" {
			env += "\nPROJECT_FILE=./" + row.RuntimeFile
		}
		target, err := m.packages.StartProject(ctx, row.ID, row.ProjectType, runtime, row.Root, row.RuntimeCommand, env, row.RuntimeRegistry, row.RuntimePort)
		if err != nil {
			rollback()
			return err
		}
		row.ProxyTarget, created = target, true
	case row.Enabled && existing && !old.Enabled && !remove:
		target, err := m.packages.ResumeProject(ctx, row.ID, "start", row.RuntimePort)
		if err != nil {
			return err
		}
		row.ProxyTarget, started = target, true
	case existing && (remove || !row.Enabled) && old.Enabled:
		if _, err := m.packages.ProjectAction(ctx, row.ID, "stop"); err != nil {
			return err
		}
		stopped = true
	}
	// 禁用时修改了运行配置，新配置在再次启用时启动。
	discard := existing && changed && !row.Enabled
	if discard {
		row.ProxyTarget = ""
	}
	if created || started || discard {
		row.RuntimePending = false
	}
	if err := m.change(ctx, *row, remove, nil, nil, commit); err != nil {
		rollback()
		return err
	}
	if replacing {
		_, _ = m.packages.ProjectAction(recovery, row.ID+"-previous", "remove")
	} else if remove || discard {
		_, _ = m.packages.ProjectAction(recovery, row.ID, "remove")
	}
	if created || remove {
		_, _ = m.packages.ProjectAction(recovery, row.ID+"-failed", "remove")
	}
	if remove && row.ProjectType == "python" {
		_ = m.packages.RemovePythonProjectEnvironment(row.ID)
	}
	return nil
}

func (m *Module) restartProject(c *gin.Context) {
	release, err := m.tasks.Acquire("website:" + c.Param("id"))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	row, ok := m.row(c)
	if !ok {
		return
	}
	auditSite(c, "", "项目类型 "+row.ProjectType, "运行环境 "+row.RuntimeVersion)
	if err := m.RestartProject(c.Request.Context(), row.ID); err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}

// RestartProject 由服务面板和计划任务共用，调用方持有项目任务锁。
func (m *Module) RestartProject(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var row store.Website
	if err := m.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	if row.RuntimeVersion == "" || !row.Enabled || row.ProxyTarget == "" {
		return errors.New("请先启用项目")
	}
	if row.ProjectType == "python" {
		mounted, err := m.packages.ProjectUsesPythonEnvironment(ctx, row.ID)
		if err != nil {
			return err
		}
		row.RuntimePending = row.RuntimePending || !mounted || row.RuntimeCommand != projectRuntimeCommand(row)
	}
	if row.RuntimePending {
		return m.changeProject(ctx, &row, row, false, func() error { return m.db.Save(&row).Error })
	}
	target, err := m.packages.ResumeProject(ctx, row.ID, "restart", row.RuntimePort)
	if err != nil || target != row.ProxyTarget {
		row.Enabled = err == nil
		if err == nil {
			row.ProxyTarget = target
		}
		changeErr := m.change(context.WithoutCancel(ctx), row, false, nil, nil, func() error { return m.db.Save(&row).Error })
		if changeErr != nil && err == nil {
			_, _ = m.packages.ProjectAction(context.WithoutCancel(ctx), row.ID, "stop")
		}
		err = errors.Join(err, changeErr)
	}
	return err
}
