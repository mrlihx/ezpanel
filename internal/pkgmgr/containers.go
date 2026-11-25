package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

// 固定官方镜像版本，安装窗口的版本号与实际拉取的标签保持一致。
var runtimeTags = map[string][]string{
	"openresty":  {"1.31.8.1", "1.29.2.4"},
	"php-8.5":    {"8.5.12"},
	"php-8.4":    {"8.4.27"},
	"php-8.3":    {"8.3.36"},
	"php-8.2":    {"8.2.35"},
	"php-8.1":    {"8.1.34"},
	"php-8.0":    {"8.0.30"},
	"php-7.4":    {"7.4.33"},
	"python":     {"3.14.10", "3.15.2", "3.15.1", "3.14.9", "3.13.18", "3.13.17", "3.12.16", "3.12.15", "3.11.18", "3.11.17", "3.10.20", "3.9.25", "3.8.20"},
	"nodejs":     {"24.22.3", "26.13.1", "26.13.0", "25.9.0", "24.22.2", "23.11.1", "22.23.5", "22.23.4", "21.7.3", "20.20.2", "18.20.8", "16.20.2"},
	"go":         {"1.26.11", "1.27.4", "1.27.3", "1.26.10", "1.25.14", "1.24.13", "1.23.12", "1.22.12", "1.21.13", "1.20.14"},
	"mysql":      {"8.4.13", "8.0.46", "5.7.44"},
	"postgresql": {"18.7", "17.12", "16.16", "15.20", "14.25", "13.23", "12.22", "11.22", "10.23", "9.6.24"},
	"mongodb":    {"8.0.38", "7.0.48"},
	"redis":      {"8.10.3", "7.4.12"},
	"memcached":  {"1.6.45"},
}

var openRestyImageTags = map[string]string{
	"1.31.8.1": "1.31.8.1-0-alpine",
	"1.29.2.4": "1.29.2.4-1-alpine",
}

func UsesDocker(id string) bool       { return len(runtimeTags[id]) != 0 }
func IsProjectRuntime(id string) bool { return id == "nodejs" || id == "python" || id == "go" }

var registryHost = regexp.MustCompile(`^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$`)
var registryPrefix = regexp.MustCompile(`^(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)*$`)

func dockerMirror(address string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
	invalid := errors.New("请输入有效的 HTTPS 镜像加速地址，可带端口和仓库前缀")
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || !registryPrefix.MatchString(u.Path) {
		return "", invalid
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if net.ParseIP(host) == nil && !registryHost.MatchString(host) || (host != "localhost" && !strings.ContainsAny(host, ".:") && port == "") {
		return "", invalid
	}
	if port != "" {
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil || number == 0 {
			return "", invalid
		}
	}
	return "https://" + strings.ToLower(strings.TrimSuffix(u.Host, ":")) + u.Path, nil
}

// DefaultDockerMirrors 新安装面板默认内置的 Docker 镜像加速地址。
var DefaultDockerMirrors = []string{
	"https://docker.1ms.run",
	"https://docker.m.daocloud.io",
	"https://hub.rat.dev",
	"https://ccr.ccs.tencentyun.com",
	"https://docker.xuanyuan.me",
}

func DefaultDockerDaemonJSON() []byte {
	data, _ := json.MarshalIndent(map[string]any{
		"registry-mirrors": DefaultDockerMirrors,
	}, "", "  ")
	return append(data, '\n')
}

func (m *Manager) dockerMirrorsMarker() string {
	if m.cfg.InstallDir == "" {
		return ""
	}
	return filepath.Join(m.cfg.DataDir(), "pkgmgr", "docker-mirrors-initialized")
}

// MarkDefaultDockerMirrorsInitialized 记录默认 Docker 镜像加速已完成初始化，后续不再覆盖用户自定义配置。
func (m *Manager) MarkDefaultDockerMirrorsInitialized() {
	marker := m.dockerMirrorsMarker()
	if marker == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0700); err != nil {
		return
	}
	_ = os.WriteFile(marker, []byte("1\n"), 0600)
}

