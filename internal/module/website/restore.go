package website

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"ezpanel/internal/store"
)

// RestoreSiteFiles 使用同一文件系统的临时目录，完整解包后才交换站点目录。
// 调用方持有网站任务锁，站点必须停止并由面板管理目录。
func (m *Module) RestoreSiteFiles(ctx context.Context, id, confirm string, input io.Reader, archiveSize int64, extract func(context.Context, string, string, string) error) (string, error) {
	var row store.Website
	if err := m.db.WithContext(ctx).Take(&row, "id = ?", id).Error; err != nil {
		return "", err
	}
	if row.Enabled || row.Name != confirm {
		return "", errors.New("请先停用站点并精确确认站点名称")
	}
	if allowed, why := m.canDelete(row); !allowed {
		return "", errors.New(why)
	}
	stage, err := os.MkdirTemp(filepath.Dir(row.Root), ".ezpanel-restore-")
	if err != nil {
		return "", err
	}
	preserve := false
	defer func() {
		if !preserve {
			_ = os.RemoveAll(stage)
		}
	}()
	archive := filepath.Join(stage, "backup.tar.gz")
	target := filepath.Join(stage, "previous")
	if err := os.Mkdir(target, 0700); err != nil {
		return "", err
	}
	f, err := os.OpenFile(archive, os.O_WRONLY|os.O_EXCL|os.O_CREATE, 0600)
	if err != nil {
		return "", err
	}
	if archiveSize < 1 {
		f.Close()
		return "", errors.New("归档大小无效")
	}
	copied, err := io.Copy(f, io.LimitReader(input, archiveSize+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return "", err
	}
	if copied != archiveSize {
		return "", errors.New("归档大小与登记记录不一致")
	}
	if err := extract(ctx, archive, target, row.Root); err != nil {
		return "", err
	}
	if err := os.Remove(archive); err != nil {
		return "", err
	}
	info, err := os.Stat(row.Root)
	if err != nil {
		return "", err
	}
	owner := info.Sys().(*syscall.Stat_t)
	if os.Geteuid() == 0 {
		if err := filepath.WalkDir(target, func(path string, _ os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			return os.Lchown(path, int(owner.Uid), int(owner.Gid))
		}); err != nil {
			return "", err
		}
	}
	if err := os.Chmod(target, info.Mode().Perm()); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest store.Website
	if err := m.db.Take(&latest, "id = ?", id).Error; err != nil || latest.Enabled || latest.Root != row.Root || latest.Name != confirm {
		return "", errors.New("网站状态发生变化，未替换目录")
	}
	if allowed, why := m.canDelete(latest); !allowed {
		return "", errors.New(why)
	}
	if err := exchangeSiteDirectories(row.Root, target); err != nil {
		return "", err
	}
	preserve = true
	return target, nil
}
