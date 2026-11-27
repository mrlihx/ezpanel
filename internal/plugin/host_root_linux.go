package plugin

import (
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// 打开授权目录时也逐级拒绝软链接，避免配置校验后目录被替换。
func openAuthorizedRoot(path string) (*os.Root, error) {
	const flags = unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_DIRECTORY | unix.O_PATH
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		return nil, err
	}
	for component := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, component, flags, 0)
		_ = unix.Close(fd)
		if err != nil {
			return nil, err
		}
		fd = next
	}
	defer unix.Close(fd)
	return os.OpenRoot("/proc/self/fd/" + strconv.Itoa(fd))
}