// EnsureDefaultDockerMirrors 在全新安装环境中仅执行一次：若 /etc/docker/daemon.json 不存在
// 或未配置 registry-mirrors，则写入内置的 5 个默认镜像加速地址，并在 Docker 运行时自动重载生效。
func (m *Manager) EnsureDefaultDockerMirrors(ctx context.Context) {
	path := m.configPath("docker")
	marker := m.dockerMirrorsMarker()
	if path == "" || marker == "" {
		return
	}
	if _, err := os.Stat(marker); err == nil {
		return
	}
	updated := false
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return
		}
		if err := os.WriteFile(path, DefaultDockerDaemonJSON(), 0644); err != nil {
			return
		}
		updated = true
	} else if err == nil {
		var doc map[string]json.RawMessage
		if json.Unmarshal(raw, &doc) == nil && doc != nil {
			var existing []string
			if val, ok := doc["registry-mirrors"]; ok {
				_ = json.Unmarshal(val, &existing)
			}
			if len(existing) == 0 {
				doc["registry-mirrors"], _ = json.Marshal(DefaultDockerMirrors)
				if merged, err := json.MarshalIndent(doc, "", "  "); err == nil {
					if err := os.WriteFile(path, append(merged, '\n'), 0644); err == nil {
						updated = true
					}
				}
			}
		}
	} else {
		return
	}
	m.MarkDefaultDockerMirrorsInitialized()
	if updated {
		if systemctl, err := m.LookPath("systemctl"); err == nil {
			if _, err := m.Run(ctx, 5*time.Second, nil, systemctl, "is-active", "--quiet", "docker.service"); err == nil {
				if _, err := m.Run(ctx, 15*time.Second, nil, systemctl, "reload", "docker.service"); err != nil {
					_, _ = m.Run(ctx, 30*time.Second, nil, systemctl, "restart", "docker.service")
				}
			}
		}
	}
}

// 复用 Docker 基础配置中已保存的镜像地址，供本次安装直接选择；若尚未配置则回退内置默认加速地址。
func (m *Manager) DockerMirrors() []string {
	var config struct {
		Mirrors []string `json:"registry-mirrors"`
	}
	content, err := m.ReadFile(m.configPath("docker"))
	if err != nil {
		if os.IsNotExist(err) {
			return slices.Clone(DefaultDockerMirrors)
		}
		return nil
	}
	if json.Unmarshal(content, &config) != nil {
		return nil
	}
	addresses := make([]string, 0, len(config.Mirrors))
	for _, address := range config.Mirrors {
		if mirror, err := dockerMirror(address); err == nil && !contains(addresses, mirror) {
			addresses = append(addresses, mirror)
		}
	}
	return addresses
}

func containerReleases(id string) []Release {
	tags := runtimeTags[id]
	rows := make([]Release, 0, len(tags))
	for _, tag := range tags {
		image := id + ":" + tag
		switch id {
		case "openresty":
			image = "openresty/openresty:" + openRestyImageTags[tag]
		case "mongodb":
			image = "mongo:" + tag
		case "postgresql":
			image = "postgres:" + tag + "-alpine"
		case "nodejs", "python", "go":
			image = projectImage(id, tag)
		}
		if IsPHP(id) {
			image = "php:" + tag + "-fpm"
		}
		row := Release{Version: tag, Source: "Docker Hub", Method: "docker", URL: image}
		if id == "mysql" && tag == "5.7.44" {
			row.Platform = "linux/amd64"
		}
		rows = append(rows, row)
	}
	return rows
}

func projectImage(id, version string) string {
	switch id {
	case "nodejs":
		if version == "16.20.2" {
			return "node:" + version + "-bullseye-slim"
		}
		return "node:" + version + "-bookworm-slim"
	case "python":
		return "python:" + version + "-slim-bookworm"
	default:
		return "golang:" + version + "-bookworm"
	}
}

