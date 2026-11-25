package alerts

import (
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type RuleInput struct {
	Title           string   `json:"title"`
	Enabled         bool     `json:"enabled"`
	Kind            string   `json:"kind"`
	Target          string   `json:"target"`
	ChannelIDs      []string `json:"channelIds" gorm:"-"`
	IntervalSeconds int      `json:"intervalSeconds"`
	MaxDaily        int      `json:"maxDaily"`
	Days            int      `json:"days"`
}

type rule struct {
	RuleInput     `gorm:"embedded"`
	ID            string     `json:"id" gorm:"primaryKey"`
	TargetName    string     `json:"targetName"`
	State         string     `json:"state"`
	LastCheckedAt *time.Time `json:"lastCheckedAt,omitempty"`
	NextCheckAt   time.Time  `json:"-"`
	SentDate      string     `json:"-"`
	SentToday     int        `json:"-"`
	CreatedAt     time.Time  `json:"-"`
	UpdatedAt     time.Time  `json:"-"`
}

func (rule) TableName() string { return "alert_rules" }

type ruleChannel struct {
	RuleID    string `gorm:"primaryKey"`
	ChannelID string `gorm:"primaryKey"`
}

func (ruleChannel) TableName() string { return "alert_rule_channels" }

func (in *RuleInput) validate() error {
	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" || utf8.RuneCountInString(in.Title) > 64 || strings.IndexFunc(in.Title, unicode.IsControl) >= 0 {
		return errors.New("请输入 1 至 64 个字符的单行标题")
	}
	if !slices.Contains([]string{"service", "project", "certificate"}, in.Kind) || in.Target == "" || len(in.Target) > 128 {
		return errors.New("请选择有效的告警对象")
	}
	if in.IntervalSeconds < 60 || in.IntervalSeconds > 86400 {
		return errors.New("检测间隔须在 60 至 86400 秒之间")
	}
	if in.MaxDaily < 1 || in.MaxDaily > 100 {
		return errors.New("每日通知次数须在 1 至 100 次之间")
	}
	if in.Kind == "certificate" {
		if in.Days < 1 || in.Days > 365 {
			return errors.New("证书提前提醒时间须在 1 至 365 天之间")
		}
	} else {
		in.Days = 0
	}
	if len(in.ChannelIDs) == 0 {
		return errors.New("至少选择一个告警通道")
	}
	slices.Sort(in.ChannelIDs)
	in.ChannelIDs = slices.Compact(in.ChannelIDs)
	return nil
}

func (m *Module) listRules(c *gin.Context) {
	rows := []rule{}
	var links []ruleChannel
	db := m.db.WithContext(c.Request.Context())
	if err := db.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	if err := db.Order("channel_id").Find(&links).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	channels := make(map[string][]string)
	for _, link := range links {
		channels[link.RuleID] = append(channels[link.RuleID], link.ChannelID)
	}
	for i := range rows {
		rows[i].ChannelIDs = channels[rows[i].ID]
	}
	response.OK(c, gin.H{"list": rows})
}

var errRuleChannel = errors.New("某个告警通道已不存在")

func (m *Module) saveRule(c *gin.Context) {
	ctx := c.Request.Context()
	var in RuleInput
	if !response.Bind(c, &in) {
		return
	}
	if err := in.validate(); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	name, _, err := m.checkTarget(ctx, in, time.Now())
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	row := rule{RuleInput: in, ID: c.Param("id"), TargetName: name, State: "pending", NextCheckAt: time.Now().UTC()}
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&channel{}).Where("id IN ?", in.ChannelIDs).Count(&count).Error; err != nil {
			return err
		}
		if count != int64(len(in.ChannelIDs)) {
			return errRuleChannel
		}
		if row.ID == "" {
			row.ID = store.ID()
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		} else {
			changed := tx.Model(&rule{}).Where("id = ?", row.ID).Updates(map[string]any{
				"title": in.Title, "enabled": in.Enabled, "kind": in.Kind, "target": in.Target, "target_name": name,
				"interval_seconds": in.IntervalSeconds, "max_daily": in.MaxDaily, "days": in.Days,
				"state": row.State, "last_checked_at": nil, "next_check_at": row.NextCheckAt,
			})
			if changed.Error != nil {
				return changed.Error
			}
			if changed.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			if err := tx.Where("rule_id = ?", row.ID).Delete(&ruleChannel{}).Error; err != nil {
				return err
			}
		}
		links := make([]ruleChannel, 0, len(in.ChannelIDs))
		for _, id := range in.ChannelIDs {
			links = append(links, ruleChannel{RuleID: row.ID, ChannelID: id})
		}
		return tx.Create(&links).Error
	})
	switch {
	case errors.Is(err, errRuleChannel):
		response.Fail(c, response.Invalid, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "告警任务不存在")
	case err != nil:
		response.Fail(c, response.Internal, "")
	default:
		response.OK(c, row)
	}
}

func (m *Module) setRuleEnabled(c *gin.Context) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Enabled == nil {
		response.Fail(c, response.Invalid, "请选择启用状态")
		return
	}
	changed := m.db.WithContext(c.Request.Context()).Model(&rule{}).Where("id = ?", c.Param("id")).Updates(map[string]any{
		"enabled": *in.Enabled, "state": "pending", "last_checked_at": nil, "next_check_at": time.Now().UTC(),
	})
	if changed.Error != nil {
		response.Fail(c, response.Internal, "")
	} else if changed.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "告警任务不存在")
	} else {
		response.OK(c, nil)
	}
}

func (m *Module) removeRule(c *gin.Context) {
	result := m.db.WithContext(c.Request.Context()).Where("id = ?", c.Param("id")).Delete(&rule{})
	if result.Error != nil {
		response.Fail(c, response.Internal, "")
	} else if result.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "告警任务不存在")
	} else {
		response.OK(c, nil)
	}
}
