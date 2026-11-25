package pkgmgr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/config"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func phpActiveSocket(layout config.Layout, branch, socket string) string {
	root := layout.PHPDir(branch)
	if _, err := os.Readlink(filepath.Join(root, "active")); err == nil {
		return filepath.Join(root, "active", socket)
	}
	return filepath.Join(root, "run", socket)
}

func (m *Manager) phpActiveSocket(branch, socket string) string {
	return phpActiveSocket(m.layout(), branch, socket)
}

const phpInventoryCode = `
$ext = [];
foreach (array_merge(get_loaded_extensions(), get_loaded_extensions(true)) as $name) $ext[strtolower($name)] = phpversion($name) ?: "";
echo json_encode(["version" => PHP_VERSION, "dir" => PHP_EXTENSION_DIR, "modules" => $ext]);
`

type phpInventory struct {
	Version string
	Dir     string
	Modules map[string]string
	Builtin map[string]bool
}

func (m *Manager) containerPHPInventory(ctx context.Context, id string, run []string) (phpInventory, error) {
	args := append(slices.Clone(run), "php", "-c", m.phpDirectory(id)+"/php.ini", "-d", "display_startup_errors=stderr", "-r", phpInventoryCode)
	result, err := m.docker(ctx, 45*time.Second, args...)
	var info phpInventory
	if err != nil {
		return info, err
	}
	if err = json.Unmarshal([]byte(result.Stdout), &info); err != nil {
		return info, errors.New("无法读取 PHP 扩展清单")
	}
	if strings.Contains(strings.ToLower(result.Stderr), "startup:") {
		return info, fmt.Errorf("PHP 扩展加载失败：%s", strings.TrimSpace(result.Stderr))
	}
	// 读取无 ini 加载时的 PHP 默认内置扩展列表，升级时仅校验手动安装的扩展。
	baseArgs := append(slices.Clone(run), "php", "-n", "-r", `echo json_encode(array_map("strtolower", array_merge(get_loaded_extensions(), get_loaded_extensions(true))));`)
	if baseRes, baseErr := m.docker(ctx, 30*time.Second, baseArgs...); baseErr == nil {
		var builtins []string
		if json.Unmarshal([]byte(baseRes.Stdout), &builtins) == nil {
			info.Builtin = make(map[string]bool, len(builtins))
			for _, name := range builtins {
				info.Builtin[name] = true
			}
		}
	}
	return info, nil
}

