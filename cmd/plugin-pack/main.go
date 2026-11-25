package main

import (
	"archive/zip"
	"errors"
	"ezpanel/internal/plugin"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "用法：pack-plugin.sh <插件包目录>")
		os.Exit(1)
	}
	output, e := pack(os.Args[1])
	if e != nil {
		fmt.Fprintln(os.Stderr, "打包失败：", e)
		os.Exit(1)
	}
	fmt.Println(output)
}
func pack(dir string) (result string, err error) {
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	f, e := os.Open(filepath.Join(dir, "plugin.yaml"))
	if e != nil {
		return "", e
	}
	manifest, e := plugin.ReadManifest(f, dir, dir, false, plugin.CoreVersion)
	f.Close()
	if e != nil {
		return "", e
	}
	files := map[string]bool{"plugin.yaml": true}
	for _, entry := range manifest.Entry {
		files[filepath.FromSlash(entry)] = true
	}
	if _, e = os.Lstat(filepath.Join(dir, "README.md")); e == nil {
		files["README.md"] = true
	} else if !os.IsNotExist(e) {
		return "", e
	}
	e = filepath.WalkDir(filepath.Join(dir, "web"), func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("web 不允许符号链接")
		}
		if !d.IsDir() {
			name, e := filepath.Rel(dir, p)
			if e != nil {
				return e
			}
			files[name] = true
		}
		return nil
	})
	if e != nil {
		return "", e
	}
	if len(files) > 5000 {
		return "", plugin.ErrLimit
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	result = filepath.Join(filepath.Dir(dir), manifest.ID+"-"+manifest.Version+".zip")
	out, e := os.OpenFile(result, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	defer func() {
		out.Close()
		if err != nil {
			os.Remove(result)
		}
	}()
	z := zip.NewWriter(out)
	var total int64
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, e := os.Lstat(path)
		if e != nil {
			return result, e
		}
		if !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
			return result, errors.New("声明路径必须为无特殊权限的普通文件")
		}
		if info.Size() > plugin.MaxUpload {
			return result, plugin.ErrLimit
		}

		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return result, e
		}
		header.Name = filepath.ToSlash(name)
		header.Method = zip.Deflate
		writer, e := z.CreateHeader(header)
		if e != nil {
			return result, e
		}
		in, e := os.Open(path)
		if e != nil {
			return result, e
		}
		n, e := io.Copy(writer, io.LimitReader(in, plugin.MaxUpload+1))
		in.Close()
		if e != nil {
			return result, e
		}
		total += n
		if n > plugin.MaxUpload || total > plugin.MaxExpanded {
			return result, plugin.ErrLimit
		}
	}
	if e = z.Close(); e != nil {
		return result, e
	}
	info, e := out.Stat()
	if e != nil {
		return result, e
	}
	if info.Size() > plugin.MaxUpload {
		return result, plugin.ErrLimit
	}
	return result, nil
}
