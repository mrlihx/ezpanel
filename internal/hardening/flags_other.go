//go:build !linux

package hardening

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func identity(fi os.FileInfo) (uint32, uint32, string) {
	s, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, ""
	}
	return s.Uid, s.Gid, fmt.Sprintf("%d:%d", s.Dev, s.Ino)
}
func fileFlags(string, *uint32) (uint32, string, error) {
	return 0, "", errors.New("文件属性保护仅支持 Linux")
}
