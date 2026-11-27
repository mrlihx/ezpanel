package pkgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"
)

type FRPService struct {
	Role       string  `json:"role"`
	Installed  bool    `json:"installed"`
	Running    bool    `json:"running"`
	Version    string  `json:"version"`
	ConfigPath string  `json:"configPath"`
	LogPath    string  `json:"logPath"`
	Release    Release `json:"-"`
}

func frpReleases(data []byte) ([]Release, error) {
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &releases); err != nil {
		return nil, err
	}
	rows := []Release{}
	versionPattern := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for _, item := range releases {
		version := strings.TrimPrefix(item.Tag, "v")
		if item.Draft || item.Prerelease || !versionPattern.MatchString(version) {
			continue
		}
		for _, asset := range item.Assets {
			if asset.Name == "frp_"+version+"_linux_"+runtime.GOARCH+".tar.gz" && strings.HasPrefix(asset.URL, "https://github.com/fatedier/frp/releases/download/") {
				rows = append(rows, Release{Version: version, Source: "Frp 官方源", Method: "binary", URL: asset.URL, SHA256: strings.TrimPrefix(asset.Digest, "sha256:"), Platform: "linux/" + runtime.GOARCH})
			}
		}
	}
	sort.Slice(rows, func(i, j int) bool { return newer(rows[i].Version, rows[j].Version) })
	if len(rows) == 0 {
		return nil, errors.New("Frp 官方源没有当前架构的正式版本")
	}
	return rows, nil
}

func ValidFRPRole(role string) bool { return role == "frps" || role == "frpc" }
func frpUnit(role string) string    { return "ezpanel-" + role + ".service" }
func (m *Manager) frpBinary(role string) string {
	return filepath.Join(m.layout().AppDir("frp"), "bin", role)
}
func (m *Manager) frpRecord(role string) string {
	return filepath.Join(m.layout().AppDir("frp"), "."+role+".json")
}
func (m *Manager) FRPServices(ctx context.Context) ([]FRPService, error) {
	rows := make([]FRPService, 0, 2)
	for _, role := range []string{"frps", "frpc"} {
		row := FRPService{Role: role, ConfigPath: filepath.Join(m.layout().AppDir("frp"), role+".toml"), LogPath: filepath.Join(m.layout().AppLogDir("frp"), role+".log")}
		b, err := os.ReadFile(m.frpRecord(role))
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		if err == nil {
			if err = json.Unmarshal(b, &row.Release); err != nil {
				return nil, err
			}
			row.Installed, row.Version = true, row.Release.Version
			result, _ := m.Run(ctx, 10*time.Second, nil, "/usr/bin/systemctl", "is-active", frpUnit(role))
			row.Running = strings.TrimSpace(result.Stdout) == "active"
		}
		rows = append(rows, row)
	}
	return rows, nil
}
func (m *Manager) frpStatus(ctx context.Context) (State, error) {
	a, _ := app("frp")
	state := State{ID: a.id, Name: a.name, Category: a.category, Description: a.description, PkgManager: "Frp 官方源", InstallDir: m.layout().AppDir("frp")}
	rows, err := m.FRPServices(ctx)
	if err != nil {
		return state, err
	}
	running := false
	for _, row := range rows {
		if !row.Installed {
			continue
		}
		state.Installed = true
		state.InstanceCount++
		if row.Running {
			running = true
			state.RunningInstances++
		}
		if state.Version == nil || newer(*state.Version, row.Version) {
			v := row.Version
			state.Version = &v
		}
		if !slices.Contains(state.InstalledVersions, row.Version) {
			state.InstalledVersions = append(state.InstalledVersions, row.Version)
		}
	}
	state.Manageable, state.Uninstallable, state.Updateable = state.Installed, state.Installed, state.Installed
	if state.Installed {
		state.Running = &running
	}
	if _, err = m.Detect(); err != nil {
		state.Reason = err.Error()
		state.Manageable = false
		return state, err
	}
	state.Installable = !state.Installed
	info := m.VersionInfo("frp")
	state.UpdateNotice = info.Notice
	for _, release := range info.List {
		if state.Version != nil && newer(release.Version, *state.Version) {
			state.UpdateVersion = release.Version
			break
		}
	}
	return state, nil
}

func frpUnitContent(role, binary, configuration, directory string) string {
	// ExecStart 的参数需要引用；WorkingDirectory 直接接收路径，仅转义说明符。
	quote := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `%`, `%%`, `$`, `$$`).Replace(s) + `"`
	}
	return "[Unit]\nDescription=EzPanel " + role + "\nWants=network-online.target\nAfter=network-online.target\n\n[Service]\nType=simple\nWorkingDirectory=" + strings.ReplaceAll(directory, "%", "%%") + "\nExecStart=" + quote(binary) + " -c " + quote(configuration) + "\nRestart=on-failure\nRestartSec=5\nTimeoutStopSec=30\nUMask=0077\n\n[Install]\nWantedBy=multi-user.target\n"
}

