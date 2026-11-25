package pkgmgr

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func dockerPHPExtensionINI(name string) string {
	order := "50"
	if name == "ioncube" {
		order = "00"
	}
	return "/usr/local/etc/php/conf.d/" + order + "-panel-" + name + ".ini"
}

func (m *Manager) changeDockerPHPExtension(ctx context.Context, id, requested, name, source, version, action string, log func(string)) error {
	containerName := "ezpanel-" + id
	info, exists, err := m.inspectContainer(ctx, containerName)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("当前 PHP 容器尚未安装")
	}
	if !info.State.Running {
		return errors.New("请先启动当前 PHP 容器后再管理扩展")
	}
	rows, err := m.PHPExtensions(ctx, id)
	if err != nil {
		return err
	}
	loaded := false
	for _, r := range rows {
		if PHPExtensionModule(r.Name) == name {
			loaded = true
			if action == "uninstall" && (r.Builtin || !r.Managed) {
				return errors.New("该扩展为内置或由其他配置加载，请在配置文件中管理")
			}
		}
	}
	iniPath := dockerPHPExtensionINI(name)
	hostINI := m.phpExtensionINI(id, name)
	if action == "uninstall" {
		log("正在从容器 " + containerName + " 移除扩展配置：" + name)
		if _, err := m.docker(ctx, 20*time.Second, "exec", containerName, "rm", "-f", iniPath); err != nil {
			return fmt.Errorf("移除容器内扩展配置失败：%w", err)
		}
		if err := os.Remove(hostINI); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("移除宿主机扩展标记失败：%w", err)
		}
		if err := m.applyPHPConfig(ctx, id, true); err != nil {
			log("移除扩展配置成功，重载 PHP 提示：" + err.Error())
		} else {
			log("已移除扩展加载配置并自动重载 PHP 服务")
		}
		return nil
	}
	if loaded {
		return errors.New("当前 PHP 已加载该扩展")
	}
	if name == "phalcon" {
		psr := false
		for _, row := range rows {
			psr = psr || row.Module == "psr"
		}
		if !psr {
			log("先安装 Phalcon 依赖扩展 psr")
			if err = m.changeDockerPHPExtension(ctx, id, "psr", "psr", "pecl", "", "install", log); err != nil {
				return err
			}
		}
	}
	if source == "loader" {
		return m.installDockerPHPLoader(ctx, id, containerName, name, log)
	}
	phpVer := info.phpVersion()
	dockerPath, err := m.LookPath("docker")
	if err != nil {
		return err
	}
	aptDeps := phpExtensionDependencies(name, true)
	// 部分扩展在 Debian PHP 镜像下需要额外运行/构建依赖
	if name == "gd" {
		aptDeps = append(aptDeps, "libfreetype6-dev", "libjpeg62-turbo-dev", "libpng-dev", "libwebp-dev")
	}
	if len(aptDeps) > 0 {
		log("正在容器内安装编译依赖：" + strings.Join(aptDeps, " "))
		installDepsCmd := "export DEBIAN_FRONTEND=noninteractive && apt-get update && apt-get install -y --no-install-recommends " + strings.Join(aptDeps, " ")
		if _, err = m.Stream(ctx, 20*time.Minute, nil, log, dockerPath, "exec", containerName, "sh", "-c", installDepsCmd); err != nil {
			return fmt.Errorf("安装扩展编译依赖失败：%w", err)
		}
	}

	directive := "extension"
	if name == "xdebug" || name == "opcache" {
		directive = "zend_extension"
	}
	if source == "php" {
		log("正在容器内编译安装 PHP 官方扩展：" + name)
		var cmdParts []string
		if name == "imap" {
			cmdParts = append(cmdParts, "docker-php-ext-configure imap --with-kerberos --with-imap-ssl")
		} else if name == "ldap" {
			cmdParts = append(cmdParts, "docker-php-ext-configure ldap --with-libdir=lib/$(dpkg-architecture -qDEB_HOST_MULTIARCH)/")
		}
		cmdParts = append(cmdParts, "docker-php-ext-install -j2 "+name)
		if _, err = m.Stream(ctx, 30*time.Minute, nil, log, dockerPath, "exec", containerName, "sh", "-c", strings.Join(cmdParts, " && ")); err != nil {
			return fmt.Errorf("编译安装扩展 %s 失败：%w", name, err)
		}
		// 统一移交给面板管理的 ini 文件，方便后续识别与卸载
		_, _ = m.docker(ctx, 15*time.Second, "exec", containerName, "rm", "-f", "/usr/local/etc/php/conf.d/docker-php-ext-"+name+".ini")
	} else {
		if version == "" {
			if resolved, e := m.peclVersion(ctx, name, "", phpVer, strings.EqualFold(requested, "swoole4")); e == nil && resolved != "" {
				version = resolved
			}
		}
		pkgSpec := name
		if version != "" {
			pkgSpec = name + "-" + version
		}
		log("正在容器内通过 PECL 编译安装扩展：" + pkgSpec)
		peclCmd := fmt.Sprintf("yes '' | pecl install --force %s", pkgSpec)
		if _, err = m.Stream(ctx, 30*time.Minute, nil, log, dockerPath, "exec", containerName, "sh", "-c", peclCmd); err != nil {
			return fmt.Errorf("PECL 安装扩展 %s 失败：%w", pkgSpec, err)
		}
	}

	moduleName := name
	if name == "opcache" {
		moduleName = "Zend OPcache"
	}
	log("正在校验扩展加载状态：" + name)
	check, err := m.docker(ctx, 20*time.Second, "exec", containerName, "php", "-c", m.phpDirectory(id)+"/php.ini", "-d", directive+"="+name+".so", "-r", fmt.Sprintf(`exit(extension_loaded(%q)?0:1);`, moduleName))
	if err != nil {
		return fmt.Errorf("扩展加载检查失败：%s %s", strings.TrimSpace(check.Stdout), strings.TrimSpace(check.Stderr))
	}
	iniValue := directive + "=" + name + ".so"
	writeCmd := fmt.Sprintf("printf '%%s\\n' %q > %s && chmod 644 %s", iniValue, iniPath, iniPath)
	if _, err = m.docker(ctx, 15*time.Second, "exec", containerName, "sh", "-c", writeCmd); err != nil {
		return fmt.Errorf("写入容器扩展配置失败：%w", err)
	}
	// 同时在宿主机 conf.d 目录留存标记，使 PHPExtensions() 识别 Managed=true
	if err = os.MkdirAll(filepath.Dir(hostINI), 0755); err == nil {
		_ = os.WriteFile(hostINI, []byte(iniValue+"\n"), 0644)
	}
	if err := m.applyPHPConfig(ctx, id, true); err != nil {
		log("扩展已安装，但重载 PHP 失败，请手动重启或重载 PHP：" + err.Error())
	} else {
		log("扩展 " + name + " 已安装并自动重载 PHP 服务")
	}
	return nil
}

