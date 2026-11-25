package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"syscall"
)

func (m *Manager) PHPMyAdminRoot() string { return filepath.Join(m.layout().PHPMyAdminDir(), "current") }

const phpMyAdminGroup = 65534 // PHP 容器中的 nogroup

func (m *Manager) allowPHPMyAdminMySQLSocket() error {
	dir := filepath.Dir(m.MySQLSocket())
	// MySQL 保留目录所有权，phpMyAdmin 只需穿过目录访问套接字。
	if err := os.Chown(dir, -1, phpMyAdminGroup); err != nil {
		return err
	}
	return os.Chmod(dir, 0710)
}

// 面板的 umask 会屏蔽组读权限，发布后显式授权给 phpMyAdmin 的专用 FPM 组。
func setPHPMyAdminConfigPermissions(dir string) error {
	for _, name := range []string{"config.inc.php", "ezpanel-signon.php"} {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("phpMyAdmin 配置必须是普通文件")
		}
		if stat := info.Sys().(*syscall.Stat_t); stat.Uid != 0 || stat.Gid != phpMyAdminGroup {
			if err := os.Chown(path, 0, phpMyAdminGroup); err != nil {
				return err
			}
		}
		if info.Mode().Perm() != 0440 {
			if err := os.Chmod(path, 0440); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) PreparePHPMyAdmin(localMySQL bool) error {
	return m.ApplicationChange("phpmyadmin", func() error {
		if err := setPHPMyAdminConfigPermissions(m.PHPMyAdminRoot()); err != nil {
			return fmt.Errorf("phpMyAdmin 配置文件权限修复失败：%w", err)
		}
		if localMySQL {
			if err := m.allowPHPMyAdminMySQLSocket(); err != nil {
				return fmt.Errorf("MySQL 套接字目录权限修复失败：%w", err)
			}
		}
		return nil
	})
}

func phpMyAdminReleases(data []byte) ([]Release, error) {
	var index struct {
		Version  string `json:"version"`
		Releases []struct {
			Version string `json:"version"`
		} `json:"releases"`
	}
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, err
	}
	versions := []string{index.Version}
	for _, release := range index.Releases {
		versions = append(versions, release.Version)
	}
	rows := []Release{}
	seen := map[string]bool{}
	pattern := regexp.MustCompile(`^5\.([0-9]+)\.[0-9]+$`)
	for _, version := range versions {
		match := pattern.FindStringSubmatch(version)
		if match == nil || seen[version] {
			continue
		}
		minor, err := strconv.Atoi(match[1])
		if err != nil || minor < 2 {
			continue
		}
		seen[version] = true
		rows = append(rows, Release{Version: version, Source: "phpMyAdmin官方源", Method: "files", URL: "https://files.phpmyadmin.net/phpMyAdmin/" + version + "/phpMyAdmin-" + version + "-all-languages.tar.gz"})
	}
	if len(rows) == 0 {
		return nil, errors.New("phpMyAdmin官方目录没有5.2及以上的5.x正式版本")
	}
	sort.Slice(rows, func(i, j int) bool { return newer(rows[i].Version, rows[j].Version) })
	return rows, nil
}

func (m *Manager) PHPMyAdminRuntime(ctx context.Context) (string, error) {
	if selected := m.cfg.Current().PHPMyAdminPHPVersion; selected != "" {
		if socket, ok := m.phpMyAdminSocket(ctx, selected); ok {
			return socket, nil
		}
		return "", errors.New("所选 PHP " + selected + " 未运行")
	}
	for _, branch := range phpMyAdminPHPBranches {
		if socket, ok := m.phpMyAdminSocket(ctx, branch); ok {
			return socket, nil
		}
	}
	return "", errors.New("请先在应用商店安装并运行兼容的 PHP")
}

var phpMyAdminPHPBranches = []string{"8.5", "8.4", "8.3", "8.2", "8.1", "8.0", "7.4"}

func (m *Manager) PHPMyAdminPHPVersions(ctx context.Context) ([]string, error) {
	versions := []string{}
	for _, branch := range phpMyAdminPHPBranches {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := m.phpMyAdminSocket(ctx, branch); ok {
			versions = append(versions, branch)
		}
	}
	return versions, nil
}

func (m *Manager) phpMyAdminSocket(ctx context.Context, branch string) (string, bool) {
	socket := m.pmaSocket(branch)
	info, err := os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return "", false
	}
	state, _ := m.Status(ctx, "php-"+branch)
	return socket, state.Running != nil && *state.Running
}

func (m *Manager) publishPHPMyAdmin(ctx context.Context, release Release, src, prefix, checksum string, choice SourceSelection, previous sourceInstall, run func(string, string, ...string) error, log func(string)) (State, error) {
	const id = "phpmyadmin"
	root := m.installRoot(id)
	if err := os.MkdirAll(filepath.Dir(prefix), 0755); err != nil {
		return State{}, err
	}
	for _, dir := range []string{filepath.Dir(root), root, filepath.Dir(prefix)} {
		if err := os.Chmod(dir, 0755); err != nil {
			return State{}, err
		}
	}
	if err := os.RemoveAll(prefix); err != nil {
		return State{}, err
	}
	published := false
	defer func() {
		if !published {
			os.RemoveAll(prefix)
		}
	}()
	if err := run(src, "cp", "-a", src, prefix); err != nil {
		return State{}, err
	}
	if err := filepath.WalkDir(prefix, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.Type()&os.ModeSymlink != 0 {
			return err
		}
		mode := os.FileMode(0644)
		if entry.IsDir() {
			mode = 0755
		}
		return os.Chmod(path, mode)
	}); err != nil {
		return State{}, err
	}
	for name, content := range map[string]string{"config.inc.php": PHPMyAdminConfig, "ezpanel-signon.php": PHPMyAdminSignon} {
		path := filepath.Join(prefix, name)
		if err := os.WriteFile(path, []byte(content), 0440); err != nil {
			return State{}, err
		}
	}
	if err := setPHPMyAdminConfigPermissions(prefix); err != nil {
		return State{}, err
	}
	if info, err := os.Stat(filepath.Join(prefix, "index.php")); err != nil || !info.Mode().IsRegular() {
		return State{}, errors.New("phpMyAdmin官方归档缺少index.php")
	}
	if err := m.selectRelease(id, prefix); err != nil {
		return State{}, err
	}
	record := sourceInstall{ID: id, Version: release.Version, DownloadSource: choice, Prefix: prefix, URL: release.URL, SHA256: checksum}
	if err := m.writeRecord(record); err != nil {
		var rollbackErr error
		if previous.Prefix != "" {
			rollbackErr = m.selectRelease(id, previous.Prefix)
		} else {
			rollbackErr = os.Remove(filepath.Join(root, "current"))
		}
		if rollbackErr != nil {
			published = true // 保留current仍在引用的文件，供管理员修复来源记录。
			return State{}, errors.New("phpMyAdmin来源记录保存失败且版本入口回退失败，请检查安装目录")
		}
		return State{}, errors.New("phpMyAdmin来源记录保存失败，版本入口已回退")
	}
	published = true
	log("phpMyAdmin官方文件已发布，仅供面板私有网关访问；未创建网站、监听端口或服务")
	return m.Status(ctx, id)
}
