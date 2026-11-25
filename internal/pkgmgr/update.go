package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"fmt"
	"gopkg.in/yaml.v3"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type oldProcess struct {
	PID     int    `json:"pid"`
	Started string `json:"started"`
}

func processInfo(pid int) (oldProcess, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return oldProcess{}, e
	}
	end := strings.LastIndex(string(b), ")")
	fields := strings.Fields(string(b)[end+1:])
	if end < 0 || len(fields) < 20 {
		return oldProcess{}, errors.New("进程状态无效")
	}
	return oldProcess{pid, fields[19]}, nil
}
func (m *Manager) selectRelease(id, prefix string) error {
	root := m.installRoot(id)
	temp := root + "/.current-next"
	if e := os.Symlink(prefix, temp); e != nil {
		return e
	}
	defer os.Remove(temp)
	return os.Rename(temp, root+"/current")
}
func (m *Manager) writeRecord(record sourceInstall) error {
	b, e := json.MarshalIndent(record, "", "  ")
	if e != nil {
		return e
	}
	root := m.installRoot(record.ID)
	f, e := os.CreateTemp(root, ".source-")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	closed := f.Close()
	if e == nil {
		e = closed
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), root+"/.panel-source.json")
}

// 隔离升级尚未退出时不能从其他入口启动同一数据目录。
func (m *Manager) upgradeBusy(ctx context.Context, id string) bool {
	if id != "mysql" && id != "mariadb" {
		return false
	}
	r, _ := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "is-active", "ezpanel-upgrade-"+id+".service")
	switch strings.TrimSpace(r.Stdout) {
	case "active", "activating", "deactivating":
		return true
	}
	return false
}
func (m *Manager) mainPID(ctx context.Context, unit string) (int, error) {
	r, e := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "show", unit, "--property=MainPID", "--value")
	if e != nil {
		return 0, e
	}
	pid, e := strconv.Atoi(strings.TrimSpace(r.Stdout))
	if e != nil || pid < 3 {
		return 0, errors.New("服务没有有效主进程")
	}
	return pid, nil
}
func unitText(id, service string) string {
	return "[Unit]\nDescription=ezpanel managed " + Name(id) + "\nAfter=network.target\n[Service]\n" + service + "Restart=always\nRestartSec=3\nNotifyAccess=all\n[Install]\nWantedBy=multi-user.target\n"
}
func (m *Manager) waitService(ctx context.Context, unit, prefix string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		pid, e := m.mainPID(ctx, unit)
		if e == nil {
			exe, e := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
			if e == nil && strings.HasPrefix(exe, prefix+"/") {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return errors.New("新版本主进程未达到预期状态")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
func (m *Manager) updateBuilt(ctx context.Context, id string, release Release, prefix, checksum string, previous sourceInstall, choice SourceSelection, service string, links map[string]string, log func(string)) (result State, failure error) {
	root := m.installRoot(id)
	oldPrefix := previous.Prefix
	if oldPrefix == "" {
		oldPrefix = root
	}
	binary := map[string]string{"nginx": "sbin/nginx", "redis": "bin/redis-server", "mysql": "bin/mysqld", "mariadb": "bin/mariadbd", "mysql-client": "bin/mysql", "docker": "bin/dockerd", "mongodb": "bin/mongod", "memcached": "bin/memcached"}[id]
	if IsPHP(id) {
		binary = "bin/php"
	}
	arg := "--version"
	if id == "nginx" {
		arg = "-v"
	}
	if id == "memcached" {
		arg = "-h"
	}
	version, e := m.Run(ctx, 30*time.Second, nil, filepath.Join(prefix, binary), arg)
	if e != nil || versionPattern.FindString(version.Stdout+" "+version.Stderr) != release.Version {
		return State{}, errors.New("构建后的程序版本与所选版本不一致")
	}
	if id == "nginx" {
		if _, e = m.Run(ctx, 30*time.Second, nil, prefix+"/sbin/nginx", "-t"); e != nil {
			return State{}, errors.New("新版本Nginx配置检查失败，旧服务保持运行")
		}
	}
	// 只重建本商店原本拥有的入口，保留系统其他客户端。
	for link, target := range previous.Links {
		links[link] = strings.Replace(target, oldPrefix, root+"/current", 1)
	}
	var previousProcesses []oldProcess
	record := sourceInstall{ID: id, DownloadSource: choice, Prefix: prefix, Version: release.Version, URL: release.URL, SHA256: checksum, Links: links, Unit: previous.Unit}
	defer func() {
		if failure != nil {
			active, _ := os.Readlink(root + "/current")
			if active == prefix {
				record.UpdateFailed = true
				record.PreviousPrefix = oldPrefix
				record.PreviousProcesses = previousProcesses
				_ = m.writeRecord(record)
			}
		}
	}()
	if id == "nginx" && previous.Prefix == "" {
		legacy := filepath.Join(root, "releases", previous.Version+"-legacy")
		if e = os.MkdirAll(legacy+"/sbin", 0755); e != nil {
			return State{}, e
		}
		for _, dir := range []string{legacy, legacy + "/sbin"} {
			if e = os.Chmod(dir, 0755); e != nil {
				return State{}, e
			}
		}
		if e = os.Rename(root+"/sbin/nginx", legacy+"/sbin/nginx"); e != nil {
			return State{}, e
		}
		if e = m.selectRelease(id, legacy); e != nil {
			os.Rename(legacy+"/sbin/nginx", root+"/sbin/nginx")
			return State{}, e
		}
		if e = os.Symlink(root+"/current/sbin/nginx", root+"/sbin/nginx"); e != nil {
			os.Remove(root + "/current")
			return State{}, errors.Join(e, os.Rename(legacy+"/sbin/nginx", root+"/sbin/nginx"))
		}
		oldPrefix = legacy
		previous.Prefix = legacy
		if e = m.writeRecord(previous); e != nil {
			return State{}, e
		}
	}

	unitPath := "/etc/systemd/system/" + previous.Unit
	oldUnit := []byte{}
	if previous.Unit != "" {
		oldUnit, e = os.ReadFile(unitPath)
		if e != nil {
			return State{}, e
		}
	}
	state, e := m.Status(ctx, id)
	if e != nil {
		return state, e
	}
	running := state.Running != nil && *state.Running
	backup := ""
	if id == "mysql" || id == "mariadb" || id == "redis" || id == "mongodb" {
		backup = filepath.Join(m.cfg.Current().BackupRoot, "software", id, time.Now().UTC().Format("20060102T150405Z"))
		if pathsafe.Contains(m.serviceDataDir(id), backup) {
			return state, errors.New("软件更新备份目录不能位于该服务数据目录内")
		}
		if e = os.MkdirAll(backup, 0700); e != nil {
			return state, e
		}
		if e = m.checkDataConfig(id); e != nil {
			return state, e
		}
		if id == "redis" && running {
			if e = m.saveRedis(ctx, prefix, log); e != nil {
				return state, e
			}
		}
	}
	if id == "docker" && running {
		b, e := os.ReadFile("/etc/docker/daemon.json")
		if e != nil {
			return state, e
		}
		var config map[string]any
		if json.Unmarshal(b, &config) != nil || config["live-restore"] != true {
			return state, errors.New("运行中的Docker更新须先在配置中开启live-restore；不会停止容器代替该设置")
		}
	}
	if running && id == "nginx" {
		pid, e := m.mainPID(ctx, previous.Unit)
		if e != nil {
			return state, e
		}
		p, e := processInfo(pid)
		if e != nil {
			return state, e
		}
		previousProcesses = append(previousProcesses, p)
		children, _ := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
		for _, raw := range strings.Fields(string(children)) {
			child, _ := strconv.Atoi(raw)
			if p, e := processInfo(child); e == nil {
				previousProcesses = append(previousProcesses, p)
			}
		}
	} else if running {
		if id == "docker" {
			// live-restore可能保留旧containerd/shim，退出前保留它们所属程序。
			entries, err := os.ReadDir("/proc")
			if err != nil {
				return state, err
			}
			for _, entry := range entries {
				pid, err := strconv.Atoi(entry.Name())
				if err != nil {
					continue
				}
				exe, err := os.Readlink("/proc/" + entry.Name() + "/exe")
				if err == nil && strings.HasPrefix(exe, oldPrefix+"/") {
					if p, err := processInfo(pid); err == nil {
						previousProcesses = append(previousProcesses, p)
					}
				}
			}
		}
		log("构建已完成，开始正常停止旧服务进行切换")
		if _, e = m.Run(ctx, 2*time.Minute, nil, "/usr/bin/systemctl", "stop", previous.Unit); e != nil {
			return state, e
		}
	}
	var oldConfiguration []byte
	databaseTouched := false
	restoreBeforePublic := func(cause error) (State, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
		defer cancel()
		if databaseTouched {
			verifyUnit := "ezpanel-upgrade-" + id + ".service"
			loaded, loadErr := m.Run(recovery, 30*time.Second, nil, "/usr/bin/systemctl", "show", verifyUnit, "--property=LoadState", "--value")
			if loadErr != nil && strings.TrimSpace(loaded.Stdout) != "not-found" {
				return state, errors.Join(cause, loadErr)
			}
			if strings.TrimSpace(loaded.Stdout) != "not-found" {
				if _, stopErr := m.Run(recovery, 2*time.Minute, nil, "/usr/bin/systemctl", "stop", verifyUnit); stopErr != nil {
					return state, errors.Join(cause, stopErr)
				}
			}
			source := m.serviceDataDir(id)
			failed := source + ".failed-" + time.Now().UTC().Format("20060102T150405Z")
			if moveErr := os.Rename(source, failed); moveErr != nil {
				return state, errors.Join(cause, moveErr)
			}
			cp, copyErr := m.LookPath("cp")
			if copyErr == nil {
				_, copyErr = m.Stream(recovery, 30*time.Minute, nil, log, cp, "-a", "--reflink=auto", backup+"/data", source)
			}
			if copyErr != nil {
				return state, errors.Join(cause, copyErr)
			}
			databaseTouched = false
		}
		active, _ := os.Readlink(root + "/current")
		if active == prefix && running && id != "nginx" && previous.Unit != "" {
			if _, stopErr := m.Run(recovery, 2*time.Minute, nil, "/usr/bin/systemctl", "stop", previous.Unit); stopErr != nil {
				return state, errors.Join(cause, stopErr)
			}
		}
		if e = m.selectRelease(id, oldPrefix); e != nil {
			return state, errors.Join(cause, e)
		}
		if len(oldConfiguration) > 0 {
			if e := os.WriteFile(m.configPath(id), oldConfiguration, 0640); e != nil {
				return state, errors.Join(cause, e)
			}
		}
		if previous.Unit != "" {
			if e := os.WriteFile(unitPath, oldUnit, 0644); e != nil {
				return state, errors.Join(cause, e)
			}
			if _, e := m.Run(recovery, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
				return state, errors.Join(cause, e)
			}
			if running {
				_, e = m.Run(recovery, 2*time.Minute, nil, "/usr/bin/systemctl", "start", previous.Unit)
			}
		}
		return state, errors.Join(cause, e)
	}
	if backup != "" {
		log("旧服务已停止，保存完整服务数据及配置：" + backup)
		source := m.serviceDataDir(id)
		cp, e := m.LookPath("cp")
		if e != nil {
			return restoreBeforePublic(e)
		}
		if _, e = m.Stream(ctx, 30*time.Minute, nil, log, cp, "-a", "--reflink=auto", source, backup+"/data"); e != nil {
			return restoreBeforePublic(e)
		}
		configuration, e := os.ReadFile(m.configPath(id))
		if e != nil {
			return restoreBeforePublic(e)
		}
		oldConfiguration = configuration
		if e = os.WriteFile(backup+"/configuration", configuration, 0600); e != nil {
			return restoreBeforePublic(e)
		}
		if id == "mysql" || id == "mariadb" {
			databaseTouched = true
			if e = m.verifyDatabaseUpgrade(ctx, id, prefix, log); e != nil {
				return restoreBeforePublic(e)
			}
		}

	}
	if id == "mysql" || id == "mariadb" {
		lines := strings.Split(string(oldConfiguration), "\n")
		for i, line := range lines {
			key, _, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "basedir" {
				lines[i] = "basedir=" + root + "/current"
			}
		}
		if e = os.WriteFile(m.configPath(id), []byte(strings.Join(lines, "\n")), 0640); e != nil {
			return restoreBeforePublic(e)
		}
	}
	if e = m.selectRelease(id, prefix); e != nil {
		return restoreBeforePublic(e)
	}
	for link, target := range links {
		old, err := os.Readlink(link)
		if err != nil {
			return restoreBeforePublic(err)
		}
		if old == target {
			continue
		}
		if old != previous.Links[link] {
			return restoreBeforePublic(errors.New("程序入口已被其他操作修改，拒绝接管"))
		}
		temp := link + ".panel-next"
		if e = os.Symlink(target, temp); e != nil {
			return restoreBeforePublic(e)
		}
		if e = os.Rename(temp, link); e != nil {
			os.Remove(temp)
			return restoreBeforePublic(e)
		}
	}
	if previous.Unit != "" {
		if e = os.WriteFile(unitPath, []byte(unitText(id, service)), 0644); e != nil {
			return restoreBeforePublic(e)
		}
		if _, e = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); e != nil {
			return restoreBeforePublic(e)
		}
		if id == "nginx" && running {
			if e = m.hotNginx(ctx, prefix, previous.Unit, previousProcesses[0], log); e != nil {
				return restoreBeforePublic(e)
			}
		} else if running {
			if _, e = m.Run(ctx, 2*time.Minute, nil, "/usr/bin/systemctl", "start", previous.Unit); e != nil {
				// 公开入口启动后可能已有新写入，不覆盖数据回滚。
				if id == "mysql" || id == "mariadb" || id == "mongodb" {
					record.PreviousPrefix = oldPrefix
					if writeErr := m.writeRecord(record); writeErr != nil {
						return state, errors.Join(e, writeErr)
					}
					return state, fmt.Errorf("新版本公开服务启动失败，已保留新旧程序与备份，不覆盖可能产生的新数据：%w", e)
				}
				return restoreBeforePublic(e)
			}
		}
		if running {
			if e = m.waitService(ctx, previous.Unit, prefix, 30*time.Second); e != nil {
				return state, e
			}
		}
	}
	if id == "mysql" || id == "mariadb" || id == "mongodb" {
		if e = os.WriteFile(m.versionFile(id), []byte(release.Version), 0600); e != nil {
			return state, e
		}
	}
	record.PreviousPrefix = oldPrefix
	record.PreviousProcesses = previousProcesses
	if e = m.writeRecord(record); e != nil {
		return state, e
	}
	log("新版本已接管；旧程序将在旧进程退出后清理。配置和数据备份保留。")
	if len(previousProcesses) == 0 {
		if e = m.cleanPrevious(&record); e != nil {
			log("旧程序清理稍后重试：" + e.Error())
		}
	}
	return m.Status(ctx, id)
}
func (m *Manager) checkDataConfig(id string) error {
	path := m.configPath(id)
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	expected := m.serviceDataDir(id)
	if id == "mongodb" {
		var conf struct {
			Storage struct {
				DBPath string `yaml:"dbPath"`
			} `yaml:"storage"`
		}
		if err := yaml.Unmarshal(b, &conf); err != nil {
			return err
		}
		if conf.Storage.DBPath != expected {
			return errors.New("MongoDB数据目录已自定义，请先迁移到受控目录")
		}
		return nil
	}
	section := ""
	dataConfigured := false
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if strings.HasPrefix(line, "include") || strings.HasPrefix(line, "!include") {
			return errors.New("存在外部包含配置，自动更新前请将服务数据路径配置集中到主配置")
		}
		if id == "redis" {
			f := strings.Fields(line)
			if len(f) >= 2 && f[0] == "dir" {
				if strings.Trim(f[1], `"'`) != expected {
					return errors.New("Redis数据目录已自定义，当前自动更新不接管该目录")
				}
				dataConfigured = true
			}
		}
		if (id == "mysql" || id == "mariadb") && section == "[mysqld]" {
			key, value, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "datadir" {
				if strings.Trim(strings.TrimSpace(value), `"'`) != expected {
					return errors.New("数据库数据目录已自定义，当前自动更新不接管该目录")
				}
				dataConfigured = true
			}
		}
	}
	if !dataConfigured {
		return errors.New("主配置缺少受控数据目录，不能确认备份范围，请先恢复数据目录配置")
	}
	return nil
}
func (m *Manager) saveRedis(ctx context.Context, prefix string, log func(string)) error {
	b, e := os.ReadFile(m.configPath("redis"))
	if e != nil {
		return e
	}
	port, host, password := "6379", "127.0.0.1", ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(strings.TrimSpace(line))
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "port":
			port = f[1]
		case "requirepass":
			password = strings.Trim(strings.Join(f[1:], " "), `"'`)
		case "bind":
			host = f[1]
		}
	}
	env := executor.DefaultEnv()
	if password != "" {
		env = append(env, "REDISCLI_AUTH="+password)
	}
	r, e := m.Run(ctx, 2*time.Minute, env, prefix+"/bin/redis-cli", "-h", host, "-p", port, "SAVE")
	if e != nil || strings.TrimSpace(r.Stdout) != "OK" {
		return errors.New("无法保存Redis内存数据，旧服务继续运行；请检查管理认证和持久化配置")
	}
	log("Redis已保存内存数据，继续切换")
	return nil
}
func (m *Manager) verifyDatabaseUpgrade(ctx context.Context, id, prefix string, log func(string)) error {
	unit := "ezpanel-upgrade-" + id
	binary := "mysqld"
	if id == "mariadb" {
		binary = "mariadbd"
	}
	dir := "/run/" + unit
	log("使用独立私有socket检查数据库升级，不开放业务端口")
	args := []string{"--unit=" + unit, "--collect", "--service-type=simple", "--property=User=panel-mysql", "--property=Group=panel-mysql", "--property=RuntimeDirectory=" + unit, "--property=RuntimeDirectoryMode=0700", "--", prefix + "/bin/" + binary, "--defaults-file=" + m.configPath(id), "--basedir=" + prefix, "--skip-networking", "--socket=" + dir + "/mysql.sock", "--pid-file=" + dir + "/mysql.pid"}
	if id == "mysql" {
		args = append(args, "--mysqlx=OFF", "--event-scheduler=OFF", "--skip-replica-start")
	} else {
		args = append(args, "--event-scheduler=OFF", "--skip-slave-start")
	}
	if _, e := m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemd-run", args...); e != nil {
		return e
	}
	deadline := time.Now().Add(5 * time.Minute)
	for {
		connection, e := net.DialTimeout("unix", dir+"/mysql.sock", time.Second)
		if e == nil {
			connection.SetReadDeadline(time.Now().Add(2 * time.Second))
			header := make([]byte, 5)
			_, e = io.ReadFull(connection, header)
			connection.Close()
			if e == nil && header[4] == 10 {
				_, e = m.Run(ctx, 2*time.Minute, nil, "/usr/bin/systemctl", "stop", unit+".service")
				return e
			}
		}
		if time.Now().After(deadline) {
			return errors.New("隔离数据库升级未达到可连接状态")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
func (m *Manager) cleanPrevious(record *sourceInstall) error {
	if record.PreviousPrefix == "" || record.UpdateFailed {
		return nil
	}
	for _, previous := range record.PreviousProcesses {
		current, e := processInfo(previous.PID)
		if e == nil && current.Started == previous.Started {
			return nil
		}
		if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	root := m.installRoot(record.ID)
	if record.PreviousPrefix == root {
		for _, dir := range []string{"bin", "sbin", "lib", "lib64", "include", "share", "scripts", "support-files", "man", "docs"} {
			if e := os.RemoveAll(filepath.Join(root, dir)); e != nil {
				return e
			}
		}
	} else {
		if !strings.HasPrefix(record.PreviousPrefix, root+"/releases/") {
			return errors.New("旧程序目录不在受控版本目录")
		}
		if e := os.RemoveAll(record.PreviousPrefix); e != nil {
			return e
		}
	}
	record.PreviousPrefix = ""
	record.PreviousProcesses = nil
	return m.writeRecord(*record)
}
func (m *Manager) Cleanup(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, a := range applications {
				if e := m.lock(a.id); e != nil {
					continue
				}
				record, e := m.sourceRecord(a.id)
				if e == nil && record.PreviousPrefix != "" {
					_ = m.cleanPrevious(&record)
				}
				m.unlock(a.id)
			}
		}
	}
}

// hotNginx 在平台实现中使用固定进程身份和systemd主进程接管。
