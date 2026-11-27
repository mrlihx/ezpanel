package plugin

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
	"gorm.io/gorm"
)

func (m *Module) historyDir(id string) string {
	return filepath.Join(m.packages(), ".backup", ".versions", id)
}

func (m *Module) historyPath(id string) (string, error) {
	var name string
	if err := m.db.Table("plugin_history").Select("snapshot").Where("plugin_id = ?", id).Take(&name).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return filepath.Join(m.packages(), ".backup", id), nil
	} else if err != nil {
		return "", err
	}
	return filepath.Join(m.historyDir(id), name), nil
}

func (m *Module) pruneHistory(id string) {
	keep, err := m.historyPath(id)
	if err != nil {
		slog.Warn("无法识别插件历史快照", "plugin", id, "error", err)
		return
	}
	legacy := filepath.Join(m.packages(), ".backup", id)
	paths := []string{legacy, filepath.Join(m.packages(), ".backup", ".rollback", id)}
	entries, err := os.ReadDir(m.historyDir(id))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("无法列出插件历史快照", "plugin", id, "error", err)
		return
	}
	for _, entry := range entries {
		paths = append(paths, filepath.Join(m.historyDir(id), entry.Name()))
	}
	for _, name := range paths {
		if name != keep {
			if err := os.RemoveAll(name); err != nil {
				slog.Warn("清理插件历史快照失败", "plugin", id, "error", err)
			}
		}
	}
}

func (m *Module) checkSnapshotSpace(id string) error {
	var needed uint64 = 1 << 20
	data := filepath.Join(m.private(), id)
	for _, root := range []string{filepath.Join(m.packages(), id), data} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if path != root && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return err
			}
			needed += 4096
			if entry.Type().IsRegular() {
				info, err := entry.Info()
				if errors.Is(err, os.ErrNotExist) {
					return nil
				}
				if err != nil {
					return err
				}
				needed += uint64(info.Size())
			}
			return nil
		})
		if err != nil && !(root == data && errors.Is(err, os.ErrNotExist)) {
			return err
		}
	}
	var disk unix.Statfs_t
	if err := unix.Statfs(filepath.Join(m.packages(), ".backup"), &disk); err != nil {
		return err
	}
	if needed > uint64(disk.Bavail)*uint64(disk.Bsize) {
		return errors.New("磁盘空间不足，无法保留历史快照并创建本次快照")
	}
	return nil
}
