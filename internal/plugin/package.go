package plugin

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const MaxUpload int64 = 200 << 20
const MaxExpanded int64 = 500 << 20

var ErrLimit = errors.New("插件包超过限额")

// 解压只创建普通文件；实际读取时计数，不信任归档声明的长度。
func unpack(zipPath, dir string) error {
	z, e := zip.OpenReader(zipPath)
	if e != nil {
		return errors.New("无法读取 ZIP")
	}
	defer z.Close()
	if len(z.File) > 5000 {
		return ErrLimit
	}
	seen := map[string]bool{}
	var total int64
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !packagePath(name) || seen[name] || f.Mode()&(os.ModeSymlink|os.ModeDevice|os.ModeNamedPipe|os.ModeSocket|os.ModeSetuid|os.ModeSetgid) != 0 {
			return errors.New("ZIP 包含非法类型、重复或越界路径")
		}
		seen[name] = true
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if f.FileInfo().IsDir() {
			if e = os.MkdirAll(dst, 0755); e != nil {
				return e
			}
			continue
		}
		if !f.Mode().IsRegular() {
			return errors.New("ZIP 只允许目录和普通文件")
		}
		if e = os.MkdirAll(filepath.Dir(dst), 0755); e != nil {
			return e
		}
		out, e := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			out.Close()
			return e
		}
		n, copyErr := io.Copy(out, io.LimitReader(in, min(MaxUpload, MaxExpanded-total)+1))
		in.Close()
		closeErr := out.Close()
		total += n
		if n > MaxUpload || total > MaxExpanded {
			return ErrLimit
		}
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	// systemd 使用 UMask=0077；安装包仍须显式设置声明的可读目录/普通文件模式。
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		mode := os.FileMode(0644)
		if d.IsDir() {
			mode = 0755
		}
		return os.Chmod(p, mode)
	})
}
