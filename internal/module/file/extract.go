package file

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	securezip "github.com/yeka/zip"
	"golang.org/x/text/encoding/simplifiedchinese"
)

var errArchivePassword = errors.New("解压密码缺失或不正确")

type extraction struct {
	Password  string
	Encoding  string
	DirMode   os.FileMode
	UID, GID  int
	Overwrite bool
	Smart     bool
}

func (m *Module) extract(c *gin.Context) {
	var in struct {
		Path      string `json:"path"`
		Dest      string `json:"dest"`
		Smart     bool   `json:"smartFolder"`
		Password  string `json:"password"`
		Encoding  string `json:"encoding"`
		DirMode   string `json:"dirMode"`
		Owner     string `json:"owner"`
		Overwrite bool   `json:"overwrite"`
	}
	if !response.Bind(c, &in) {
		return
	}
	source, err := m.policy.Check(in.Path, false)
	if err != nil {
		fail(c, err)
		return
	}
	dest, err := m.policy.Check(in.Dest, true)
	if err != nil {
		fail(c, err)
		return
	}
	lower := strings.ToLower(source)
	if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".tar.gz") {
		response.Fail(c, response.Invalid, "仅支持zip和tar.gz")
		return
	}
	info, err := os.Stat(source)
	if err != nil {
		fail(c, err)
		return
	}
	if !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "请选择普通压缩文件")
		return
	}
	if in.Encoding == "" {
		in.Encoding = "UTF-8"
	}
	if in.DirMode == "" {
		in.DirMode = "755"
	}
	mode, err := strconv.ParseUint(in.DirMode, 8, 9)
	if err != nil || len(in.DirMode) != 3 || (in.Encoding != "UTF-8" && in.Encoding != "GB18030" && in.Encoding != "GBK") || len(in.Password) > 1024 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "编码、目录权限或密码无效")
		return
	}
	if strings.HasSuffix(lower, ".tar.gz") && in.Password != "" {
		response.Fail(c, response.Invalid, "tar.gz不支持解压密码，请留空")
		return
	}
	opts := extraction{Password: in.Password, Encoding: in.Encoding, DirMode: os.FileMode(mode), UID: -1, GID: -1, Overwrite: in.Overwrite, Smart: in.Smart}
	if in.Owner != "" {
		owner, e := user.Lookup(in.Owner)
		if e != nil {
			response.Fail(c, response.Invalid, "所有者不存在")
			return
		}
		opts.UID, err = strconv.Atoi(owner.Uid)
		if err == nil {
			opts.GID, err = strconv.Atoi(owner.Gid)
		}
		if err != nil {
			response.Fail(c, response.Invalid, "所有者ID无效")
			return
		}
	}
	m.submit(c, "extract", []string{source}, func(ctx context.Context, _ string) error { return m.extractWithOptions(ctx, source, dest, opts) })
}

