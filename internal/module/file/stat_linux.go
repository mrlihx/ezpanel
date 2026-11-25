package file

import (
	"os"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func statMetadata(info os.FileInfo) map[string]any {
	s := info.Sys().(*syscall.Stat_t)
	return map[string]any{
		"accessTime": time.Unix(s.Atim.Sec, s.Atim.Nsec),
		"changeTime": time.Unix(s.Ctim.Sec, s.Ctim.Nsec),
		"uid":        s.Uid, "gid": s.Gid, "links": s.Nlink,
		"inode": strconv.FormatUint(s.Ino, 10), "inodeMode": s.Mode, "device": strconv.FormatUint(uint64(s.Dev), 10),
	}
}

func renameTransfer(src, dst string, overwrite bool) error {
	if overwrite {
		return os.Rename(src, dst)
	}
	return unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
}