func (m *Manager) docker(ctx context.Context, timeout time.Duration, args ...string) (executor.Result, error) {
	path, err := m.LookPath("docker")
	if err != nil {
		return executor.Result{}, errors.New("请先在 Docker 页面安装 Docker 引擎")
	}
	result, err := m.Run(ctx, timeout, nil, path, args...)
	if err != nil && strings.TrimSpace(result.Stderr) != "" {
		err = fmt.Errorf("Docker 操作失败：%s", strings.TrimSpace(result.Stderr))
	}
	return result, err
}

type containerInfo struct {
	ID         string `json:"Id"`
	ImageID    string `json:"Image"`
	Mounts     []struct{ Source, Destination string }
	HostConfig struct {
		PortBindings map[string][]struct{ HostIP, HostPort string }
		Memory       uint64
	}
	Config struct {
		Cmd    []string
		Labels map[string]string
		Env    []string
		Image  string
	}
	State struct {
		Running   bool
		Status    string
		ExitCode  int
		Pid       int32
		StartedAt string
	}
	NetworkSettings struct {
		Networks map[string]struct{ IPAddress string }
		Ports    map[string][]struct {
			HostPort string
		}
	}
}

func (info containerInfo) phpVersion() string {
	for _, env := range info.Config.Env {
		if version, ok := strings.CutPrefix(env, "PHP_VERSION="); ok {
			return version
		}
	}
	return info.Config.Labels["ezpanel.version"]
}

func (info containerInfo) databasePort(id string) string {
	for _, binding := range info.HostConfig.PortBindings[databasePorts[id]+"/tcp"] {
		if binding.HostIP == "127.0.0.1" || binding.HostIP == "0.0.0.0" || binding.HostIP == "" {
			return binding.HostPort
		}
	}
	return ""
}

func (info containerInfo) databaseRemoteAccess(id string) bool {
	for _, binding := range info.HostConfig.PortBindings[databasePorts[id]+"/tcp"] {
		if binding.HostIP == "0.0.0.0" || binding.HostIP == "" {
			return true
		}
	}
	return false
}

func (m *Manager) inspectContainer(ctx context.Context, name string) (containerInfo, bool, error) {
	result, err := m.docker(ctx, 5*time.Second, "container", "inspect", name)
	var rows []containerInfo
	if err != nil {
		if strings.Contains(result.Stderr, "No such container") || strings.Contains(result.Stderr, "No such object") {
			return containerInfo{}, false, nil
		}
		return containerInfo{}, false, err
	}
	if err := json.Unmarshal([]byte(result.Stdout), &rows); err != nil || len(rows) != 1 {
		return containerInfo{}, false, errors.New("无法读取容器状态")
	}
	if rows[0].Config.Labels["ezpanel.managed"] != "true" {
		return containerInfo{}, false, errors.New("同名容器由外部创建")
	}
	return rows[0], true, nil
}