// 旧调用保留默认行为；新窗口通过选项控制智能目录和覆盖。
func (m *Module) extractArchive(ctx context.Context, src, dest string) error {
	return m.extractWithOptions(ctx, src, dest, extraction{Encoding: "UTF-8", DirMode: 0755, UID: -1, GID: -1})
}
func (m *Module) walkArchive(ctx context.Context, src string, opts extraction, contents bool, visit func(string, os.FileMode, io.Reader) error) (err error) {
	defer func() {
		if errors.Is(err, securezip.ErrPassword) || errors.Is(err, securezip.ErrAuthentication) {
			err = errArchivePassword
		}
	}()
	var total uint64
	count := 0
	entry := func(raw string, mode os.FileMode, size uint64, r io.Reader) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := raw
		var err error
		if opts.Encoding == "GBK" {
			name, err = simplifiedchinese.GBK.NewDecoder().String(raw)
		} else if opts.Encoding == "GB18030" {
			name, err = simplifiedchinese.GB18030.NewDecoder().String(raw)
		}
		if err != nil || !utf8.ValidString(name) {
			return errors.New("文件名编码无效，请选择正确编码")
		}
		if name == "" || strings.ContainsAny(name, "\\\x00:") || filepath.IsAbs(name) {
			return errors.New("压缩条目路径无效")
		}
		clean := filepath.Clean(filepath.FromSlash(name))
		if clean == "." && mode.IsDir() {
			return nil
		}
		if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return errors.New("压缩条目越界")
		}
		if !mode.IsRegular() && !mode.IsDir() {
			return errors.New("压缩包不能包含链接或特殊文件")
		}
		count++
		if count > m.cfg.ExtractMaxEntries || size > uint64(m.cfg.ExtractMaxBytes)-total {
			return errLimit
		}
		total += size
		return visit(clean, mode, r)
	}
	if strings.HasSuffix(strings.ToLower(src), ".zip") {
		reader, err := securezip.OpenReader(src)
		if err != nil {
			return err
		}
		defer reader.Close()
		for _, file := range reader.File {
			if file.IsEncrypted() {
				if opts.Password == "" {
					return errArchivePassword
				}
				file.SetPassword(opts.Password)
			}
			if !contents {
				if err = entry(file.Name, file.Mode(), file.UncompressedSize64, nil); err != nil {
					return err
				}
				continue
			}
			r, err := file.Open()
			if err != nil {
				return err
			}
			err = entry(file.Name, file.Mode(), file.UncompressedSize64, r)
			closeErr := r.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	}
	file, err := os.Open(src)
	if err != nil {
		return err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gz.Close()
	reader := tar.NewReader(contextReader{ctx: ctx, r: gz})
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errors.New("压缩包不能包含链接或特殊文件")
		}
		mode := os.FileMode(header.Mode) & 0777
		if header.Typeflag == tar.TypeDir {
			mode |= os.ModeDir
		}
		if err = entry(header.Name, mode, uint64(header.Size), reader); err != nil {
			return err
		}
	}
}
func (m *Module) extractWithOptions(ctx context.Context, src, dest string, opts extraction) (err error) {
	dest, err = m.policy.Check(dest, true)
	if err != nil {
		return err
	}
	if opts.DirMode == 0 {
		opts.DirMode = 0755
	}
	if opts.UID < 0 {
		if info, statErr := os.Stat(dest); statErr == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok && (st.Uid != 0 || st.Gid != 0) {
				opts.UID, opts.GID = int(st.Uid), int(st.Gid)
			}
		}
	}
	created := []string{}
	defer func() {
		if err != nil {
			for i := len(created) - 1; i >= 0; i-- {
				_ = os.Remove(created[i])
			}
		}
	}()
	var ensureDir func(string) error
	ensureDir = func(path string) error {
		if path == filepath.Dir(path) {
			return nil
		}
		if err := ensureDir(filepath.Dir(path)); err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errConflict
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err = os.Mkdir(path, opts.DirMode); err != nil {
			return err
		}
		_ = os.Chmod(path, opts.DirMode)
		if opts.UID >= 0 {
			_ = os.Chown(path, opts.UID, opts.GID)
		}
		created = append(created, path)
		return nil
	}

	if opts.Smart {
		roots := map[string]bool{}
		loose := false
		if err = m.walkArchive(ctx, src, opts, false, func(name string, mode os.FileMode, _ io.Reader) error {
			root, _, nested := strings.Cut(name, string(filepath.Separator))
			roots[root] = true
			if !nested && !mode.IsDir() {
				loose = true
			}
			return nil
		}); err != nil {
			return err
		}
		if loose || len(roots) > 1 {
			if err = ensureDir(dest); err != nil {
				return err
			}
			base := filepath.Base(src)
			if strings.HasSuffix(strings.ToLower(base), ".tar.gz") {
				base = base[:len(base)-7]
			} else {
				base = base[:len(base)-4]
			}
			if base == "" || base == "." || base == ".." {
				base = "archive"
			}
			// 遇到同名目录生成新名称，不把本次散碎文件混入已有目录。
			for n := 0; ; n++ {
				name := base
				if n > 0 {
					name += "_" + strconv.Itoa(n)
				}
				candidate := filepath.Join(dest, name)
				if _, err = m.policy.CheckLink(candidate, true); err != nil {
					return err
				}
				err = os.Mkdir(candidate, 0755)
				if os.IsExist(err) {
					continue
				}
				if err != nil {
					return err
				}
				created = append(created, candidate)
				dest = candidate
				break
			}

		}
	}
	var total int64
	err = m.walkArchive(ctx, src, opts, true, func(name string, mode os.FileMode, reader io.Reader) error {
		target := filepath.Join(dest, name)
		resolved, err := m.policy.CheckLink(target, true)
		if err != nil {
			return err
		}
		if !pathsafe.Contains(dest, resolved) {
			return errors.New("压缩条目越界")
		}
		if err = ensureDir(filepath.Dir(target)); err != nil {
			return err
		}
		if mode.IsDir() {
			return ensureDir(target)
		}
		old, oldErr := os.Lstat(target)
		if oldErr == nil && (!opts.Overwrite || !old.Mode().IsRegular()) {
			return errConflict
		}
		if oldErr != nil && !os.IsNotExist(oldErr) {
			return oldErr
		}
		f, err := os.CreateTemp(filepath.Dir(target), ".panel-extract-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		if err = f.Chmod(opts.DirMode); err != nil {
			f.Close()
			return err
		}
		n, err := io.Copy(f, io.LimitReader(contextReader{ctx: ctx, r: reader}, m.cfg.ExtractMaxBytes-total+1))
		total += n
		if total > m.cfg.ExtractMaxBytes {
			err = errLimit
		}
		if err == nil && opts.UID >= 0 {
			err = f.Chown(opts.UID, opts.GID)
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if oldErr == nil {
			err = os.Rename(f.Name(), target)
		} else {
			err = os.Link(f.Name(), target)
		}
		if err == nil && os.IsNotExist(oldErr) {
			created = append(created, target)
		}
		return err
	})
	if err != nil {
		return err
	}
	for i := len(created) - 1; i >= 0; i-- {
		info, e := os.Lstat(created[i])
		if e != nil {
			return e
		}
		if info.IsDir() {
			if opts.UID >= 0 {
				if e = os.Chown(created[i], opts.UID, opts.GID); e != nil {
					return e
				}
			}
			if e = os.Chmod(created[i], opts.DirMode); e != nil {
				return e
			}
		}
	}
	return nil
}
