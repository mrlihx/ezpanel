package website

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

var statisticsLine = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "([^"]*)" ([0-9]{3}) ([0-9]+|-)`)

type visits struct {
	Day      string `json:"day"`
	Requests int    `json:"requests"`
	Errors   int    `json:"errors"`
	Bytes    int64  `json:"bytes"`
}

func (m *Module) statistics(c *gin.Context) {
	days, err := strconv.Atoi(c.DefaultQuery("days", "7"))
	if err != nil || (days != 1 && days != 7 && days != 30) {
		response.Fail(c, response.Invalid, "请选择今天、近 7 天或近 30 天")
		return
	}
	row, ok := m.row(c)
	if !ok {
		return
	}
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		failure(c, err)
		return
	}
	now := time.Now().In(location)
	start := time.Date(now.Year(), now.Month(), now.Day()-days+1, 0, 0, 0, 0, location)
	points := make([]visits, days)
	positions := map[string]int{}
	for i := range points {
		points[i].Day = start.AddDate(0, 0, i).Format(time.DateOnly)
		positions[points[i].Day] = i
	}
	addresses := map[string]bool{}
	paths := map[string]int{}
	files, err := filepath.Glob(siteLogPath(siteLogDirectory(row), "access") + "*")
	if err != nil {
		failure(c, err)
		return
	}
	partial := false
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	for _, name := range files {
		if ctx.Err() != nil {
			partial = true
			break
		}
		err := readStatisticsLog(ctx, name, start, func(line string) {
			match := statisticsLine.FindStringSubmatch(line)
			if match == nil {
				partial = true
				return
			}
			stamp, err := time.Parse("02/Jan/2006:15:04:05 -0700", match[2])
			if err != nil {
				return
			}
			idx, ok := positions[stamp.In(location).Format(time.DateOnly)]
			if !ok {
				return
			}
			point := &points[idx]
			point.Requests++
			if match[4][0] == '4' || match[4][0] == '5' {
				point.Errors++
			}
			amount, _ := strconv.ParseInt(match[5], 10, 64)
			point.Bytes += amount
			addresses[match[1]] = true
			if parts := strings.Fields(match[3]); len(parts) == 3 {
				path, _, _ := strings.Cut(parts[1], "?")
				paths[path]++
			}
		})
		partial = partial || err != nil
	}
	type visitPath struct {
		Path     string `json:"path"`
		Requests int    `json:"requests"`
	}
	top := make([]visitPath, 0, len(paths))
	for path, requests := range paths {
		top = append(top, visitPath{path, requests})
	}
	slices.SortFunc(top, func(a, b visitPath) int {
		if a.Requests == b.Requests {
			return strings.Compare(a.Path, b.Path)
		}
		return b.Requests - a.Requests
	})
	total := visits{}
	for _, point := range points {
		total.Requests += point.Requests
		total.Errors += point.Errors
		total.Bytes += point.Bytes
	}
	response.OK(c, gin.H{"total": total, "uniqueIPs": len(addresses), "days": points, "topPaths": top[:min(20, len(top))], "partial": partial})
}

func readStatisticsLog(ctx context.Context, name string, start time.Time, consume func(string)) error {
	file, err := os.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("访问日志必须为普通文件")
	}
	if info.ModTime().Before(start) {
		return nil
	}
	var reader io.Reader = file
	if strings.HasSuffix(name, ".gz") {
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	}
	scan := bufio.NewScanner(reader)
	scan.Buffer(make([]byte, 4096), 256<<10)
	for scan.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		consume(scan.Text())
	}
	return scan.Err()
}
