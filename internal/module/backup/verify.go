package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
)

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(buf []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(buf)
}

// VerifySiteBackup 完整读取压缩流，让 gzip 验证文件结尾、校验和与原始长度。
func (m *Module) VerifySiteBackup(ctx context.Context, siteID, id string) (string, error) {
	var row record
	if err := m.db.WithContext(ctx).Table("site_backups").Where("id = ? AND website_id = ?", id, siteID).First(&row).Error; err != nil {
		return "", err
	}
	source, _, err := m.openBackup(ctx, row)
	if err != nil {
		return "", err
	}
	defer source.Close()
	hash := sha256.New()
	var input io.Reader = contextReader{ctx, source}
	var limit *io.LimitedReader
	if row.Size > 0 {
		limit = &io.LimitedReader{R: input, N: row.Size + 1}
		input = limit
	}
	reader, err := gzip.NewReader(io.TeeReader(input, hash))
	if err != nil {
		return "", err
	}
	defer reader.Close()
	archive := tar.NewReader(contextReader{ctx, reader})
	seen := false
	for {
		_, err := archive.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		seen = true
	}
	if !seen {
		return "", errors.New("备份不包含网站归档条目")
	}
	if _, err = io.Copy(io.Discard, contextReader{ctx, reader}); err != nil {
		return "", err
	}
	if limit != nil && limit.N != 1 {
		return "", errors.New("备份大小与登记记录不一致")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
