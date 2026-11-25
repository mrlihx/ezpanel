package pkgmgr

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"ezpanel/internal/executor"

	"gopkg.in/yaml.v3"
)

type LogSource struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Unit      string `json:"-"`
	Container string `json:"-"`
	StartedAt string `json:"-"`
	Clearable bool   `json:"clearable"`
}

// 日志位置取受管应用的配置与固定目录，不接受浏览器传入的路径。
func (m *Manager) LogSources(ctx context.Context, id string) ([]LogSource, error) {
	state, err := m.Status(ctx, id)
	if err != nil {
		return nil, err
	}
	if !state.Installed {
		return []LogSource{}, nil
	}
	out := []LogSource{}
	if state.PkgManager == "Docker" && IsDatabaseContainer(id) {
		row, err := m.DatabaseInstance(ctx, id, "default")
		if err != nil {
			return nil, err
		}
		return m.DatabaseLogSources(ctx, id, row)
	}
	add := func(key, name, path string) {
		out = append(out, LogSource{ID: key, Name: name, Path: path, Clearable: true})
	}
	content, _ := m.ReadFile(m.configPath(id))
	settings := Settings{Content: string(content)}
	values := map[string]string{}
	for _, line := range strings.Split(settings.Content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.ReplaceAll(strings.TrimSpace(key), "_", "-")] = strings.Trim(strings.TrimSpace(value), "\"'")
		}
	}
	switch id {
	case "ftp":
		if values, err := m.ftpValues(state); err == nil {
			if _, file, ok := strings.Cut(values["AltLog"], ":"); ok && filepath.IsAbs(file) {
				add("transfer", "传输日志", file)
			}
		}
	case "rsync":
		if path := values["log-file"]; filepath.IsAbs(path) {
			add("file", "同步日志", path)
		} else {
			add("file", "同步日志", m.layout().RsyncLog())
		}
	case "openresty":
		if match := regexp.MustCompile(`(?m)^\s*error_log\s+([^;\s]+)`).FindStringSubmatch(settings.Content); len(match) > 1 {
			path := strings.Trim(match[1], "\"'")
			if mapped := m.mountedLogPath(m.layout().OpenRestyLogDir(), path); mapped != "" {
				add("error", "服务错误日志", mapped)
			} else if filepath.IsAbs(path) && strings.HasPrefix(filepath.Clean(path), WebsiteLogRoot+"/") {
				add("error", "服务错误日志", path)
			}
		}
	case "nginx":
		for _, kind := range []string{"error", "access"} {
			path := "/var/log/nginx/" + kind + ".log"
			if match := regexp.MustCompile(`(?m)^\s*` + kind + `_log\s+([^;\s]+)`).FindStringSubmatch(settings.Content); len(match) > 1 && filepath.IsAbs(strings.Trim(match[1], "\"'")) {
				path = strings.Trim(match[1], "\"'")
			}
			name := "错误日志"
			if kind == "access" {
				name = "访问日志"
			}
			add(kind, name, path)
		}
	case "mysql", "mariadb":
		data := values["datadir"]
		if data == "" {
			data = state.DataDir
		}
		if data == "" {
			data = "/var/lib/mysql"
		}
		errorPath := values["log-error"]
		if errorPath == "" {
			matches, _ := filepath.Glob(filepath.Join(data, "*.err"))
			if len(matches) > 0 {
				errorPath = matches[0]
			} else if _, err := os.Stat("/var/log/mysql/error.log"); err == nil {
				errorPath = "/var/log/mysql/error.log"
			}
		}
		slowPath := values["slow-query-log-file"]
		if slowPath == "" {
			hostname, _ := os.Hostname()
			slowPath = filepath.Join(data, hostname+"-slow.log")
		}
		if !filepath.IsAbs(slowPath) {
			slowPath = filepath.Join(data, slowPath)
		}
		add("slow", "慢日志", slowPath)
		if errorPath != "" && errorPath != "stderr" {
			if !filepath.IsAbs(errorPath) {
				errorPath = filepath.Join(data, errorPath)
			}
			add("error", "错误日志", errorPath)
		}
	case "redis":
		for _, line := range strings.Split(settings.Content, "\n") {
			parts := strings.Fields(line)
			if len(parts) >= 2 && parts[0] == "logfile" {
				path := strings.Trim(strings.Join(parts[1:], " "), "\"'")
				if filepath.IsAbs(path) {
					add("file", "运行日志", path)
				}
			}
		}
	case "mongodb":
		var cfg struct {
			SystemLog struct {
				Path string `yaml:"path"`
			} `yaml:"systemLog"`
		}
		_ = yaml.Unmarshal([]byte(settings.Content), &cfg)
		if filepath.IsAbs(cfg.SystemLog.Path) {
			add("file", "运行日志", cfg.SystemLog.Path)
		}
	default:
		if IsPHP(id) {
			path := values["error-log"]
			if mapped := m.mountedLogPath(m.layout().PHPLogDir(phpBranch(id)), path); mapped != "" {
				add("error", "错误日志", mapped)
			}
		}
	}
	if len(out) == 0 && state.PkgManager == "Docker" && !IsProjectRuntime(id) {
		out = append(out, containerLogSource("ezpanel-"+id))
	}
	if state.Unit != "" {
		out = append(out, LogSource{ID: "journal", Name: "服务日志", Path: "journalctl · " + state.Unit, Unit: state.Unit})
	}
	return out, nil
}

