package logview

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

type Text struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated"`
	Clearable bool   `json:"clearable"`
}

// 调用方只传服务器已确定的日志路径；禁止符号链接和设备文件。
func Open(path string, write bool) (*os.File, error) {
	flags := os.O_RDONLY
	if write {
		flags = os.O_WRONLY
	}
	f, err := os.OpenFile(path, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("日志不是普通文件")
	}
	return f, nil
}

func Read(path string) (Text, error) {
	out := Text{Path: path, Clearable: true}
	f, err := Open(path, false)
	if os.IsNotExist(err) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return out, err
	}
	out.Size = info.Size()
	const limit = 1 << 20
	start := max(int64(0), out.Size-limit)
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return out, err
	}
	data, err := io.ReadAll(io.LimitReader(f, limit))
	if err != nil {
		return out, err
	}
	out.Truncated = start > 0
	if start > 0 {
		if pos := bytes.IndexByte(data, '\n'); pos >= 0 {
			data = data[pos+1:]
		}
	}
	out.Content = strings.ToValidUTF8(string(data), "�")
	return out, nil
}

func Clear(path string) error {
	f, err := Open(path, true)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Truncate(0)
}