func (m *Manager) dockerStatus(ctx context.Context, id string) (State, error) {
	a, _ := app(id)
	state := State{ID: id, Name: a.name, Category: a.category, Description: a.description, PkgManager: "Docker", InstallDir: m.defaultInstallRoot(id)}
	if IsProjectRuntime(id) {
		items, err := m.RuntimeEnvironments(id)
		if err != nil {
			return state, err
		}
		state.InstanceCount = len(items)
		for _, item := range items {
			if !contains(state.InstalledVersions, item.Version) {
				state.InstalledVersions = append(state.InstalledVersions, item.Version)
			}
		}
		state.Installed, state.Uninstallable = len(items) > 0, len(items) > 0
		if state.Installed {
			state.Version = &state.InstalledVersions[0]
		}
		_, err = m.docker(ctx, 5*time.Second, "info", "--format", "{{.ServerVersion}}")
		if err != nil {
			state.Reason = err.Error()
		} else {
			state.Installable = true
		}
		return state, nil
	}
	info, exists, err := m.inspectContainer(ctx, "ezpanel-"+id)
	if err != nil {
		state.Reason = err.Error()
		return state, nil
	}
	state.Installable = !exists
	if IsDatabaseContainer(id) {
		if instances, err := m.databaseContainers(ctx, id); err == nil {
			state.InstanceCount += len(instances)
			for _, instance := range instances {
				if !contains(state.InstalledVersions, instance.version) {
					state.InstalledVersions = append(state.InstalledVersions, instance.version)
				}
				if instance.state == "running" {
					state.RunningInstances++
				}
			}
		}
		if exists {
			state.InstanceCount++
			if info.State.Running {
				state.RunningInstances++
			}
			version := info.Config.Labels["ezpanel.version"]
			if !contains(state.InstalledVersions, version) {
				state.InstalledVersions = append([]string{version}, state.InstalledVersions...)
			}
		}
	}
	if IsDatabaseContainer(id) && state.InstanceCount > 0 {
		state.Updateable = true
	}
	if !exists {
		return state, nil
	}
	version := info.Config.Labels["ezpanel.version"]
	if IsPHP(id) {
		version = info.phpVersion()
	}
	if id == "openresty" && version == "alpine" && info.State.Running {
		result, err := m.docker(ctx, 3*time.Second, "exec", "ezpanel-openresty", "openresty", "-v")
		if actual := versionPattern.FindString(result.Stdout + result.Stderr); err == nil && actual != "" {
			version = actual
		}
	}
	state.Installed, state.Manageable, state.Uninstallable = true, true, true
	state.Version, state.Running = &version, &info.State.Running
	if IsPHP(id) || id == "openresty" || IsDatabaseContainer(id) {
		state.Updateable = true
		state.UpdateVersion = m.containerUpdateVersion(ctx, id, version)
	}
	if id == "mysql" || id == "postgresql" || id == "mongodb" || id == "redis" {
		state.DataDir = m.serviceDataDir(id)
		state.InitialPasswordPath = filepath.Join(m.serviceStateDir(id), "root-password")
	}
	return state, nil
}

func (m *Manager) dockerRelease(ctx context.Context, id, version string, selected ...SourceSelection) (Release, error) {
	releases, err := m.Releases(ctx, id, false, selected...)
	if err != nil {
		return Release{}, err
	}
	for _, release := range releases {
		if version == "" || release.Version == version {
			if release.UnavailableReason != "" {
				return Release{}, errors.New(release.UnavailableReason)
			}
			return release, nil
		}
	}
	return Release{}, errors.New("请选择应用提供的镜像版本")
}

func releasePlatform(release Release) string {
	if release.Platform != "" {
		return release.Platform
	}
	return "linux/" + runtime.GOARCH
}

func (m *Manager) installDocker(ctx context.Context, id, version string, log func(string), selected ...SourceSelection) (State, error) {
	if IsProjectRuntime(id) {
		if _, err := m.InstallRuntimeEnvironment(ctx, id, version, log, selected...); err != nil {
			return State{}, err
		}
		return m.dockerStatus(ctx, id)
	}
	return m.changeContainer(ctx, id, version, false, log, selected...)
}

