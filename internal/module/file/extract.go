package file

import (
	"archive/tar"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	securezip "github.com/yeka/zip"
	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	zipMethodBZip2 uint16 = 12
	zipMethodZstd  uint16 = 93
)

func init() {
	securezip.RegisterDecompressor(zipMethodBZip2, func(r io.Reader) io.ReadCloser {
		return io.NopCloser(bzip2.NewReader(r))
	})
	securezip.RegisterDecompressor(zipMethodZstd, newZstdReadCloser)
}

type zstdReadCloser struct {
	dec *zstd.Decoder
	err error
}

func newZstdReadCloser(r io.Reader) io.ReadCloser {
	dec, err := zstd.NewReader(r)
	return &zstdReadCloser{dec: dec, err: err}
}

func (z *zstdReadCloser) Read(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	return z.dec.Read(p)
}

func (z *zstdReadCloser) Close() error {
	if z.dec != nil {
		z.dec.Close()
		z.dec = nil
	}
	return nil
}

var (
	errArchivePassword    = errors.New("解压密码缺失或不正确")
	errArchiveAlgorithm   = errors.New("压缩包使用了不支持的压缩算法")
	errArchiveFormat      = errors.New("压缩包格式无效或已损坏")
	errArchiveChecksum    = errors.New("压缩包校验失败，文件可能已损坏")
	errArchiveEncoding    = errors.New("文件名编码无效，请选择正确编码")
	errArchivePath        = errors.New("压缩条目路径无效")
	errArchiveEscape      = errors.New("压缩条目越界")
	errArchiveSpecial     = errors.New("压缩包不能包含链接或特殊文件")
	errArchiveToolMissing = errors.New("解压该格式需要安装服务器的7zip软件包")
)

func findSevenZip() string {
	for _, name := range []string{"7zz", "7z", "7za"} {
		if bin, err := executor.LookPath(name); err == nil {
			return bin
		}
	}
	return ""
}

func archiveKind(path string) (kind string, baseName string, supportsPassword bool, singleFile bool) {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	for _, spec := range []struct {
		suffix           string
		kind             string
		supportsPassword bool
		singleFile       bool
	}{
		{".tar.gz", "tar.gz", false, false},
		{".tar.bz2", "tar.bz2", false, false},
		{".tar.xz", "tar.xz", false, false},
		{".tar.zst", "tar.zst", false, false},
		{".tgz", "tar.gz", false, false},
		{".tbz2", "tar.bz2", false, false},
		{".txz", "tar.xz", false, false},
		{".tzst", "tar.zst", false, false},
		{".zip", "zip", true, false},
		{".tar", "tar", false, false},
		{".7z", "7z", true, false},
		{".rar", "rar", true, false},
		{".gz", "gz", false, true},
		{".bz2", "bz2", false, true},
		{".xz", "xz", false, true},
		{".zst", "zst", false, true},
	} {
		if strings.HasSuffix(lower, spec.suffix) {
			return spec.kind, base[:len(base)-len(spec.suffix)], spec.supportsPassword, spec.singleFile
		}
	}
	return "", base, false, false
}

type extraction struct {
	Password  string
	Encoding  string
	DirMode   os.FileMode
	UID, GID  int
	Overwrite bool
	Smart     bool
	KeepMode  bool
	siteRoot  string
	tarLink   func(string, *tar.Header) error
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
	kind, _, supportsPassword, _ := archiveKind(source)
	if kind == "" {
		response.Fail(c, response.Invalid, "不支持的压缩包格式")
		return
	}
	if (kind == "7z" || kind == "rar") && findSevenZip() == "" {
		response.Fail(c, response.Invalid, errArchiveToolMissing.Error())
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
	if !supportsPassword && in.Password != "" {
		response.Fail(c, response.Invalid, "当前压缩格式不支持解压密码，请留空")
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

// ExtractSiteBackup 在面板刚创建的空目录中校验并解包站点归档。
func (m *Module) ExtractSiteBackup(ctx context.Context, src, dest, siteRoot string) error {
	return m.extractWithOptions(ctx, src, dest, extraction{Encoding: "UTF-8", UID: -1, GID: -1, KeepMode: true, siteRoot: filepath.Clean(siteRoot)})
}

func (m *Module) validateEntry(ctx context.Context, opts extraction, count *int, total *uint64, raw string, mode os.FileMode, size uint64, r io.Reader, visit func(string, os.FileMode, io.Reader) error) error {
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
		return errArchiveEncoding
	}
	if name == "" || strings.ContainsAny(name, "\\\x00:") || filepath.IsAbs(name) {
		return errArchivePath
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." && mode.IsDir() {
		return nil
	}
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errArchiveEscape
	}
	if !mode.IsRegular() && !mode.IsDir() {
		return errArchiveSpecial
	}
	*count++
	if *count > m.cfg.ExtractMaxEntries || size > uint64(m.cfg.ExtractMaxBytes)-*total {
		return errLimit
	}
	*total += size
	return visit(clean, mode, r)
}

func mapArchiveError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, securezip.ErrPassword) || errors.Is(err, securezip.ErrAuthentication) {
		return errArchivePassword
	}
	if errors.Is(err, securezip.ErrAlgorithm) {
		return errArchiveAlgorithm
	}
	if errors.Is(err, securezip.ErrChecksum) || errors.Is(err, gzip.ErrChecksum) {
		return errArchiveChecksum
	}
	var bzErr bzip2.StructuralError
	if errors.Is(err, securezip.ErrFormat) || errors.Is(err, gzip.ErrHeader) || errors.Is(err, tar.ErrHeader) || errors.As(err, &bzErr) {
		return errArchiveFormat
	}
	return err
}

