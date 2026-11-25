package pkgmgr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type RuntimeEnvironment struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Image   string `json:"image"`
}

func readRuntimeEnvironments(dir string) ([]RuntimeEnvironment, error) {
	data, err := os.ReadFile(filepath.Join(dir, "environments.json"))
	if os.IsNotExist(err) {
		return []RuntimeEnvironment{}, nil
	}
	if err != nil {
		return nil, err
	}
	environments := []RuntimeEnvironment{}
	err = json.Unmarshal(data, &environments)
	return environments, err
}

func writeRuntimeEnvironments(dir string, environments []RuntimeEnvironment) error {
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}
	file, err := os.CreateTemp(dir, ".environments-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = json.NewEncoder(file).Encode(environments)
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(dir, "environments.json"))
}

func (m *Manager) RuntimeEnvironments(id string) ([]RuntimeEnvironment, error) {
	return readRuntimeEnvironments(m.serviceStateDir(id))
}

func (m *Manager) RuntimeEnvironment(id, environmentID string) (RuntimeEnvironment, error) {
	items, err := m.RuntimeEnvironments(id)
	if err != nil {
		return RuntimeEnvironment{}, err
	}
	for _, item := range items {
		if item.ID == environmentID {
			if item.Image == "" {
				item.Image = projectImage(id, item.Version)
			}
			return item, nil
		}
	}
	return RuntimeEnvironment{}, fmt.Errorf("%s 运行环境不存在，请重新选择", Name(id))
}

func (m *Manager) InstallRuntimeEnvironment(ctx context.Context, id, version string, log func(string), selected ...SourceSelection) (RuntimeEnvironment, error) {
	dir := m.serviceStateDir(id)
	item := RuntimeEnvironment{ID: strings.ToLower(rand.Text()), Version: version}
	err := m.ApplicationChange(id, func() error {
		items, err := m.RuntimeEnvironments(id)
		if err != nil {
			return err
		}
		// 同版本环境复用本机镜像，每个项目容器仍有独立的文件系统。
		for _, installed := range items {
			if installed.Version != version || (len(selected) > 0 && selected[0].Mirror != "") {
				continue
			}
			image := installed.Image
			if image == "" {
				image = projectImage(id, version)
			}
			if info, err := m.docker(ctx, 10*time.Second, "image", "inspect", "--format", "{{.Id}}", image); err == nil {
				item.Image = strings.TrimSpace(info.Stdout)
				break
			}
		}
		if item.Image == "" {
			release, err := m.dockerRelease(ctx, id, version, selected...)
			if err != nil {
				return err
			}
			item.Version = release.Version
			reference, err := m.pullContainer(ctx, release.URL, releasePlatform(release), log)
			if err != nil {
				return err
			}
			info, err := m.docker(ctx, 10*time.Second, "image", "inspect", "--format", "{{.Id}}", reference)
			if err != nil {
				return err
			}
			item.Image = strings.TrimSpace(info.Stdout)
		}
		return writeRuntimeEnvironments(dir, append(items, item))
	})
	return item, err
}

func (m *Manager) RemoveRuntimeEnvironment(id, environmentID string) error {
	return m.ApplicationChange(id, func() error {
		items, err := m.RuntimeEnvironments(id)
		if err != nil {
			return err
		}
		index := slices.IndexFunc(items, func(item RuntimeEnvironment) bool { return item.ID == environmentID })
		if index < 0 {
			return errors.New("运行环境不存在")
		}
		return writeRuntimeEnvironments(m.serviceStateDir(id), slices.Delete(items, index, index+1))
	})
}