func (m *Manager) changeContainer(ctx context.Context, id, version string, upgrade bool, log func(string), selected ...SourceSelection) (State, error) {
	release, err := m.dockerRelease(ctx, id, version, selected...)
	if err != nil {
		return State{}, err
	}
	err = m.ApplicationChange(id, func() error {
		name := "ezpanel-" + id
		old, exists, err := m.inspectContainer(ctx, name)
		if err != nil {
			return err
		}
		if upgrade {
			if !exists {
				return errors.New("应用容器尚未安装")
			}
			if IsPHP(id) {
				return m.upgradePHPContainer(ctx, id, release, old, log)
			}
			return m.upgradeContainerInstance(ctx, id, "default", release, old, log)
		}
		if exists {
			return errors.New("应用已安装")
		}
		platform := releasePlatform(release)
		reference, err := m.pullContainer(ctx, release.URL, platform, log)
		if err != nil {
			return err
		}
		image := reference
		if IsPHP(id) {
			image, err = m.buildFPMImage(ctx, id, reference, release.Version, log)
			if err != nil {
				return err
			}
		}
		args, err := m.serviceContainerArgs(id, "")
		if err != nil {
			return err
		}
		command := m.serviceCommand(id)
		if IsDatabaseContainer(id) {
			if err := m.prepareDatabaseConfig(id, "default"); err != nil {
				return err
			}
			args = append(args, m.databaseConfigMount(id, "default")...)
			command = m.databaseConfigCommand(id, "default")
		}
		err = m.createServiceContainer(ctx, []string{"run", "-d"}, name, release.Version, platform, args, image, command)
		if err == nil && IsPHP(id) {
			link := filepath.Join(m.defaultInstallRoot(id), "active")
			if _, found := os.Readlink(link); found == nil {
				err = selectPHPSocket(link, "run")
				if err != nil {
					_, _ = m.docker(context.WithoutCancel(ctx), 30*time.Second, "rm", "-f", name)
				}
			}
			if err == nil {
				_ = m.EnsurePHPCLIWrapper(id)
			}
		}
		if err == nil && id == "openresty" {
			if _, err = m.OpenRestyCommand(ctx, "-t"); err != nil {
				_, _ = m.docker(context.WithoutCancel(ctx), 30*time.Second, "rm", "-f", name)
			}
		}
		return err
	})
	if err != nil {
		return State{}, err
	}
	return m.dockerStatus(ctx, id)
}

func (m *Manager) createServiceContainer(ctx context.Context, action []string, name, version, platform string, args []string, image string, command []string) error {
	dockerArgs := append(action, "--pull=never", "--name", name, "--label", "ezpanel.managed=true", "--label", "ezpanel.version="+version, "--restart", "unless-stopped", "--log-opt", "max-size=10m", "--log-opt", "max-file=3", "--platform", platform)
	// OpenResty 和升级验证容器分别通过参数指定 host 和 none。
	if !slices.Contains(args, "--network") {
		if err := m.ensureManagedNetwork(ctx); err != nil {
			return err
		}
		dockerArgs = append(dockerArgs, "--network", ManagedNetwork)
	}
	dockerArgs = append(dockerArgs, args...)
	dockerArgs = append(dockerArgs, image)
	_, err := m.docker(ctx, 60*time.Second, append(dockerArgs, command...)...)
	return err
}

func (m *Manager) pullContainer(ctx context.Context, reference, platform string, log func(string)) (string, error) {
	if log == nil {
		log = func(string) {}
	}
	path, err := m.LookPath("docker")
	if err != nil {
		return "", errors.New("请先在 Docker 页面安装 Docker 引擎")
	}
	log("正在拉取 " + reference + "（" + platform + "）")
	var missingAttestation bool
	_, err = m.Stream(ctx, 20*time.Minute, nil, func(line string) {
		if strings.Contains(line, "application/vnd.in-toto+json") && strings.Contains(line, "not found") {
			missingAttestation = true
		}
		log(line)
	}, path, "pull", "--platform", platform, reference)
	if err == nil || !missingAttestation || ctx.Err() != nil {
		return reference, err
	}
	// 有些镜像仓库缺少构建证明，按目标平台的运行镜像摘要重试。
	log("镜像源缺少构建证明，正在查找 " + platform + " 运行镜像")
	result, inspectErr := m.docker(ctx, 45*time.Second, "buildx", "imagetools", "inspect", "--raw", reference)
	if inspectErr != nil {
		return "", fmt.Errorf("读取镜像清单失败：%w", inspectErr)
	}
	var index struct {
		Manifests []struct {
			Digest   string
			Platform struct{ OS, Architecture string }
		}
	}
	if err := json.Unmarshal([]byte(result.Stdout), &index); err != nil {
		return "", fmt.Errorf("解析镜像清单失败：%w", err)
	}
	for _, manifest := range index.Manifests {
		if manifest.Platform.OS+"/"+manifest.Platform.Architecture == platform && manifest.Digest != "" {
			repository := reference[:strings.LastIndexByte(reference, ':')]
			resolved := repository + "@" + manifest.Digest
			_, err := m.Stream(ctx, 20*time.Minute, nil, log, path, "pull", "--platform", platform, resolved)
			return resolved, err
		}
	}
	return "", fmt.Errorf("镜像清单中没有 %s 运行镜像", platform)
}