func walkTarStream(ctx context.Context, r io.Reader, entry func(string, os.FileMode, uint64, io.Reader) error, link func(*tar.Header) error) error {
	reader := tar.NewReader(contextReader{ctx: ctx, r: r})
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if link != nil && (header.Typeflag == tar.TypeSymlink || header.Typeflag == tar.TypeLink) {
			if err = link(header); err != nil {
				return err
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return errArchiveSpecial
		}
		if header.Size < 0 {
			return errArchiveFormat
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

func openCompressedStream(kind string, r io.Reader) (io.Reader, func() error, error) {
	switch kind {
	case "tar":
		return r, func() error { return nil }, nil
	case "tar.gz", "gz":
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return gz, gz.Close, nil
	case "tar.bz2", "bz2":
		return bzip2.NewReader(r), func() error { return nil }, nil
	case "tar.xz", "xz":
		xr, err := xz.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return xr, func() error { return nil }, nil
	case "tar.zst", "zst":
		zr, err := zstd.NewReader(r)
		if err != nil {
			return nil, nil, err
		}
		return zr, func() error { zr.Close(); return nil }, nil
	default:
		return nil, nil, errArchiveFormat
	}
}

func (m *Module) walkArchive(ctx context.Context, src string, opts extraction, contents bool, visit func(string, os.FileMode, io.Reader) error) (err error) {
	defer func() {
		err = mapArchiveError(err)
	}()
	kind, baseName, _, singleFile := archiveKind(src)
	if kind == "" {
		return errArchiveFormat
	}
	var total uint64
	count := 0
	entry := func(raw string, mode os.FileMode, size uint64, r io.Reader) error {
		return m.validateEntry(ctx, opts, &count, &total, raw, mode, size, r, visit)
	}
	if kind == "zip" {
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
	stream, closeStream, err := openCompressedStream(kind, contextReader{ctx: ctx, r: file})
	if err != nil {
		return err
	}
	defer closeStream()
	if singleFile {
		outName := baseName
		if outName == "" || outName == "." || outName == ".." {
			outName = "extracted"
		}
		if !contents {
			return entry(outName, 0644, 0, nil)
		}
		return entry(outName, 0644, 0, stream)
	}
	var link func(*tar.Header) error
	if opts.tarLink != nil {
		link = func(header *tar.Header) error {
			return m.validateEntry(ctx, opts, &count, &total, header.Name, 0, 0, nil, func(name string, _ os.FileMode, _ io.Reader) error {
				return opts.tarLink(name, header)
			})
		}
	}
	if err := walkTarStream(ctx, stream, entry, link); err != nil || kind == "tar" {
		return err
	}
	// tar 结束标记后仍需读完压缩流，才能校验压缩尾部。
	tail := &io.LimitedReader{R: contextReader{ctx: ctx, r: stream}, N: max(m.cfg.ExtractMaxBytes, 1<<20)}
	if _, err := io.Copy(io.Discard, tail); err != nil {
		return err
	}
	if tail.N == 0 {
		return errLimit
	}
	return nil
}

func mapSevenZipError(res executor.Result, err error) error {
	combined := strings.ToLower(res.Stdout + "\n" + res.Stderr)
	if strings.Contains(combined, "wrong password") ||
		strings.Contains(combined, "can not open encrypted archive") ||
		strings.Contains(combined, "enter password") ||
		strings.Contains(combined, "data error in encrypted file") {
		return errArchivePassword
	}
	if strings.Contains(combined, "crc failed") || strings.Contains(combined, "data error") {
		return errArchiveChecksum
	}
	if strings.Contains(combined, "cannot open the file as archive") ||
		strings.Contains(combined, "headers error") ||
		strings.Contains(combined, "unexpected end of archive") {
		return errArchiveFormat
	}
	return err
}

func parseSevenZipList(output io.Reader, maxEntries int, maxBytes int64) error {
	entries := 0
	var total int64
	lines := bufio.NewScanner(output)
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if strings.HasPrefix(line, "Path = ") && len(line) > len("Path = ") {
			entries++
			if entries > maxEntries {
				return errLimit
			}
		} else if raw, ok := strings.CutPrefix(line, "Size = "); ok {
			size, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if errors.Is(err, strconv.ErrRange) {
				return errLimit
			}
			if err != nil || size < 0 {
				continue
			}
			if size > maxBytes-total {
				return errLimit
			}
			total += size
		}
	}
	return lines.Err()
}

func (m *Module) extractSevenZipToStaging(ctx context.Context, src string, opts extraction) (string, error) {
	sevenZip := findSevenZip()
	if sevenZip == "" {
		return "", errArchiveToolMissing
	}
	// 逐行读取完整清单，避免命令日志的体积上限截断条目。
	listReader, listWriter := io.Pipe()
	parsed := make(chan error, 1)
	go func() {
		err := parseSevenZipList(listReader, m.cfg.ExtractMaxEntries, m.cfg.ExtractMaxBytes)
		_ = listReader.Close()
		parsed <- err
	}()
	listRes, err := executor.Pipe(ctx, time.Hour, nil, nil, listWriter, sevenZip, "l", "-slt", "-ba", "-sccUTF-8", "-p"+opts.Password, "--", src)
	_ = listWriter.Close()
	if parseErr := <-parsed; parseErr != nil {
		return "", parseErr
	}
	if err != nil {
		return "", mapSevenZipError(listRes, err)
	}
	stagingDir, err := os.MkdirTemp("", ".panel-extract-7z-*")
	if err != nil {
		return "", err
	}
	if err = os.Chmod(stagingDir, 0700); err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", err
	}
	res, err := executor.RunFixed(ctx, time.Hour, stagingDir, nil, sevenZip, "x", "-y", "-bd", "-snl", "-sccUTF-8", "-p"+opts.Password, "-o"+stagingDir, "--", src)
	if err != nil {
		_ = os.RemoveAll(stagingDir)
		return "", mapSevenZipError(res, err)
	}
	return stagingDir, nil
}

func (m *Module) walkStagedArchive(ctx context.Context, stagingDir string, opts extraction, contents bool, visit func(string, os.FileMode, io.Reader) error) error {
	var total uint64
	count := 0
	return filepath.WalkDir(stagingDir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if p == stagingDir {
			return nil
		}
		rel, err := filepath.Rel(stagingDir, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		mode := info.Mode()
		size := uint64(0)
		if mode.IsRegular() && info.Size() > 0 {
			size = uint64(info.Size())
		}
		raw := filepath.ToSlash(rel)
		if !contents || !mode.IsRegular() {
			return m.validateEntry(ctx, opts, &count, &total, raw, mode, size, nil, visit)
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		err = m.validateEntry(ctx, opts, &count, &total, raw, mode, size, f, visit)
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	})
}

func (m *Module) extractWithOptions(ctx context.Context, src, dest string, opts extraction) (err error) {
	kind, baseName, _, singleFile := archiveKind(src)
	if kind == "" {
		return errArchiveFormat
	}
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
	type replacement struct{ target, backup string }
	var replaced []replacement
	defer func() {
		for i := len(replaced) - 1; i >= 0; i-- {
			item := replaced[i]
			if err != nil {
				if restoreErr := os.Rename(item.backup, item.target); restoreErr != nil {
					err = errors.Join(err, restoreErr)
					continue
				}
			}
			_ = os.RemoveAll(filepath.Dir(item.backup))
		}
		if err != nil {
			for i := len(created) - 1; i >= 0; i-- {
				_ = os.Remove(created[i])
			}
		}
	}()
	type archiveLink struct {
		name, target string
		hard         bool
	}
	var links []archiveLink
	if opts.KeepMode {
		opts.tarLink = func(name string, header *tar.Header) error {
			target := filepath.FromSlash(header.Linkname)
			hard := header.Typeflag == tar.TypeLink
			if target == "" || strings.ContainsAny(target, "\\\x00:") {
				return errArchivePath
			}
			if hard {
				target = filepath.Clean(target)
				if filepath.IsAbs(target) || target == ".." || strings.HasPrefix(target, ".."+string(filepath.Separator)) {
					return errArchiveEscape
				}
			} else if filepath.IsAbs(target) {
				if !filepath.IsAbs(opts.siteRoot) {
					return errArchiveEscape
				}
				suffix := "."
				if target != opts.siteRoot {
					var inside bool
					suffix, inside = strings.CutPrefix(target, opts.siteRoot+string(filepath.Separator))
					if !inside {
						return errArchiveEscape
					}
				}
				back, err := filepath.Rel(filepath.Dir(name), ".")
				if err != nil {
					return err
				}
				// 保留目标路径中的目录跳转，交给根目录句柄校验实际解析结果。
				target = back + string(filepath.Separator) + suffix
			}
			links = append(links, archiveLink{name, target, hard})
			return nil
		}
	}
	var stagingDir string
	if kind == "7z" || kind == "rar" {
		stagingDir, err = m.extractSevenZipToStaging(ctx, src, opts)
		if err != nil {
			return err
		}
		defer os.RemoveAll(stagingDir)
	}
	walk := func(contents bool, visit func(string, os.FileMode, io.Reader) error) error {
		if stagingDir != "" {
			return m.walkStagedArchive(ctx, stagingDir, opts, contents, visit)
		}
		return m.walkArchive(ctx, src, opts, contents, visit)
	}
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

	if opts.Smart && !singleFile {
		roots := map[string]bool{}
		loose := false
		if err = walk(false, func(name string, mode os.FileMode, _ io.Reader) error {
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
			base := baseName
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
	dirModes := map[string]os.FileMode{}
	err = walk(true, func(name string, mode os.FileMode, reader io.Reader) error {
		target := filepath.Join(dest, name)
		resolved, err := m.policy.CheckLink(target, true)
		if err != nil {
			return err
		}
		if !pathsafe.Contains(dest, resolved) {
			return errArchiveEscape
		}
		if err = ensureDir(filepath.Dir(target)); err != nil {
			return err
		}
		if mode.IsDir() {
			if opts.KeepMode {
				dirModes[target] = mode.Perm()
			}
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
		permissions := opts.DirMode
		if opts.KeepMode {
			permissions = mode.Perm()
		}
		if err = f.Chmod(permissions); err != nil {
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
			// 保留旧文件至整个归档通过校验，后续失败时可按顺序恢复。
			dir, err := os.MkdirTemp(filepath.Dir(target), ".panel-extract-backup-")
			if err != nil {
				return err
			}
			backup := filepath.Join(dir, "original")
			if err = os.Link(target, backup); err != nil {
				_ = os.Remove(dir)
				return err
			}
			replaced = append(replaced, replacement{target, backup})
			return os.Rename(f.Name(), target)
		}
		if err = os.Link(f.Name(), target); err == nil {
			created = append(created, target)
		}
		return err
	})
	if err != nil {
		return err
	}
	if len(links) > 0 {
		root, err := os.OpenRoot(dest)
		if err != nil {
			return err
		}
		defer root.Close()
		for _, link := range links {
			if err := ensureDir(filepath.Join(dest, filepath.Dir(link.name))); err != nil {
				return err
			}
		}
		for _, link := range links {
			if !link.hard {
				continue
			}
			info, err := root.Lstat(link.target)
			if err != nil || !info.Mode().IsRegular() {
				return errArchiveSpecial
			}
			if err := root.Link(link.target, link.name); err != nil {
				return err
			}
			created = append(created, filepath.Join(dest, link.name))
		}
		for _, link := range links {
			if link.hard {
				continue
			}
			if err := root.Symlink(link.target, link.name); err != nil {
				return err
			}
			created = append(created, filepath.Join(dest, link.name))
		}
		// 全部链接创建后验证实际解析结果，临时目录验证成功才会发布。
		for _, link := range links {
			if !link.hard {
				if _, err := root.Stat(link.name); err != nil {
					return errors.New("归档链接目标无法在站点内解析")
				}
			}
		}
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
			mode, ok := dirModes[created[i]]
			if !ok {
				mode = opts.DirMode
			}
			if e = os.Chmod(created[i], mode); e != nil {
				return e
			}
		}
	}
	return nil
}
