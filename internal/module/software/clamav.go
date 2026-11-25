package software

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const clamAVKey = "software.clamav"
const clamAVLock = "software-install:clamav"

type clamTarget struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Path      string `json:"path"`
	Policy    string `json:"policy"`
	Remark    string `json:"remark"`
	Scheduled bool   `json:"scheduled"`
}

type clamConfig struct {
	Mode    string       `json:"mode"`
	Auto    bool         `json:"auto"`
	Hour    int          `json:"hour"`
	Minute  int          `json:"minute"`
	Targets []clamTarget `json:"targets"`
}

func (m *Module) clamRoutes(r *gin.RouterGroup) {
	r.GET("/clamav/visual", m.clamVisual)
	r.PUT("/clamav/visual", m.clamVisual)
	r.POST("/clamav/scan", m.scanClamAV)
	r.POST("/clamav/signatures", m.clamUpdate)
	r.GET("/clamav/history", m.clamHistory)
	r.GET("/clamav/quarantine", m.clamQuarantine)
	r.POST("/clamav/quarantine", m.clamQuarantineAction)
}

func (m *Module) clamSettings() (clamConfig, error) {
	out := clamConfig{Mode: "normal", Targets: []clamTarget{}}
	var row store.Setting
	err := m.db.First(&row, "key = ?", clamAVKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal([]byte(row.Value), &out)
}

func (m *Module) clamVisual(c *gin.Context) {
	settings, err := m.clamSettings()
	if err != nil {
		fail(c, err)
		return
	}
	ctx := c.Request.Context()
	if c.Request.Method == "PUT" {
		var in clamConfig
		if !response.Bind(c, &in) {
			return
		}
		if (in.Mode != "normal" && in.Mode != "quick") || in.Hour < 0 || in.Hour > 23 || in.Minute < 0 || in.Minute > 59 || len(in.Targets) > 100 {
			response.Fail(c, response.Invalid, "扫描配置无效")
			return
		}
		release, err := m.tasks.Acquire(clamAVLock)
		if err != nil {
			fail(c, err)
			return
		}
		defer release()
		ids := map[string]bool{}
		for i := range in.Targets {
			row := &in.Targets[i]
			row.Name = strings.TrimSpace(row.Name)
			if row.Name == "" || len(row.Name) > 256 || len(row.Remark) > 1024 ||
				(row.Policy != "report" && row.Policy != "quarantine") {
				response.Fail(c, response.Invalid, "检测目录名称或感染文件策略无效")
				return
			}
			if row.ID == "" {
				row.ID = store.ID()
			}
			if !clamID.MatchString(row.ID) || ids[row.ID] {
				response.Fail(c, response.Invalid, "检测目录标识无效或重复")
				return
			}
			ids[row.ID] = true
			if _, err := m.scanDirectory(row.Path); err != nil {
				response.Fail(c, response.Invalid, err.Error())
				return
			}
		}
		if in.Mode != settings.Mode {
			state, err := m.packages.Status(ctx, "clamav")
			if err == nil && state.Running != nil && *state.Running != (in.Mode == "quick") {
				action := "stop"
				if in.Mode == "quick" {
					action = "start"
				}
				_, err = m.packages.Service(ctx, "clamav", action)
			}
			if err != nil {
				response.Fail(c, response.System, response.Summary(err.Error()))
				return
			}
		}
		encoded, _ := json.Marshal(in)
		if err := m.db.Save(&store.Setting{Key: clamAVKey, Value: string(encoded)}).Error; err != nil {
			fail(c, err)
			return
		}
		settings = in
	}
	response.OK(c, gin.H{"settings": settings, "status": m.packages.ClamAVInfo(ctx), "siteRoot": m.cfg.Current().SiteRoot})
}

func (m *Module) scanDirectory(raw string) (string, error) {
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", err
	}
	real, err := policy.Check(raw, false)
	if err != nil || policy.CheckTree(real) != nil {
		return "", errors.New("检测目录无效或受到保护")
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return "", errors.New("检测目录必须是已存在的目录")
	}
	return real, nil
}

func (m *Module) scanClamAV(c *gin.Context) {
	settings, err := m.clamSettings()
	if err != nil {
		fail(c, err)
		return
	}
	target := clamTarget{Name: "网站目录", Path: m.cfg.Current().SiteRoot, Policy: "report"}
	if id := c.Query("id"); id != "" {
		target = clamTarget{}
		for _, row := range settings.Targets {
			if row.ID == id {
				target = row
				break
			}
		}
		if target.ID == "" {
			response.Fail(c, response.NotFound, "检测目录不存在")
			return
		}
	}
	id, err := m.submitClamScan(settings.Mode, []clamTarget{target}, auth.Current(c).User.Username, m.auth.ClientIP(c))
	m.clamTaskResult(c, id, err)
}

