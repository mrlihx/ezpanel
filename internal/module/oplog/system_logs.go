package oplog

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/geoip"
	"ezpanel/internal/logview"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) logRoutes(g *gin.RouterGroup) {
	r := g.Group("/api/logs")
	r.GET("/sites", m.sites)
	r.GET("/runtime", m.runtimeLog)
	r.DELETE("/runtime", m.clearRuntime)
	r.GET("/ssh", m.sshLog)
	r.GET("/crons/:id", m.cronLog)
	r.GET("/apps/:app", m.appSources)
	r.GET("/apps/:app/targets", m.appLogTargets)
	r.GET("/apps/:app/:source", m.appLog)
	r.DELETE("/apps/:app/:source", m.appLog)
	r.GET("/apps/:app/:source/download", m.appLog)
}
func (m *Module) runtimeLog(c *gin.Context) {
	text, err := logview.Read(filepath.Join(m.cfg.DataDir(), "logs/runtime.log"))
	if err != nil {
		response.Fail(c, response.System, "面板运行日志读取失败")
		return
	}
	response.OK(c, text)
}
func (m *Module) clearRuntime(c *gin.Context) {
	if err := m.runtime.Clear(); err != nil {
		response.Fail(c, response.System, "面板运行日志清理失败")
		return
	}
	response.OK(c, nil)
}
func (m *Module) appSources(c *gin.Context) {
	sources, err := m.appLogSources(c)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, sources)
}
func (m *Module) appLog(c *gin.Context) {
	sources, err := m.appLogSources(c)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var source *pkgmgr.LogSource
	for i := range sources {
		if sources[i].ID == c.Param("source") {
			source = &sources[i]
			break
		}
	}
	if source == nil {
		response.Fail(c, response.NotFound, "应用未安装或日志类型不可用")
		return
	}
	if c.Request.Method == "DELETE" {
		if !source.Clearable {
			response.Fail(c, response.Invalid, "运行日志由所属服务管理，不能单独清空")
			return
		}
		if err := logview.Clear(source.Path); err != nil {
			response.Fail(c, response.System, "应用日志清理失败")
			return
		}
		response.OK(c, nil)
		return
	}
	text := logview.Text{Path: source.Path, Clearable: source.Clearable}
	if source.Container != "" {
		result, e := m.packages.ReadServiceOutput(c.Request.Context(), *source)
		if e != nil {
			response.Fail(c, response.System, "容器日志读取失败")
			return
		}
		text.Content = result.Stdout + result.Stderr
		text.Truncated = result.Truncated
		text.Size = int64(len(text.Content))
	} else if source.Unit != "" {
		result, e := executor.Run(c.Request.Context(), 15*time.Second, "/usr/bin/journalctl", "--unit", source.Unit, "--lines=1000", "--no-pager", "--output=short-iso")
		if e != nil {
			response.Fail(c, response.System, "服务日志读取失败")
			return
		}
		text.Content = result.Stdout
		text.Truncated = result.Truncated
		text.Size = int64(len(result.Stdout))
	} else if !strings.HasSuffix(c.FullPath(), "/download") {
		text, err = logview.Read(source.Path)
		if err != nil {
			response.Fail(c, response.System, "应用日志读取失败")
			return
		}
	}
	if strings.HasSuffix(c.FullPath(), "/download") {
		name := c.Param("app") + "-" + source.ID + ".log"
		if source.Unit != "" || source.Container != "" {
			c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
			c.Header("Content-Type", "text/plain; charset=utf-8")
			c.String(http.StatusOK, "%s", text.Content)
			return
		}
		f, e := logview.Open(source.Path, false)
		if e != nil {
			response.Fail(c, response.NotFound, "日志尚未生成")
			return
		}
		defer f.Close()
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, f)
		return
	}
	response.OK(c, text)
}

type sshEntry struct {
	ID        int       `json:"id"`
	IP        string    `json:"ip"`
	Address   string    `json:"address"`
	Location  string    `json:"location"`
	Username  string    `json:"username"`
	Success   bool      `json:"success"`
	CreatedAt time.Time `json:"createdAt"`
}

var sshPattern = regexp.MustCompile(`(?:^|\s)(Accepted|Failed) \S+ for (?:invalid user )?(\S+) from (\S+) port (\d+)`)

func (m *Module) sshLog(c *gin.Context) {
	// 只读取sshd相关记录，最多5000条；不查询外部IP归属服务。
	result, err := executor.Run(c.Request.Context(), 15*time.Second, "/usr/bin/journalctl", "--identifier=sshd", "--identifier=sshd-session", "--lines=5000", "--reverse", "--no-pager", "--output=json", "--output-fields=MESSAGE,__REALTIME_TIMESTAMP")
	if err != nil {
		response.Fail(c, response.System, "SSH登录日志读取失败，请检查systemd日志服务")
		return
	}
	rows := []sshEntry{}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	for {
		var item struct {
			Message json.RawMessage `json:"MESSAGE"`
			Stamp   string          `json:"__REALTIME_TIMESTAMP"`
		}
		if err := decoder.Decode(&item); err != nil {
			if !errors.Is(err, io.EOF) && !result.Truncated {
				response.Fail(c, response.System, "SSH日志格式无法读取")
				return
			}
			break
		}
		var message string
		if len(item.Message) > 0 {
			if err := json.Unmarshal(item.Message, &message); err != nil {
				var rawBytes []byte
				if json.Unmarshal(item.Message, &rawBytes) == nil {
					message = string(rawBytes)
				}
			}
		}
		match := sshPattern.FindStringSubmatch(message)
		if match == nil {
			continue
		}
		stamp, err := strconv.ParseInt(item.Stamp, 10, 64)
		if err != nil {
			continue
		}
		location := geoip.Location(match[3])
		if location == "" {
			location = "—"
		}
		rows = append(rows, sshEntry{ID: len(rows), IP: match[3], Address: net.JoinHostPort(match[3], match[4]), Location: location, Username: match[2], Success: match[1] == "Accepted", CreatedAt: time.UnixMicro(stamp).UTC()})
	}
	response.OK(c, gin.H{"list": rows, "truncated": result.Truncated, "scope": "最近5000条sshd记录中的登录事件"})
}

func (m *Module) cronLog(c *gin.Context) {
	var row store.Task
	q := m.db.Where("cron_job_id = ?", c.Param("id"))
	if run := c.Query("runId"); run != "" {
		q = q.Where("id = ?", run)
	}
	err := q.Order("started_at DESC,id DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.OK(c, nil)
		return
	}
	if err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	text, err := task.ReadLogTail(row, 1<<20)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		response.Fail(c, response.System, "任务日志读取失败")
		return
	}
	text.Truncated = text.Truncated || row.LogTruncated
	response.OK(c, gin.H{"text": text, "run": task.Summary(row)})
}

func (m *Module) sites(c *gin.Context) {
	var rows []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		LogDirectory string `json:"logDirectory"`
	}
	if err := m.db.Model(&store.Website{}).Select("id,name,log_directory").Order("name,id").Scan(&rows).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	for i := range rows {
		if rows[i].LogDirectory == "" {
			rows[i].LogDirectory = rows[i].ID
		}
		rows[i].LogDirectory = filepath.Join(pkgmgr.WebsiteLogRoot, rows[i].LogDirectory)
	}
	response.OK(c, rows)
}