func (m *Manager) installFRP(ctx context.Context, version string, roles []string, updating bool, log func(string)) (state State, err error) {
	if log == nil {
		log = func(string) {}
	}
	err = m.ApplicationChange("frp", func() (err error) {
		if _, err = m.Detect(); err != nil {
			return err
		}
		services, err := m.FRPServices(ctx)
		if err != nil {
			return err
		}
		if updating {
			roles = nil
			for _, row := range services {
				if row.Installed {
					if newer(row.Version, version) {
						return errors.New("不能将已安装的 Frp 服务降级")
					}
					if newer(version, row.Version) {
						roles = append(roles, row.Role)
					}
				}
			}
			if len(roles) == 0 {
				return errors.New("没有需要升级的 Frp 服务")
			}
		}
		releases, releaseErr := m.Releases(ctx, "frp", false)
		if releaseErr != nil && version == "" {
			return releaseErr
		}
		var release Release
		if version == "" {
			version = releases[0].Version
		}
		for _, r := range releases {
			if r.Version == version {
				release = r
				break
			}
		}
		if release.URL == "" {
			for _, row := range services {
				if row.Version == version {
					release = row.Release
					break
				}
			}
		}
		if release.URL == "" {
			if releaseErr != nil {
				return releaseErr
			}
			return errors.New("所选版本不在官方版本目录，请刷新版本列表")
		}
		log("下载 Frp " + version + "，安装前保留原配置与服务状态")
		data, err := m.sourceRequest(ctx, release.URL, 128<<20, 10*time.Minute)
		if err != nil {
			return err
		}
		if release.SHA256 == "" {
			b, e := m.sourceRequest(ctx, release.URL[:strings.LastIndex(release.URL, "/")+1]+"frp_sha256_checksums.txt", 1<<20)
			if e != nil {
				return e
			}
			for _, line := range strings.Split(string(b), "\n") {
				fields := strings.Fields(line)
				if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == filepath.Base(release.URL) {
					release.SHA256 = fields[0]
					break
				}
			}
		}
		checksum := sha256.Sum256(data)
		if !strings.EqualFold(hex.EncodeToString(checksum[:]), release.SHA256) {
			return errors.New("Frp 官方归档 SHA256 校验失败")
		}
		root := m.layout().AppDir("frp")
		if err = os.MkdirAll(filepath.Join(root, "bin"), 0755); err != nil {
			return err
		}
		if err = os.MkdirAll(m.layout().AppLogDir("frp"), 0750); err != nil {
			return err
		}
		work, err := os.MkdirTemp(root, ".install-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(work)
		archive := filepath.Join(work, "frp.tar.gz")
		if err = os.WriteFile(archive, data, 0600); err != nil {
			return err
		}
		data = nil
		src := filepath.Join(work, "source")
		if err = extractSource(archive, src); err != nil {
			return err
		}
		staged := map[string]string{}
		initial := map[string][]byte{}
		for _, role := range roles {
			candidate := filepath.Join(src, role)
			if err = os.Chmod(candidate, 0755); err != nil {
				return err
			}
			configuration := filepath.Join(root, role+".toml")
			if _, e := os.Stat(configuration); os.IsNotExist(e) {
				initial[role], err = m.frpDefault(role)
				if err != nil {
					return err
				}
				configuration = filepath.Join(work, role+".toml")
				if err = os.WriteFile(configuration, initial[role], 0600); err != nil {
					return err
				}
			} else if e != nil {
				return e
			}
			if err = m.verifyFRP(ctx, candidate, configuration); err != nil {
				return err
			}
			staged[role] = candidate
		}
		// 全部服务校验通过后再切换；任一切换失败，恢复已切换的二进制、单元及运行状态。
		type previous struct {
			role         string
			binary       bool
			unit, record []byte
			running      bool
		}
		changed := []previous{}
		defer func() {
			if err == nil {
				return
			}
			rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			for i := len(changed) - 1; i >= 0; i-- {
				old := changed[i]
				_, e := m.Run(rollbackCtx, 30*time.Second, nil, "/usr/bin/systemctl", "stop", frpUnit(old.role))
				err = errors.Join(err, e)
				binary := m.frpBinary(old.role)
				if old.binary {
					err = errors.Join(err, os.Rename(binary+".previous", binary))
				} else {
					err = errors.Join(err, writeHostConfig(binary, nil))
				}
				err = errors.Join(err, writeHostConfig("/etc/systemd/system/"+frpUnit(old.role), old.unit), writeHostConfig(m.frpRecord(old.role), old.record))
				if len(old.unit) > 0 {
					err = errors.Join(err, os.Chmod("/etc/systemd/system/"+frpUnit(old.role), 0644))
				}
			}
			_, e := m.Run(rollbackCtx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload")
			err = errors.Join(err, e)
			for _, old := range changed {
				if old.running {
					_, e = m.Run(rollbackCtx, 30*time.Second, nil, "/usr/bin/systemctl", "start", frpUnit(old.role))
					err = errors.Join(err, e)
				}
			}
			log("切换失败，已执行原服务恢复")
		}()
		for _, role := range roles {
			binary := m.frpBinary(role)
			unitPath := "/etc/systemd/system/" + frpUnit(role)
			old := previous{role: role}
			old.unit, err = os.ReadFile(unitPath)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			old.record, err = os.ReadFile(m.frpRecord(role))
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, row := range services {
				if row.Role == role {
					old.running = row.Running
				}
			}
			if b, e := os.ReadFile(binary); e == nil {
				if err = os.WriteFile(binary+".previous", b, 0755); err != nil {
					return err
				}
				old.binary = true
			} else if !os.IsNotExist(e) {
				return e
			}
			changed = append(changed, old)
			if old.running {
				if _, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "stop", frpUnit(role)); err != nil {
					return err
				}
			}
			if err = os.Rename(staged[role], binary); err != nil {
				return err
			}
			if b := initial[role]; b != nil {
				if err = writeHostConfig(filepath.Join(root, role+".toml"), b); err != nil {
					return err
				}
			}
			if err = writeHostConfig(unitPath, []byte(frpUnitContent(role, binary, filepath.Join(root, role+".toml"), root))); err != nil {
				return err
			}
			if err = os.Chmod(unitPath, 0644); err != nil {
				return err
			}
			record, _ := json.Marshal(release)
			if err = writeHostConfig(m.frpRecord(role), record); err != nil {
				return err
			}
		}
		if _, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
			return err
		}
		for _, old := range changed {
			if old.running {
				if _, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "start", frpUnit(old.role)); err != nil {
					return err
				}
			}
		}
		log("Frp 安装完成，已保留配置、日志及原运行状态；新装服务请配置后手动启动")
		return nil
	})
	if err != nil {
		return state, err
	}
	return m.frpStatus(ctx)
}

