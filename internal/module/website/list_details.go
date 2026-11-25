package website

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var siteColumns = []string{"name", "status", "backups", "root", "traffic", "remark", "php", "ssl", "actions"}

func (m *Module) columns(c *gin.Context) {
	key := fmt.Sprintf("websites.columns.%d", auth.Current(c).User.ID)
	if c.Request.Method == "GET" {
		var setting store.Setting
		err := m.db.First(&setting, "key = ?", key).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.OK(c, gin.H{"columns": siteColumns})
			return
		}
		var columns []string
		if err != nil || json.Unmarshal([]byte(setting.Value), &columns) != nil {
			failure(c, errStorage)
			return
		}
		response.OK(c, gin.H{"columns": columns})
		return
	}
	var in struct {
		Columns []string `json:"columns"`
	}
	if !response.Bind(c, &in) {
		return
	}
	seen := map[string]bool{}
	for _, key := range in.Columns {
		valid := false
		for _, allowed := range siteColumns {
			if key == allowed {
				valid = true
				break
			}
		}
		if !valid || seen[key] {
			response.Fail(c, response.Invalid, "显示列无效或重复")
			return
		}
		seen[key] = true
	}
	if len(in.Columns) == 0 || !seen["actions"] {
		response.Fail(c, response.Invalid, "操作列必须显示")
		return
	}
	data, _ := json.Marshal(in.Columns)
	err := m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "key"}}, DoUpdates: clause.AssignmentColumns([]string{"value"})}).Create(&store.Setting{Key: key, Value: string(data)}).Error
	if err != nil {
		failure(c, errStorage)
		return
	}
	response.OK(c, in)
}

type trafficFile struct {
	info   os.FileInfo
	offset int64
	bytes  int64
}
type siteTraffic struct {
	day     string
	checked time.Time
	files   map[string]*trafficFile
	bytes   *int64
	reason  string
}

var accessBytes = regexp.MustCompile(`\[([^\]]+)\] "[^"]*" [0-9]{3} ([0-9]+|-)`)

// 列表不启动后台轮询；一分钟内复用结果，当前日志只追加读取新增完整行。
func (m *Module) dailyTraffic(id, name string) (*int64, string) {
	m.trafficMu.Lock()
	defer m.trafficMu.Unlock()
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		return nil, "统计时区不可用"
	}
	now := time.Now().In(location)
	day := now.Format("2006-01-02")
	cache := m.traffic[id]
	if cache != nil && cache.day == day && time.Since(cache.checked) < time.Minute {
		return cache.bytes, cache.reason
	}
	if cache == nil || cache.day != day {
		cache = &siteTraffic{day: day, files: map[string]*trafficFile{}}
		m.traffic[id] = cache
	}
	cache.checked = now
	cache.bytes = nil
	cache.reason = ""
	paths, err := filepath.Glob(siteLogPath(name, "access") + "*")
	if err != nil {
		return nil, "访问日志路径不可用"
	}
	// 自带logrotate使用copytruncate：轮转副本改变后须重建，不能把已重长的当前文件当成追加。
	rotated := len(paths) != len(cache.files)
	if !rotated {
		for _, path := range paths {
			if path == siteLogPath(name, "access") {
				continue
			}
			info, statErr := os.Lstat(path)
			old := cache.files[path]
			if statErr != nil || old == nil || !os.SameFile(info, old.info) || info.Size() != old.info.Size() || info.ModTime() != old.info.ModTime() {
				rotated = true
				break
			}
		}
	}
	if rotated {
		cache.files = map[string]*trafficFile{}
	}
	var total int64
	active := map[string]*trafficFile{}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			cache.reason = "部分访问日志不可读"
			return nil, cache.reason
		}
		previous := cache.files[path]
		compressed := strings.HasSuffix(path, ".gz")
		unchanged := previous != nil && os.SameFile(info, previous.info) && info.Size() == previous.info.Size() && info.ModTime() == previous.info.ModTime()
		if unchanged {
			active[path] = previous
			total += previous.bytes
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			cache.reason = "访问日志读取失败"
			return nil, cache.reason
		}
		result := &trafficFile{info: info}
		var reader io.Reader = file
		var zipped *gzip.Reader
		if compressed {
			zipped, err = gzip.NewReader(file)
			if err == nil {
				reader = zipped
			}
		} else if previous != nil && os.SameFile(info, previous.info) && info.Size() >= previous.offset {
			result.offset = previous.offset
			result.bytes = previous.bytes
			_, err = file.Seek(result.offset, io.SeekStart)
		}
		if err == nil {
			buffered := bufio.NewReader(reader)
			for {
				line, readErr := buffered.ReadString('\n')
				if readErr != nil {
					if readErr != io.EOF {
						err = readErr
					}
					break
				}
				result.offset += int64(len(line))
				match := accessBytes.FindStringSubmatch(line)
				if match == nil {
					err = errors.New("访问日志格式不是Nginx combined格式")
					break
				}
				stamp, parseErr := time.Parse("02/Jan/2006:15:04:05 -0700", match[1])
				if parseErr != nil {
					err = parseErr
					break
				}
				if stamp.In(location).Format("2006-01-02") == day && match[2] != "-" {
					amount, parseErr := strconv.ParseInt(match[2], 10, 64)
					if parseErr != nil {
						err = parseErr
						break
					}
					result.bytes += amount
				}
			}
		}
		if zipped != nil {
			zipped.Close()
		}
		file.Close()
		if err != nil {
			cache.reason = "访问日志无法解析，请检查日志格式或文件权限"
			return nil, cache.reason
		}
		active[path] = result
		total += result.bytes
	}
	cache.files = active
	cache.bytes = &total
	return cache.bytes, ""
}
