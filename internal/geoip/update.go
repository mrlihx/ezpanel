package geoip

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

var xdbOrigins = [...]string{
	"https://raw.githubusercontent.com/lionsoul2014/ip2region/master/data/",
	"https://gitee.com/lionsoul/ip2region/raw/master/data/",
}

type xdbCache struct {
	origin string
	etag   string
}

const xdbRefresh = 7 * 24 * time.Hour
const xdbMaxDownload = 128 << 20

func updateDatabases(ctx context.Context, dir string, updated [2]time.Time) {
	client := &http.Client{
		Timeout: 3 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	var caches [2]xdbCache
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		for i, source := range xdbFiles {
			if ctx.Err() != nil {
				return
			}
			if time.Since(updated[i]) < xdbRefresh {
				continue
			}
			path := filepath.Join(dir, source.name)
			var minDate uint32
			if !updated[i].IsZero() {
				databases.RLock()
				if databases.xdb[i] != nil {
					minDate = databases.xdb[i].createdAt
				}
				databases.RUnlock()
			}
			var reader *xdbReader
			var failures error
			for _, origin := range xdbOrigins {
				etag := ""
				if caches[i].origin == origin {
					etag = caches[i].etag
				}
				replacement, nextETag, err := downloadXDB(ctx, client, origin, path, source.version, minDate, etag)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) && caches[i].origin == origin {
						caches[i] = xdbCache{}
					}
					failures = errors.Join(failures, err)
					continue
				}
				reader, caches[i], failures = replacement, xdbCache{origin, nextETag}, nil
				updated[i] = time.Now()
				break
			}
			if failures != nil {
				if ctx.Err() == nil {
					slog.Warn("IP 归属地数据库更新失败", "file", source.name, "error", failures)
				}
				continue
			}
			if reader == nil {
				continue
			}
			databases.Lock()
			previous := databases.xdb[i]
			databases.xdb[i] = reader
			databases.Unlock()
			if previous != nil {
				previous.Close()
			}
			slog.Info("IP 归属地数据库已更新", "file", source.name)
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func downloadXDB(ctx context.Context, client *http.Client, origin, path string, version *xdb.Version, minDate uint32, etag string) (*xdbReader, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+filepath.Base(path), nil)
	if err != nil {
		return nil, "", err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified && etag != "" {
		now := time.Now()
		return nil, etag, os.Chtimes(path, now, now)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("下载源返回 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > xdbMaxDownload {
		return nil, "", errors.New("数据库超过下载大小限制")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, "", err
	}
	tmp, err := os.CreateTemp(dir, ".geoip-*.xdb")
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(tmp.Name())
	size, writeErr := io.Copy(tmp, io.LimitReader(resp.Body, xdbMaxDownload+1))
	if err := errors.Join(writeErr, tmp.Close()); err != nil {
		return nil, "", err
	}
	if size > xdbMaxDownload || resp.ContentLength >= 0 && size != resp.ContentLength {
		return nil, "", errors.New("数据库下载大小异常")
	}

	// 校验成功后才替换文件，查询随即切换到已打开的新版。
	reader, err := openXDB(tmp.Name(), version)
	if err != nil {
		return nil, "", err
	}
	if reader.createdAt < minDate {
		reader.Close()
		return nil, "", errors.New("下载源版本早于当前数据库")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		reader.Close()
		return nil, "", err
	}
	return reader, resp.Header.Get("ETag"), nil
}