func (m *Manager) installDockerPHPLoader(ctx context.Context, id, containerName, name string, log func(string)) error {
	arch := map[string]string{"amd64": "x86-64", "arm64": "aarch64"}[runtime.GOARCH]
	if runtime.GOOS != "linux" || arch == "" {
		return errors.New("厂商加载器仅支持 Linux amd64/arm64")
	}
	address := "https://downloads.ioncube.com/loader_downloads/ioncube_loaders_lin_" + arch + ".tar.gz"
	filename := "ioncube_loader_lin_" + phpBranch(id) + ".so"
	directive, moduleName := "zend_extension", "ionCube Loader"
	if name == "sourceguardian" {
		if arch == "x86-64" {
			arch = "x86_64"
		}
		address = "https://www.sourceguardian.com/loaders/download/loaders.linux-" + arch + ".tar.gz"
		filename = "ixed." + phpBranch(id) + ".lin"
		directive, moduleName = "extension", "SourceGuardian"
	}
	log("从厂商官方下载加载器：" + address)
	data, err := m.sourceRequest(ctx, address, 128<<20, 5*time.Minute)
	if err != nil {
		return err
	}
	log(fmt.Sprintf("加载器归档 SHA256：%x", sha256.Sum256(data)))
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer reader.Close()
	archive := tar.NewReader(reader)
	var loader []byte
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		header, e := archive.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if filepath.Base(header.Name) != filename || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size < 1 || header.Size > 32<<20 {
			return errors.New("加载器文件大小无效")
		}
		loader, err = io.ReadAll(io.LimitReader(archive, (32<<20)+1))
		if err != nil {
			return err
		}
		break
	}
	if len(loader) == 0 {
		return fmt.Errorf("厂商包没有适配 PHP %s 的加载器，请选择厂商支持的 PHP 版本", phpBranch(id))
	}
	dir := filepath.Join(m.phpDirectory(id), "loaders")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	target := filepath.Join(dir, filename)
	if err = os.WriteFile(target, loader, 0644); err != nil {
		return err
	}
	result, err := m.docker(ctx, 20*time.Second, "exec", containerName, "php", "-n", "-d", directive+"="+target, "-r", fmt.Sprintf(`exit(extension_loaded(%q)?0:1);`, moduleName))
	if err != nil {
		return fmt.Errorf("加载器检查失败，未启用：%s %s", strings.TrimSpace(result.Stdout), strings.TrimSpace(result.Stderr))
	}
	iniValue := directive + "=" + target
	iniPath := dockerPHPExtensionINI(name)
	writeCmd := fmt.Sprintf("printf '%%s\\n' %q > %s && chmod 644 %s", iniValue, iniPath, iniPath)
	if _, err = m.docker(ctx, 15*time.Second, "exec", containerName, "sh", "-c", writeCmd); err != nil {
		return fmt.Errorf("写入容器加载器配置失败：%w", err)
	}
	hostINI := m.phpExtensionINI(id, name)
	if err = os.MkdirAll(filepath.Dir(hostINI), 0755); err == nil {
		_ = os.WriteFile(hostINI, []byte(iniValue+"\n"), 0644)
	}
	if err := m.applyPHPConfig(ctx, id, true); err != nil {
		log("厂商加载器已安装，请手动重载 PHP 服务：" + err.Error())
	} else {
		log("厂商加载器已安装并自动重载 PHP 服务")
	}
	return nil
}
