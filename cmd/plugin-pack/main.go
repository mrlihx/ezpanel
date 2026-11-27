package main

import (
	"archive/zip"
	"errors"
	"ezpanel/internal/plugin"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	force := flag.Bool("f", false, "覆盖已存在的输出 ZIP")
	flag.BoolVar(force, "force", false, "覆盖已存在的输出 ZIP")
	outputPath := flag.String("o", "", "指定输出 ZIP 路径")
	dev := flag.Bool("dev", false, "开发模式（允许仅包含当前平台架构入口）")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：pack-plugin.sh [-f|--force] [-o <输出路径>] [--dev] <插件包目录>")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	output, count, size, e := pack(flag.Arg(0), *outputPath, *force, *dev)
	if e != nil {
		fmt.Fprintln(os.Stderr, "打包失败：", e)
		os.Exit(1)
	}
	fmt.Printf("%s (%d 个文件, %d 字节)\n", output, count, size)
}
func pack(dir, customOut string, force, dev bool) (string, int, int64, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", 0, 0, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", 0, 0, err
	}
	f, e := os.Open(filepath.Join(dir, "plugin.yaml"))
	if e != nil {
		return "", 0, 0, e
	}
	manifest, e := plugin.ReadManifest(f, dir, "", dev, plugin.CoreVersion)
	f.Close()
	if e != nil {
		return "", 0, 0, e
	}
	result := customOut
	if result == "" {
		result = filepath.Join(filepath.Dir(dir), manifest.ID+"-"+manifest.Version+".zip")
	}
	result, e = filepath.Abs(result)
	if e != nil {
		return "", 0, 0, e
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(result))
	if e != nil {
		return "", 0, 0, e
	}
	result = filepath.Join(parent, filepath.Base(result))
	files := map[string]bool{"plugin.yaml": true}
	for _, entry := range manifest.Entry {
		files[filepath.FromSlash(entry)] = true
	}
	if _, e = os.Lstat(filepath.Join(dir, "README.md")); e == nil {
		files["README.md"] = true
	} else if !os.IsNotExist(e) {
		return "", 0, 0, e
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
		if !d.IsDir() && p != result && filepath.Base(p) != ".DS_Store" {
			name, e := filepath.Rel(dir, p)
			if e != nil {
				return e
			}
			files[name] = true
		}
		return nil
	})
	if e != nil {
		return "", 0, 0, e
	}
	if name, err := filepath.Rel(dir, result); err == nil && (files[name] || result == filepath.Join(dir, manifest.UI)) {
		return "", 0, 0, errors.New("输出路径与插件输入文件冲突")
	}
	if len(files) > 5000 {
		return "", 0, 0, plugin.ErrLimit
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	out, e := os.CreateTemp(filepath.Dir(result), ".ezpanel-plugin-*.zip")
	if e != nil {
		return "", 0, 0, e
	}
	defer os.Remove(out.Name())
	defer out.Close()
	z := zip.NewWriter(out)
	var total int64
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, e := os.Lstat(path)
		if e != nil {
			return result, 0, 0, e
		}
		if !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
			return result, 0, 0, errors.New("声明路径必须为无特殊权限的普通文件")
		}
		if info.Size() > plugin.MaxUpload {
			return result, 0, 0, plugin.ErrLimit
		}

		header, e := zip.FileInfoHeader(info)
		if e != nil {
			return result, 0, 0, e
		}
		header.Name = filepath.ToSlash(name)
		header.Method = zip.Deflate
		writer, e := z.CreateHeader(header)
		if e != nil {
			return result, 0, 0, e
		}
		in, e := os.Open(path)
		if e != nil {
			return result, 0, 0, e
		}
		n, e := io.Copy(writer, io.LimitReader(in, plugin.MaxUpload+1))
		in.Close()
		if e != nil {
			return result, 0, 0, e
		}
		total += n
		if n > plugin.MaxUpload || total > plugin.MaxExpanded {
			return result, 0, 0, plugin.ErrLimit
		}
	}
	if e = z.Close(); e != nil {
		return result, 0, 0, e
	}
	info, e := out.Stat()
	if e != nil {
		return result, 0, 0, e
	}
	if info.Size() > plugin.MaxUpload {
		return result, 0, 0, plugin.ErrLimit
	}
	if e = out.Close(); e != nil {
		return result, 0, 0, e
	}
	if force {
		e = os.Rename(out.Name(), result)
	} else {
		e = os.Link(out.Name(), result)
	}
	if e != nil {
		return result, 0, 0, e
	}
	return result, len(names), info.Size(), nil
}
