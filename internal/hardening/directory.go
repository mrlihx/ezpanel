package hardening

import (
	"fmt"
	"time"
)

type DirectoryPolicy struct {
	ID         string
	Path       string
	Exclusions []string
}
type DirectoryBackend interface {
	Supported() (bool, string)
	Sync([]DirectoryPolicy) error
}

func (m *Manager) SetDirectoryBackend(backend DirectoryBackend) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.directory = backend
	m.state.DirectorySupported, m.state.DirectoryNotice = backend.Supported()
	return m.syncDirectories()
}
func (m *Manager) syncDirectories() error {
	policies := []DirectoryPolicy{}
	if m.state.Enabled {
		for _, rule := range m.state.Rules {
			if !rule.Directory || rule.Mode != "immutable" || !rule.Enabled || rule.PausedUntil != nil && time.Now().Before(*rule.PausedUntil) {
				continue
			}
			policies = append(policies, DirectoryPolicy{ID: rule.ID, Path: rule.Path, Exclusions: exclusionPatterns(rule.Exclusions)})
		}
	}
	if err := m.directory.Sync(policies); err != nil {
		return fmt.Errorf("目录策略同步失败：%w", err)
	}
	return nil
}
