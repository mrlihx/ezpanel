package geoip

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

type xdbReader struct {
	mu        sync.Mutex
	createdAt uint32
	*xdb.Searcher
}

var databases struct {
	sync.RWMutex
	xdb  [2]*xdbReader
	mmdb *maxminddb.Reader
}

var xdbFiles = []struct {
	name    string
	version *xdb.Version
}{
	{"ip2region_v4.xdb", xdb.IPv4},
	{"ip2region_v6.xdb", xdb.IPv6},
}

// Open 优先加载已有库，后台下载期间面板可以继续提供查询。
func Open(parent context.Context, dir string) func() {
	var updated [2]time.Time
	downloadDir := filepath.Join(dir, "managed")
	for i, source := range xdbFiles {
		for _, root := range []string{downloadDir, dir} {
			path := filepath.Join(root, source.name)
			reader, err := openXDB(path, source.version)
			if err == nil {
				databases.Lock()
				databases.xdb[i] = reader
				databases.Unlock()
				if root == downloadDir {
					if info, err := os.Stat(path); err == nil {
						updated[i] = info.ModTime()
					}
				}
				break
			}
			if !errors.Is(err, os.ErrNotExist) {
				slog.Warn("无法打开 IP 归属地数据库", "path", path, "error", err)
			}
		}
	}
	for _, name := range []string{"GeoIP2-City.mmdb", "GeoLite2-City.mmdb", "GeoIP2-Country.mmdb", "GeoLite2-Country.mmdb"} {
		path := filepath.Join(dir, name)
		reader, err := maxminddb.Open(path)
		if err == nil {
			databases.Lock()
			databases.mmdb = reader
			databases.Unlock()
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("无法打开 IP 归属地数据库", "path", path, "error", err)
		}
	}

	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		updateDatabases(ctx, downloadDir, updated)
	}()
	return func() {
		cancel()
		<-done
		databases.Lock()
		defer databases.Unlock()
		for i, reader := range databases.xdb {
			if reader != nil {
				reader.Close()
				databases.xdb[i] = nil
			}
		}
		if databases.mmdb != nil {
			_ = databases.mmdb.Close()
			databases.mmdb = nil
		}
	}
}

func openXDB(path string, version *xdb.Version) (*xdbReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader, err := readXDB(file, version)
	if err != nil {
		file.Close()
		return nil, err
	}
	return reader, nil
}

func readXDB(file *os.File, version *xdb.Version) (*xdbReader, error) {
	if err := xdb.Verify(file); err != nil {
		return nil, err
	}
	header, err := xdb.LoadHeader(file)
	if err != nil {
		return nil, err
	}
	if header.IPVersion != 0 && header.IPVersion != version.Id ||
		header.Version == xdb.Structure20 && version == xdb.IPv6 {
		return nil, errors.New("数据库 IP 版本与文件名不一致")
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if header.StartIndexPtr < xdb.XdbBufferMinBytes || header.EndIndexPtr < header.StartIndexPtr ||
		int64(header.EndIndexPtr)+int64(version.SegmentIndexSize) > info.Size() {
		return nil, errors.New("数据库索引不完整")
	}
	index, err := xdb.LoadVectorIndex(file)
	if err != nil {
		return nil, err
	}
	reader, err := xdb.NewWithReader(version, file, index, nil)
	if err != nil {
		return nil, err
	}
	return &xdbReader{createdAt: header.CreatedAt, Searcher: reader}, nil
}

type names map[string]string

func (n names) display() string {
	if n["zh-CN"] != "" {
		return n["zh-CN"]
	}
	return n["en"]
}

func Location(value string) string {
	ip, err := netip.ParseAddr(value)
	if err != nil {
		return ""
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback():
		return "本机"
	case ip.IsPrivate(), ip.IsLinkLocalUnicast():
		return "内网"
	case !ip.IsGlobalUnicast():
		return ""
	}

	databases.RLock()
	mmdbReader := databases.mmdb
	xdbReader := databases.xdb[1]
	if ip.Is4() {
		xdbReader = databases.xdb[0]
	}
	databases.RUnlock()

	if mmdbReader != nil {
		var record struct {
			Country struct {
				Names names `maxminddb:"names"`
			} `maxminddb:"country"`
			Subdivisions []struct {
				Names names `maxminddb:"names"`
			} `maxminddb:"subdivisions"`
			City struct {
				Names names `maxminddb:"names"`
			} `maxminddb:"city"`
		}
		result := mmdbReader.Lookup(ip)
		if result.Found() && result.Decode(&record) == nil {
			parts := []string{record.Country.Names.display()}
			for _, subdivision := range record.Subdivisions {
				parts = append(parts, subdivision.Names.display())
			}
			if location := formatLocation(append(parts, record.City.Names.display())); location != "" {
				return location
			}
		}
	}

	if xdbReader == nil {
		return ""
	}
	xdbReader.mu.Lock()
	region, err := xdbReader.Search(ip.AsSlice())
	xdbReader.mu.Unlock()
	if err != nil {
		return ""
	}
	parts := strings.Split(region, "|")
	// XDB 末尾的 ISO 国家代码已有中文国家名，无需重复显示。
	if len(parts) > 1 && len(parts[len(parts)-1]) == 2 {
		parts = parts[:len(parts)-1]
	}
	return formatLocation(parts)
}

func formatLocation(parts []string) string {
	unique := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && part != "0" && (len(unique) == 0 || unique[len(unique)-1] != part) {
			unique = append(unique, part)
		}
	}
	return strings.Join(unique, " ")
}
