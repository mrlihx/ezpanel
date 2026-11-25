//go:build linux || darwin

package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// 从文件直至根目录检查所有权，避免插件替换父目录中的可执行文件。
func protectedExecutable(p string) error {
	for {
		info, e := os.Stat(p)
		if e != nil {
			return e
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return errors.New("命令及父目录必须由 root 持有且禁止组/其他用户写入")
		}
		if p == "/" {
			return nil
		}
		p = filepath.Dir(p)
	}
}
