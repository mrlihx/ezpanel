package alerts

import (
	"ezpanel/internal/config"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Module struct {
	providers    NotificationProviders
	checks       PluginChecks
	db           *gorm.DB
	cfg          config.Config
	packages     *pkgmgr.Manager
	blockedPorts func() []int
}

func New(db *gorm.DB, cfg config.Config, packages *pkgmgr.Manager, blockedPorts func() []int) *Module {
	return &Module{db: db, cfg: cfg, packages: packages, blockedPorts: blockedPorts}
}

func (m *Module) Name() string { return "alerts" }

func (m *Module) Routes(group *gin.RouterGroup) {
	r := group.Group("/api/settings/alerts")
	m.channelRoutes(r)
	r.GET("/rules", m.listRules)
	r.POST("/rules", m.saveRule)
	r.PUT("/rules/:id", m.saveRule)
	r.PATCH("/rules/:id", m.setRuleEnabled)
	r.DELETE("/rules/:id", m.removeRule)
	r.GET("/targets", m.targets)
	r.GET("/logs", m.logs)
}

type deliveryLog struct {
	ID          string    `json:"id" gorm:"primaryKey"`
	RuleID      string    `json:"ruleId"`
	Title       string    `json:"title"`
	ChannelName string    `json:"channelName"`
	Kind        string    `json:"kind"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
}

func (deliveryLog) TableName() string { return "alert_delivery_logs" }

func (m *Module) writeDelivery(ruleID, title string, item channel, sendErr error) error {
	return m.db.Create(deliveryResult(ruleID, title, item, sendErr)).Error
}

func deliveryResult(ruleID, title string, item channel, sendErr error) *deliveryLog {
	row := deliveryLog{ID: store.ID(), RuleID: ruleID, Title: title, ChannelName: item.Name, Kind: item.Kind, Success: sendErr == nil}
	if sendErr != nil {
		row.Error = response.Summary(sendErr.Error())
	}
	return &row
}

func (m *Module) logs(c *gin.Context) {
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	query := m.db.WithContext(c.Request.Context()).Model(&deliveryLog{})
	if id := c.Query("ruleId"); id != "" {
		query = query.Where("rule_id = ?", id)
	}
	switch c.Query("status") {
	case "success":
		query = query.Where("success = ?", true)
	case "failed":
		query = query.Where("success = ?", false)
	case "", "all":
	default:
		response.Fail(c, response.Invalid, "发送状态无效")
		return
	}
	if term := strings.TrimSpace(c.Query("keyword")); term != "" {
		query = query.Where("instr(lower(title), lower(?)) > 0 OR instr(lower(channel_name), lower(?)) > 0 OR instr(lower(error), lower(?)) > 0", term, term, term)
	}
	var count int64
	rows := []deliveryLog{}
	if err := query.Count(&count).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	if int64(page-1) <= count/int64(size) {
		if err := query.Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
	}
	response.OK(c, gin.H{"list": rows, "total": count})
}
