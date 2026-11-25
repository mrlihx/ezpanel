package pkgmgr

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"ezpanel/internal/executor"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) compatSource(ctx context.Context, work, name, address, expected string, log func(string), pecl ...bool) (string, error) {
	log("下载PHP构建源码：" + name)
	if expected == "" {
		expected = m.releaseChecksum(ctx, Release{URL: address})
	}
	req, e := http.NewRequestWithContext(ctx, "GET", m.fileURL(address), nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("User-Agent", softwareUserAgent)
	client := http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 || r.URL.Scheme != "https" {
			return errors.New("兼容依赖只允许HTTPS下载")
		}
		return nil
	}}
	res, e := client.Do(req)
	if e != nil {
		return "", e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", fmt.Errorf("PHP兼容依赖%s下载失败HTTP %d", name, res.StatusCode)
	}
	archive := filepath.Join(work, name+".archive")
	f, e := os.Create(archive)
	if e != nil {
		return "", e
	}
	hash := sha256.New()
	n, e := io.Copy(io.MultiWriter(f, hash), io.LimitReader(res.Body, (128<<20)+1))
	closeErr := f.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > 128<<20 {
		return "", errors.New("PHP兼容依赖归档超过128MiB")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if expected != "" && digest != expected {
		return "", errors.New("PHP兼容依赖SHA256不匹配")
	}
	log(name + " 归档SHA256：" + digest)
	if strings.HasSuffix(address, ".xz") {
		input, e := os.Open(archive)
		if e != nil {
			return "", e
		}
		defer input.Close()
		converted, e := os.Create(archive + ".gz")
		if e != nil {
			return "", e
		}
		writer := gzip.NewWriter(converted)
		bin, e := m.LookPath("xz")
		if e == nil {
			_, e = executor.Pipe(ctx, 5*time.Minute, nil, input, writer, bin, "-dc")
		}
		zipErr := writer.Close()
		fileErr := converted.Close()
		if e != nil {
			return "", e
		}
		if zipErr != nil {
			return "", zipErr
		}
		if fileErr != nil {
			return "", fileErr
		}
		archive += ".gz"
	}
	src := filepath.Join(work, "compat-"+name)
	if e = os.MkdirAll(src, 0700); e != nil {
		return "", e
	}
	if e = extractSource(archive, src, pecl...); e != nil {
		return "", e
	}
	return src, nil
}