func (m *Manager) FRPAction(ctx context.Context, role, action, version string, log func(string)) error {
	switch action {
	case "install", "reinstall":
		_, err := m.installFRP(ctx, version, []string{role}, false, log)
		return err
	default:
		return m.ApplicationChange("frp", func() error {
			if action == "uninstall" {
				return m.removeFRPRole(ctx, role)
			}
			configuration := filepath.Join(m.layout().AppDir("frp"), role+".toml")
			if action == "start" || action == "restart" || action == "reload" {
				if err := m.verifyFRP(ctx, m.frpBinary(role), configuration); err != nil {
					return err
				}
			}
			if action == "start" || action == "restart" {
				// 同步旧版本生成的单元，已安装的服务无需重装即可修复。
				unitPath := "/etc/systemd/system/" + frpUnit(role)
				unit := frpUnitContent(role, m.frpBinary(role), configuration, m.layout().AppDir("frp"))
				current, err := os.ReadFile(unitPath)
				if err != nil {
					return err
				}
				if string(current) != unit {
					if err = writeHostConfig(unitPath, []byte(unit)); err != nil {
						return err
					}
					if err = os.Chmod(unitPath, 0644); err != nil {
						return err
					}
					if _, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
						return err
					}
				}
			}
			if action == "reload" {
				if role != "frpc" {
					return errors.New("frps 不支持重载，请使用重启")
				}
				_, err := m.Run(ctx, 30*time.Second, nil, m.frpBinary(role), "reload", "-c", configuration)
				return err
			}
			args := []string{action, frpUnit(role)}
			if action == "stop" {
				args = []string{"disable", "--now", frpUnit(role)}
			}
			_, err := m.Run(ctx, 40*time.Second, nil, "/usr/bin/systemctl", args...)
			if err == nil && action == "start" {
				err = m.enableSystemdStartup(ctx, 40*time.Second, frpUnit(role))
			}
			return err
		})
	}
}
func (m *Manager) removeFRPRole(ctx context.Context, role string) error {
	_, err := m.Run(ctx, 40*time.Second, nil, "/usr/bin/systemctl", "disable", "--now", frpUnit(role))
	if err != nil {
		return err
	}
	if err = writeHostConfig("/etc/systemd/system/"+frpUnit(role), nil); err != nil {
		return err
	}
	if _, err = m.Run(ctx, 30*time.Second, nil, "/usr/bin/systemctl", "daemon-reload"); err != nil {
		return err
	}
	for _, path := range []string{m.frpBinary(role), m.frpBinary(role) + ".previous", m.frpRecord(role)} {
		if err = writeHostConfig(path, nil); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) removeFRP(ctx context.Context) (State, error) {
	err := m.ApplicationChange("frp", func() error {
		services, err := m.FRPServices(ctx)
		if err != nil {
			return err
		}
		for _, row := range services {
			if row.Installed {
				if err = m.removeFRPRole(ctx, row.Role); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return State{}, err
	}
	return m.frpStatus(ctx)
}
