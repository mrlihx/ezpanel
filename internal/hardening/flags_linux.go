//go:build linux

package hardening

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func identity(fi os.FileInfo) (uint32, uint32, string) {
	s := fi.Sys().(*syscall.Stat_t)
	return s.Uid, s.Gid, fmt.Sprintf("%d:%d", s.Dev, s.Ino)
}
func fileFlags(path string, set *uint32) (uint32, string, error) {
	f, e := os.OpenFile(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return 0, "", e
	}
	defer f.Close()
	fi, e := f.Stat()
	if e != nil {
		return 0, "", e
	}
	if !fi.Mode().IsRegular() {
		return 0, "", errors.New("文件属性保护仅支持普通文件")
	}
	_, _, id := identity(fi)
	const (
		fsIOCGetFlags = 0x80006601 | (uintptr(unsafe.Sizeof(uintptr(0))) << 16)
		fsIOCSetFlags = 0x40006602 | (uintptr(unsafe.Sizeof(uintptr(0))) << 16)
	)
	var flags uintptr
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIOCGetFlags, uintptr(unsafe.Pointer(&flags)))
	if errno != 0 {
		return 0, id, errno
	}
	if set != nil {
		flags = uintptr(*set)
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIOCSetFlags, uintptr(unsafe.Pointer(&flags)))
		if errno != 0 {
			return 0, id, errno
		}
		var actual uintptr
		_, _, errno = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIOCGetFlags, uintptr(unsafe.Pointer(&actual)))
		if errno != 0 {
			return 0, id, errno
		}
		if uint32(actual) != uint32(flags) {
			return uint32(actual), id, fmt.Errorf("文件属性写入后校验不一致")
		}
	}
	return uint32(flags), id, nil
}
