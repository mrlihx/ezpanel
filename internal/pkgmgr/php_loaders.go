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
	"time"
)

func (m *Manager) installPHPLoader(ctx context.Context, id, name string, record sourceInstall, log func(string)) error {
	arch := map[string]string{"amd64": "x86-64", "arm64": "aarch64"}[runtime.GOARCH]
	if runtime.GOOS != "linux" || arch == "" {
		return errors.New("厂商加载器仅支持Linux amd64/arm64")
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
	log(fmt.Sprintf("加载器归档SHA256：%x", sha256.Sum256(data)))
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
		return fmt.Errorf("厂商包没有适配PHP %s的加载器，请选择厂商支持的PHP版本", phpBranch(id))
	}
	dir := filepath.Join(m.phpDirectory(id), "loaders")
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	if err = os.Chmod(dir, 0755); err != nil {
		return err
	}
	target := filepath.Join(dir, filename)
	if err = os.WriteFile(target, loader, 0644); err != nil {
		return err
	}
	if err = os.Chmod(target, 0644); err != nil {
		return err
	}
	result, err := m.Run(ctx, 15*time.Second, nil, record.Prefix+"/bin/php", "-n", "-d", directive+"="+target, "-r", fmt.Sprintf(`exit(extension_loaded(%q)?0:1);`, moduleName))
	if err != nil {
		return fmt.Errorf("加载器检查失败，未启用：%s %s", result.Stdout, result.Stderr)
	}
	ini := m.phpExtensionINI(id, name)
	if err = writeInitial(ini, directive+"="+target+"\n", 0644); err != nil {
		return err
	}
	log("厂商加载器已安装；请重载PHP服务后查看phpinfo中的实际版本")
	return nil
}