func missingPHPExtensions(old, next phpInventory) []string {
	var missing []string
	for name, version := range old.Modules {
		if old.Builtin[name] {
			continue
		}
		installed, exists := next.Modules[name]
		if !exists || (version != "" && version != old.Version && version != installed) {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	return missing
}

func (m *Manager) phpUpgradeImage(ctx context.Context, id string, release Release, old containerInfo, args []string, log func(string)) (string, error) {
	name := "ezpanel-" + id
	platform := releasePlatform(release)
	var active []string
	if old.State.Running {
		active = []string{"exec", name}
	} else {
		// 已停止的容器也可能有手工扩展，用其快照读取清单。
		snapshot, err := m.docker(ctx, 2*time.Minute, "commit", name)
		if err != nil {
			return "", err
		}
		image := strings.TrimSpace(snapshot.Stdout)
		defer m.docker(context.WithoutCancel(ctx), 20*time.Second, "image", "rm", image)
		active = []string{"run", "--rm", "--pull=never", "--volumes-from", name + ":ro", image}
	}
	oldPHP, err := m.containerPHPInventory(ctx, id, active)
	if err != nil {
		return "", err
	}
	log(fmt.Sprintf("已读取 PHP %s 的 %d 个已加载模块", oldPHP.Version, len(oldPHP.Modules)))
	reference, err := m.pullContainer(ctx, release.URL, platform, log)
	if err != nil {
		return "", err
	}
	image, err := m.buildFPMImage(ctx, id, reference, release.Version, log)
	if err != nil {
		return "", err
	}

	dir, err := os.MkdirTemp(m.defaultInstallRoot(id), "php-upgrade-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	phpConfig := m.phpDirectory(id)
	run := append([]string{"run", "--rm", "--pull=never", "--platform", platform, "--mount", "type=bind,src=" + phpConfig + ",dst=" + phpConfig + ",readonly"}, image)
	abi, err := m.docker(ctx, 45*time.Second, append(slices.Clone(run), "php", "-n", "-r", "echo PHP_EXTENSION_DIR;")...)
	if err != nil {
		return "", err
	}
	if oldPHP.Dir != strings.TrimSpace(abi.Stdout) {
		return "", errors.New("目标镜像的 PHP 扩展 ABI 已变化，当前分支无法安全保留扩展")
	}
	for _, item := range []struct{ dir, source string }{{"modules", oldPHP.Dir}, {"conf", "/usr/local/etc/php/conf.d"}} {
		if err = os.Mkdir(filepath.Join(dir, item.dir), 0755); err != nil {
			return "", err
		}
		if _, err = m.docker(ctx, time.Minute, "cp", "-L", name+":"+item.source+"/.", filepath.Join(dir, item.dir)); err != nil {
			return "", fmt.Errorf("备份 PHP 扩展及加载配置失败：%w", err)
		}
	}
	// 补齐旧版扩展，目标镜像自带的同名二进制保持原样。
	dockerfile := "FROM " + image + "\nCOPY modules/ /tmp/ezpanel-php-modules/\nCOPY conf/ /usr/local/etc/php/conf.d/\n" +
		"RUN cp -an /tmp/ezpanel-php-modules/. " + oldPHP.Dir + "/ && rm -rf /tmp/ezpanel-php-modules\n"
	if err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0644); err != nil {
		return "", err
	}
	path, err := m.LookPath("docker")
	if err != nil {
		return "", err
	}
	image += "-preserved"
	if _, err = m.Stream(ctx, 5*time.Minute, nil, log, path, "build", "--platform", platform, "-t", image, dir); err != nil {
		return "", err
	}
	run[len(run)-1] = image
	next, err := m.containerPHPInventory(ctx, id, run)
	if err != nil {
		return "", err
	}
	if missing := missingPHPExtensions(oldPHP, next); len(missing) != 0 {
		return "", fmt.Errorf("目标镜像无法保留以下扩展及其版本：%s", strings.Join(missing, "、"))
	}
	if next.Version != release.Version {
		return "", fmt.Errorf("目标镜像的 PHP 版本为 %s，与所选版本不一致", next.Version)
	}
	check := append([]string{"run", "--rm", "--pull=never", "--platform", platform}, args...)
	check = append(append(check, image), m.serviceCommand(id)...)
	if _, err = m.docker(ctx, 45*time.Second, append(check, "-t")...); err != nil {
		return "", fmt.Errorf("新版 PHP-FPM 配置校验失败：%w", err)
	}
	return image, nil
}

func (m *Manager) upgradePHPContainer(ctx context.Context, id string, release Release, old containerInfo, log func(string)) error {
	if log == nil {
		log = func(string) {}
	}
	current := old.phpVersion()
	if series(current) != series(release.Version) || !newer(release.Version, current) {
		return errors.New("请选择当前 PHP 分支中更高的版本")
	}
	root := m.defaultInstallRoot(id)
	runDir := filepath.Join(root, "run")
	previousDir, nextDir := runDir, filepath.Join(root, "standby")
	for _, mount := range old.Mounts {
		if mount.Destination == runDir && mount.Source == nextDir {
			previousDir, nextDir = nextDir, previousDir
			break
		}
	}
	args, err := m.serviceContainerArgs(id, "")
	if err != nil {
		return err
	}
	fpm, err := os.ReadFile(filepath.Join(m.phpDirectory(id), "php-fpm.conf"))
	if err != nil {
		return err
	}
	if PHPDirectives(string(fpm), "www")["listen"] != filepath.Join(runDir, "fpm.sock") ||
		PHPDirectives(string(fpm), "phpmyadmin")["listen"] != filepath.Join(runDir, "phpmyadmin.sock") {
		return errors.New("PHP-FPM 已使用自定义监听地址，无法自动切换")
	}
	if err = EnsureWebDirectory(nextDir); err != nil {
		return err
	}
	for i, arg := range args {
		if arg == "type=bind,src="+runDir+",dst="+runDir {
			args[i] = "type=bind,src=" + nextDir + ",dst=" + runDir
			break
		}
	}
	image, err := m.phpUpgradeImage(ctx, id, release, old, args, log)
	if err != nil {
		return err
	}

	name := "ezpanel-" + id
	pending := name + "-candidate"
	previous := name + "-previous"
	recovery := context.WithoutCancel(ctx)
	if _, exists, err := m.inspectContainer(ctx, pending); err != nil {
		return err
	} else if exists {
		return errors.New("上次升级的候选容器仍在，请先检查其状态")
	}
	// 旧容器和新容器使用各自的套接字目录，可并行接受 FastCGI 请求。
	if err = m.createServiceContainer(ctx, []string{"run", "-d"}, pending, release.Version, releasePlatform(release), args, image, m.serviceCommand(id)); err != nil {
		_, _ = m.docker(recovery, 30*time.Second, "rm", "-fv", pending)
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = m.docker(recovery, 30*time.Second, "rm", "-fv", pending)
		}
	}()
	statusPath := PHPDirectives(string(fpm), "www")["pm.status_path"]
	if err = probePHP(ctx, nextDir, statusPath); err != nil {
		return err
	}
	log("新版 PHP-FPM 已通过配置、扩展和 FastCGI 检查")
	if !old.State.Running {
		if _, err = m.docker(ctx, 2*time.Minute, "stop", "--time", "90", pending); err != nil {
			return err
		}
	}
	if err := m.ApplicationChange("openresty", func() error {
		link := filepath.Join(root, "active")
		if _, err := os.Lstat(link); errors.Is(err, os.ErrNotExist) {
			if err = os.Symlink(filepath.Base(previousDir), link); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := m.migratePHPSocket(ctx, filepath.Join(previousDir, "fpm.sock"), filepath.Join(link, "fpm.sock")); err != nil {
			return err
		}
		if _, exists, err := m.inspectContainer(ctx, previous); err != nil {
			return err
		} else if exists {
			if _, err = m.docker(ctx, 30*time.Second, "rm", "-v", previous); err != nil {
				return err
			}
		}
		if _, err := m.docker(ctx, 15*time.Second, "rename", name, previous); err != nil {
			return err
		}
		if _, err := m.docker(ctx, 15*time.Second, "rename", pending, name); err != nil {
			_, rollback := m.docker(recovery, 15*time.Second, "rename", previous, name)
			return errors.Join(err, rollback)
		}
		if err := selectPHPSocket(link, filepath.Base(nextDir)); err != nil {
			_, move := m.docker(recovery, 15*time.Second, "rename", name, pending)
			_, rollback := m.docker(recovery, 15*time.Second, "rename", previous, name)
			return errors.Join(err, move, rollback)
		}
		committed = true
		return nil
	}); err != nil {
		return err
	}
	log("已切换 PHP-FPM 套接字，旧容器将优雅退出并留存")
	if old.State.Running {
		if _, err := m.docker(recovery, 2*time.Minute, "stop", "--time", "90", "--signal", "SIGQUIT", previous); err != nil {
			log("旧 PHP-FPM 容器退出失败：" + err.Error())
		}
	}
	return nil
}

func selectPHPSocket(link, slot string) error {
	temporary := link + ".next"
	if err := os.Symlink(slot, temporary); err != nil {
		return err
	}
	defer os.Remove(temporary)
	return os.Rename(temporary, link)
}

func probePHP(ctx context.Context, dir, statusPath string) error {
	check, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		var err error
		if statusPath != "" {
			_, err = readFPMStatus(check, "unix", filepath.Join(dir, "fpm.sock"), statusPath)
		} else {
			fields := []string{"REQUEST_METHOD=GET", "SCRIPT_FILENAME=/__ezpanel_fpm_probe_missing.php", "SCRIPT_NAME=/__ezpanel_fpm_probe_missing.php"}
			err = FastCGI(check, "unix", filepath.Join(dir, "fpm.sock"), fields, nil, io.Discard)
		}
		if err == nil {
			var response bytes.Buffer
			fields := []string{"REQUEST_METHOD=GET", "SCRIPT_FILENAME=/__ezpanel_fpm_probe_missing.php", "SCRIPT_NAME=/__ezpanel_fpm_probe_missing.php"}
			err = FastCGI(check, "unix", filepath.Join(dir, "phpmyadmin.sock"), fields, nil, &response)
			if err == nil && response.Len() > 0 {
				return nil
			}
		}
		select {
		case <-check.Done():
			return fmt.Errorf("新版 PHP-FPM 未就绪：%w", check.Err())
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// 首次升级时把旧站点配置中的固定套接字切到别名。此时别名仍指向旧容器。
func (m *Manager) migratePHPSocket(ctx context.Context, from, to string) error {
	files := make(map[string][]byte)
	l := m.layout()
	paths, err := filepath.Glob(filepath.Join(l.OpenRestyConfDir(), "*.conf"))
	if err != nil {
		return err
	}
	for _, path := range append(paths, l.OpenRestyConfig()) {
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(content, []byte("unix:"+from)) {
			files[path] = content
		}
	}
	info, exists, err := m.inspectContainer(ctx, "ezpanel-openresty")
	if err != nil {
		return err
	}
	openRestyWorkers := func(ctx context.Context) (map[string]bool, error) {
		result, err := m.docker(ctx, 5*time.Second, "top", "ezpanel-openresty", "-eo", "pid,args")
		workers := map[string]bool{}
		for line := range strings.SplitSeq(result.Stdout, "\n") {
			if strings.Contains(line, ": worker process") && !strings.Contains(line, "shutting down") {
				workers[strings.Fields(line)[0]] = true
			}
		}
		return workers, err
	}
	apply := func(ctx context.Context, verify bool) error {
		if !exists {
			return nil
		}
		if verify {
			result, err := m.OpenRestyCommand(ctx, "-T")
			if err != nil {
				return err
			}
			if result.Truncated || strings.Contains(result.Stdout, "unix:"+from) {
				return errors.New("仍有 OpenResty 配置使用旧 PHP 套接字，无法安全切换")
			}
		}
		if len(files) == 0 || !info.State.Running {
			return nil
		}
		before, err := openRestyWorkers(ctx)
		if err != nil {
			return err
		}
		if _, err = m.OpenRestyCommand(ctx, "-s", "reload"); err != nil {
			return err
		}
		timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		for {
			after, err := openRestyWorkers(timeout)
			if err != nil {
				return err
			}
			fresh, oldAccepting := false, false
			for pid := range after {
				fresh = fresh || !before[pid]
				oldAccepting = oldAccepting || before[pid]
			}
			if fresh && !oldAccepting {
				return nil
			}
			select {
			case <-timeout.Done():
				return errors.New("OpenResty 未完成套接字配置重载")
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	for path, content := range files {
		if err = writeHostConfig(path, bytes.ReplaceAll(content, []byte("unix:"+from), []byte("unix:"+to))); err != nil {
			break
		}
	}
	if err == nil {
		err = apply(ctx, true)
	}
	if err != nil {
		for path, content := range files {
			err = errors.Join(err, writeHostConfig(path, content))
		}
		return errors.Join(err, apply(context.WithoutCancel(ctx), false))
	}
	return nil
}
