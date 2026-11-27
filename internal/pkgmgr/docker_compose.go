package pkgmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

const dockerComposeVersion = "v5.7.1"
const dockerComposePlugin = "/usr/local/lib/docker/cli-plugins/docker-compose"

func (m *Manager) dockerComposeStatus(ctx context.Context, docker string) (string, bool) {
	env := append(executor.DefaultEnv(), "HOME=/root")
	if result, err := m.Run(ctx, 5*time.Second, env, docker, "compose", "version"); err == nil {
		return strings.TrimSpace(result.Stdout), true
	}
	if binary, err := m.LookPath("docker-compose"); err == nil {
		if result, err := m.Run(ctx, 5*time.Second, env, binary, "version"); err == nil {
			return strings.TrimSpace(result.Stdout), true
		}
	}
	return "", false
}

func (m *Manager) ensureDockerCompose(ctx context.Context, docker string, log func(string)) error {
	if current, available := m.dockerComposeStatus(ctx, docker); available {
		log("保留现有 Docker Compose：" + current)
		return nil
	}
	env := append(executor.DefaultEnv(), "HOME=/root")

	// 固定版本和官方发布摘要，启用 GitHub 代理时也校验二进制内容。
	asset, checksum := "", ""
	switch runtime.GOARCH {
	case "amd64":
		asset = "docker-compose-linux-x86_64"
		checksum = "af426722a8af5de812fcee30cc1c39f0639f5f723cbadefb25e5d4db054064cf"
	case "arm64":
		asset = "docker-compose-linux-aarch64"
		checksum = "f2f49f903240895fa764c0dca3a14548b754ad9426f6b609ccaf55ec5a3aa2a5"
	}
	if runtime.GOOS != "linux" || asset == "" {
		return errors.New("自动安装 Docker Compose 仅支持 Linux amd64/arm64")
	}
	if _, err := os.Lstat(dockerComposePlugin); err == nil {
		return fmt.Errorf("已有 Docker Compose 插件无法运行，请检查 %s", dockerComposePlugin)
	} else if !os.IsNotExist(err) {
		return err
	}
	log("下载 Docker Compose " + dockerComposeVersion)
	address := "https://github.com/docker/compose/releases/download/" + dockerComposeVersion + "/" + asset
	data, err := m.sourceRequest(ctx, address, 64<<20, 5*time.Minute)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != checksum {
		return errors.New("Docker Compose 官方二进制 SHA256 校验失败")
	}

	dir := filepath.Dir(dockerComposePlugin)
	// 面板 umask 为 0077，新建的系统插件目录须允许 Docker 用户遍历。
	for _, directory := range []string{"/usr/local/lib", filepath.Dir(dir), dir} {
		if err = os.Mkdir(directory, 0755); os.IsExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if err = os.Chmod(directory, 0755); err != nil {
			return err
		}
	}
	file, err := os.CreateTemp(dir, ".compose-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	_, err = file.Write(data)
	if err == nil {
		err = file.Chmod(0755)
	}
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	// 发布完整文件，保留其他安装工具写入的插件。
	if err = os.Link(file.Name(), dockerComposePlugin); err != nil {
		return err
	}
	result, err := m.Run(ctx, 5*time.Second, env, docker, "compose", "version")
	if err != nil {
		return errors.Join(fmt.Errorf("Docker Compose 验证失败：%w", err), os.Remove(dockerComposePlugin))
	}
	log("Docker Compose 已安装：" + strings.TrimSpace(result.Stdout))
	return nil
}