func (m *Module) submitClamScan(mode string, targets []clamTarget, user, ip string) (string, error) {
	name := "ClamAV 自动检测"
	if user != "system" {
		name = "ClamAV " + targets[0].Name
	}
	return m.tasks.Submit(task.Meta{Module: "software", Action: "scan", Target: name, ResourceID: "clamav", LockKey: clamAVLock, Username: user, IP: ip}, func(ctx context.Context, log *task.Log) (any, error) {
		infected := false
		total := 0
		var resultErr error
		for _, target := range targets {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			found, count, err := m.runClamScan(ctx, mode, target, log)
			infected = infected || found
			total += count
			resultErr = errors.Join(resultErr, err)
		}
		return gin.H{"infected": infected, "quarantined": total}, resultErr
	})
}

func (m *Module) clamUpdate(c *gin.Context) {
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "software", Action: "signatures", Target: "ClamAV 病毒库更新", ResourceID: "clamav", LockKey: clamAVLock, Username: session.User.Username, IP: m.auth.ClientIP(c)}, func(ctx context.Context, log *task.Log) (any, error) {
		return nil, m.packages.UpdateClamAVSignatures(ctx, log.Line)
	})
	m.clamTaskResult(c, id, err)
}

func (m *Module) clamTaskResult(c *gin.Context, id string, err error) {
	if err != nil {
		fail(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}

func (m *Module) clamHistory(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	query := m.db.Model(&store.Task{}).Where("module = ? AND action = ? AND resource_id = ?", "software", "scan", "clamav")
	var rows []store.Task
	var total int64
	if query.Count(&total).Error != nil || query.Order("started_at DESC,id DESC").Offset((page-1)*size).Limit(size).Find(&rows).Error != nil {
		response.Fail(c, response.System, "读取检测记录失败")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		item := task.Summary(row)
		if row.Result != nil {
			var result map[string]any
			if json.Unmarshal([]byte(*row.Result), &result) == nil {
				item["infected"], item["quarantined"] = result["infected"], result["quarantined"]
			}
		}
		items = append(items, item)
	}
	response.OK(c, gin.H{"list": items, "total": total})
}

func (m *Module) RunClamSchedule(ctx context.Context) {
	loc, err := time.LoadLocation(m.cfg.Timezone)
	if err != nil {
		return
	}
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			now = now.In(loc)
			day := now.Format(time.DateOnly)
			if day == last {
				continue
			}
			config, err := m.clamSettings()
			if err != nil || !config.Auto || config.Hour != now.Hour() || config.Minute != now.Minute() {
				continue
			}
			var targets []clamTarget
			for _, row := range config.Targets {
				if row.Scheduled {
					targets = append(targets, row)
				}
			}
			if len(targets) == 0 {
				continue
			}
			state, err := m.packages.Status(ctx, "clamav")
			if err != nil || !state.Installed || !state.Manageable {
				continue
			}
			if _, err := m.submitClamScan(config.Mode, targets, "system", ""); err == nil {
				last = day
			}
		}
	}
}

func (m *Module) runClamScan(ctx context.Context, mode string, target clamTarget, log *task.Log) (bool, int, error) {
	root, err := m.scanDirectory(target.Path)
	if err != nil {
		return false, 0, err
	}
	log.Line("开始检测 " + target.Name + " " + root)
	var files []string
	found, err := m.packages.ScanClamAVDirectory(ctx, root, mode == "quick", func(line string) {
		log.Line(line)
		if prefix, ok := strings.CutSuffix(line, " FOUND"); ok && target.Policy == "quarantine" {
			if index := strings.LastIndex(prefix, ": "); index >= 0 && pathsafe.Contains(root, prefix[:index]) {
				files = append(files, prefix[:index])
			}
		}
	})
	count := 0
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return found, count, err
		}
		if moveErr := m.quarantineFile(file, root); moveErr != nil {
			log.Line("隔离失败 " + file + " " + moveErr.Error())
			err = errors.Join(err, moveErr)
		} else {
			count++
			log.Line("已隔离 " + file)
		}
	}
	log.Line(fmt.Sprintf("检测结束 %s，发现感染 %t，隔离 %d 个文件", target.Name, found, count))
	return found, count, err
}