func (m *Manager) dockerService(ctx context.Context, id, action string) (State, error) {
	if IsDatabaseContainer(id) && action != "remove" {
		if err := m.DatabaseInstanceAction(ctx, id, "default", action); err != nil {
			return State{}, err
		}
		return m.dockerStatus(ctx, id)
	}
	err := m.ApplicationChange(id, func() error {
		info, exists, err := m.inspectContainer(ctx, "ezpanel-"+id)
		if err != nil {
			return err
		} else if !exists {
			return errors.New("应用容器尚未安装")
		}
		if action == "reload" {
			if !info.State.Running {
				return errors.New("请先启动服务再重载配置")
			}
			if IsPHP(id) {
				return m.applyPHPConfig(ctx, id, true)
			}
			if _, err := m.OpenRestyCommand(ctx, "-t"); err != nil {
				return err
			}
			_, err := m.OpenRestyCommand(ctx, "-s", "reload")
			return err
		}
		if IsPHP(id) && (action == "start" || action == "restart") {
			if err := m.connectManagedNetwork(ctx, info); err != nil {
				return err
			}
		}
		args := []string{action, "ezpanel-" + id}
		if action == "remove" {
			args = []string{"rm", "-f", args[1]}
		}
		_, err = m.docker(ctx, 60*time.Second, args...)
		if err == nil && action == "remove" && IsDatabaseContainer(id) && m.syncDBPort != nil {
			err = m.syncDBPort(ctx, id, "default", 0, false)
		}
		if err == nil && action == "remove" && IsPHP(id) {
			m.RemovePHPCLIWrapper(ctx, id)
			// 同步清理配置与运行目录，确保重新安装该 PHP 版本时生成全新默认配置（含默认禁用函数）。
			root := m.defaultInstallRoot(id)
			for _, sub := range []string{"conf", "run", "build", "active"} {
				_ = os.RemoveAll(filepath.Join(root, sub))
			}
		}
		return err
	})
	if err != nil {
		return State{}, err
	}
	return m.dockerStatus(ctx, id)
}

func (m *Manager) OpenRestyCommand(ctx context.Context, args ...string) (executor.Result, error) {
	info, exists, err := m.inspectContainer(ctx, "ezpanel-openresty")
	if err != nil || !exists {
		if err == nil {
			err = errors.New("请先安装 OpenResty")
		}
		return executor.Result{}, err
	}
	if info.State.Running || len(args) != 1 || (args[0] != "-t" && args[0] != "-T") {
		return m.docker(ctx, 30*time.Second, append([]string{"exec", "ezpanel-openresty", "openresty", "-c", m.layout().OpenRestyConfig()}, args...)...)
	}
	image, err := m.docker(ctx, 5*time.Second, "inspect", "--format", "{{.Image}}", "ezpanel-openresty")
	if err != nil {
		return executor.Result{}, err
	}
	return m.docker(ctx, 45*time.Second, "run", "--rm", "--volumes-from", "ezpanel-openresty", strings.TrimSpace(image.Stdout), "openresty", args[0], "-c", m.layout().OpenRestyConfig())
}

func (m *Manager) buildFPMImage(ctx context.Context, id, base, version string, log func(string)) (string, error) {
	dir := filepath.Join(m.defaultInstallRoot(id), "build")
	if err := os.MkdirAll(dir, 0750); err != nil {
		return "", err
	}
	dockerfile := "FROM " + base + "\nRUN docker-php-ext-install -j2 mysqli pdo_mysql\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return "", err
	}
	path, err := m.LookPath("docker")
	if err != nil {
		return "", err
	}
	image := "ezpanel/" + id + ":fpm-" + version
	_, err = m.Stream(ctx, 20*time.Minute, nil, log, path, "build", "-t", image, dir)
	return image, err
}
