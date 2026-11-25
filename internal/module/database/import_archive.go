package database

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"strings"
)

var errImportTooLarge = errors.New("文件超过导入大小上限")

type importReader struct {
	ctx context.Context
	io.Reader
}

func (r importReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func copyImportFile(ctx context.Context, dst io.Writer, src io.Reader, limit int64) error {
	n, err := io.Copy(dst, io.LimitReader(importReader{ctx, src}, limit+1))
	if n > limit {
		return errImportTooLarge
	}
	if err == nil && n == 0 {
		return errors.New("备份文件为空")
	}
	return err
}

// 只读取归档中的 SQL 内容，归档条目路径不会写入文件系统。
func readImportSQL(ctx context.Context, dst io.Writer, src io.ReaderAt, size int64, name string, limit int64, entryLimit int) error {
	lower := strings.ToLower(name)
	if strings.HasSuffix(lower, ".zip") {
		archive, err := zip.NewReader(src, size)
		if err != nil {
			return err
		}
		if len(archive.File) > entryLimit {
			return errors.New("归档条目超过上限")
		}
		var sqlFile *zip.File
		for _, file := range archive.File {
			if !file.Mode().IsRegular() || !strings.HasSuffix(strings.ToLower(file.Name), ".sql") {
				continue
			}
			if sqlFile != nil {
				return errors.New("压缩包只能包含一个 SQL 文件")
			}
			sqlFile = file
		}
		if sqlFile == nil {
			return errors.New("压缩包中没有可导入的 SQL 文件")
		}
		r, err := sqlFile.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		return copyImportFile(ctx, dst, r, limit)
	}

	input := io.NewSectionReader(src, 0, size)
	if strings.HasSuffix(lower, ".sql") {
		return copyImportFile(ctx, dst, input, limit)
	}
	gz, err := gzip.NewReader(input)
	if err != nil {
		return err
	}
	defer gz.Close()
	if !strings.HasSuffix(lower, ".tar.gz") && !strings.HasSuffix(lower, ".tgz") {
		return copyImportFile(ctx, dst, gz, limit)
	}

	// TAR 的大小限制覆盖全部解压字节，包含跳过的条目和尾部。
	limited := &io.LimitedReader{R: importReader{ctx, gz}, N: limit + 1}
	archive := tar.NewReader(limited)
	found := false
	for entries := 0; ; entries++ {
		header, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if entries >= entryLimit {
			return errors.New("归档条目超过上限")
		}
		if !header.FileInfo().Mode().IsRegular() || !strings.HasSuffix(strings.ToLower(header.Name), ".sql") {
			continue
		}
		if found {
			return errors.New("压缩包只能包含一个 SQL 文件")
		}
		found = true
		if err := copyImportFile(ctx, dst, archive, limit); err != nil {
			return err
		}
	}
	if _, err := io.Copy(io.Discard, limited); err != nil {
		return err
	}
	if limited.N == 0 {
		return errImportTooLarge
	}
	if !found {
		return errors.New("压缩包中没有可导入的 SQL 文件")
	}
	return nil
}