func containerLogSource(name string) LogSource {
	return LogSource{ID: "container", Name: "服务输出", Path: "Docker 日志驱动", Container: name}
}

func (m *Manager) DatabaseLogSources(_ context.Context, app string, instance DatabaseInstance) ([]LogSource, error) {
	if instance.ContainerID == "" {
		return []LogSource{}, nil
	}
	var sources []LogSource
	add := func(key, name, path string, clearable bool) {
		if path == "" {
			return
		}
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			sources = append(sources, LogSource{ID: key, Name: name, Path: path, Clearable: clearable})
		}
	}
	raw, _ := os.ReadFile(m.databaseConfigPath(app, instance.ID))
	logDir := m.layout().AppInstanceLogDir(app, instance.ID)
	switch app {
	case "mysql":
		add("error", "错误日志", filepath.Join(logDir, "error.log"), true)
		for line := range strings.SplitSeq(string(raw), "\n") {
			if key, value := databaseDirective(app, line); key == "slow_query_log_file" {
				add("slow", "慢日志", m.mountedDatabaseLogPath(app, instance, strings.Trim(value, "\"'")), false)
				break
			}
		}
		if len(sources) == 1 {
			if _, err := os.Stat(filepath.Join(logDir, "slow.log")); err == nil {
				add("slow", "慢日志", filepath.Join(logDir, "slow.log"), false)
			} else if matches, _ := filepath.Glob(filepath.Join(instance.DataDir, "*-slow.log")); len(matches) > 0 {
				add("slow", "慢日志", matches[len(matches)-1], false)
			}
		}
	case "postgresql":
		add("file", "服务日志", filepath.Join(logDir, "postgresql.log"), true)
	case "mongodb":
		add("file", "服务日志", m.mountedDatabaseLogPath(app, instance, mongoOptionValues(string(raw))["logPath"]), false)
	case "memcached":
		add("file", "服务日志", instance.LogFile, true)
	case "redis":
		for line := range strings.SplitSeq(string(raw), "\n") {
			if key, value := databaseDirective(app, line); key == "logfile" {
				add("file", "服务日志", m.mountedDatabaseLogPath(app, instance, strings.Trim(value, "\"'")), false)
				break
			}
		}
	}
	if len(sources) == 0 {
		source := containerLogSource(instance.ContainerID)
		if app == "memcached" {
			source.StartedAt = instance.StartedAt
		}
		sources = append(sources, source)
	}
	return sources, nil
}

func (m *Manager) ReadContainerLog(ctx context.Context, name string) (executor.Result, error) {
	result, err := m.docker(ctx, 15*time.Second, "logs", "--timestamps", "--tail", "1000", name)
	if err == nil && result.Stderr != "" {
		lines := strings.Split(strings.TrimSuffix(result.Stdout+result.Stderr, "\n"), "\n")
		slices.SortStableFunc(lines, func(a, b string) int {
			first, _, _ := strings.Cut(a, " ")
			second, _, _ := strings.Cut(b, " ")
			return strings.Compare(first, second)
		})
		result.Stdout, result.Stderr = strings.Join(lines, "\n")+"\n", ""
	}
	return result, err
}

func (m *Manager) ReadServiceOutput(ctx context.Context, source LogSource) (executor.Result, error) {
	result, err := m.ReadContainerLog(ctx, source.Container)
	if err == nil && strings.TrimSpace(result.Stdout+result.Stderr) == "" {
		if started, err := time.Parse(time.RFC3339Nano, source.StartedAt); err == nil && !started.IsZero() {
			result.Stdout = "[" + started.UTC().Format(time.RFC3339) + "] 容器启动记录（来自 Docker 实例状态）\n"
		}
	}
	return result, err
}
