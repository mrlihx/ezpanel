//go:build !linux

package file

import "os"

// 正式运行环境为Linux；其他平台仅供构建检查。
func statMetadata(_ os.FileInfo) map[string]any { return map[string]any{} }

func renameTransfer(src, dst string, overwrite bool) error {
	if !overwrite {
		if _, err := os.Lstat(dst); err == nil {
			return os.ErrExist
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return os.Rename(src, dst)
}
