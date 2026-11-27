package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"

	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"
	"gorm.io/gorm"
)

const securityPolicySettingKey = "panel.security_policy"

var allowedUnauthStatuses = []int{200, 401, 403, 404, 444, 500, 502, 503}

type SecurityPolicy struct {
	UserAgentEnabled bool   `json:"userAgentEnabled"`
	UserAgentAllow   string `json:"userAgentAllow"`
	UnauthStatus     int    `json:"unauthStatus"`
	UnauthHTML       string `json:"unauthHtml"`
}

func defaultSecurityPolicy() SecurityPolicy {
	return SecurityPolicy{UnauthStatus: http.StatusNotFound}
}

func parseUserAgentTokens(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func matchesUserAgent(ua, allow string) bool {
	tokens := parseUserAgentTokens(allow)
	if len(tokens) == 0 {
		return true
	}
	lowerUA := strings.ToLower(ua)
	for _, token := range tokens {
		if strings.Contains(lowerUA, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

// ReadSecurityPolicy 供面板和只读就绪检查使用，避免探针绕过入口规则。
func ReadSecurityPolicy(ctx context.Context, db *sql.DB) (SecurityPolicy, error) {
	policy := defaultSecurityPolicy()
	var raw string
	err := db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", securityPolicySettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return policy, nil
	}
	if err != nil {
		return policy, err
	}
	if err := json.Unmarshal([]byte(raw), &policy); err != nil {
		return defaultSecurityPolicy(), err
	}
	if !slices.Contains(allowedUnauthStatuses, policy.UnauthStatus) {
		policy.UnauthStatus = http.StatusNotFound
	}
	return policy, nil
}

func (p SecurityPolicy) ProbeUserAgent() string {
	if p.UserAgentEnabled {
		for _, token := range parseUserAgentTokens(p.UserAgentAllow) {
			if httpguts.ValidHeaderFieldValue(token) {
				return token
			}
		}
	}
	return ""
}

func (s *Service) SecurityPolicy() SecurityPolicy {
	if s == nil {
		return defaultSecurityPolicy()
	}
	s.policyMu.RLock()
	if s.policyLoaded {
		p := s.policyCache
		s.policyMu.RUnlock()
		return p
	}
	s.policyMu.RUnlock()

	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	if s.policyLoaded {
		return s.policyCache
	}
	policy := defaultSecurityPolicy()
	if s.db != nil {
		db, err := s.db.DB()
		if err != nil {
			return policy
		}
		policy, err = ReadSecurityPolicy(context.Background(), db)
		if err != nil {
			return policy
		}
	}
	s.policyCache = policy
	s.policyLoaded = true
	return policy
}

func (s *Service) SaveSecurityPolicy(ctx context.Context, currentUA string, policy SecurityPolicy) error {
	return s.ApplySecurityPolicy(ctx, currentUA, policy, false)
}

func (s *Service) ApplySecurityPolicy(ctx context.Context, currentUA string, policy SecurityPolicy, refreshSessions bool) error {
	policy.UserAgentAllow = strings.TrimSpace(policy.UserAgentAllow)
	if len(policy.UserAgentAllow) > 2048 {
		return errors.New("User-Agent 限制规则过长（最多 2048 字符）")
	}
	if policy.UnauthStatus == 0 {
		policy.UnauthStatus = http.StatusNotFound
	}
	if !slices.Contains(allowedUnauthStatuses, policy.UnauthStatus) {
		return errors.New("不支持的未认证响应状态码")
	}
	if len(policy.UnauthHTML) > 64<<10 {
		return errors.New("自定义未认证 HTML 不能超过 64KiB")
	}
	if policy.UserAgentEnabled && policy.UserAgentAllow != "" && !matchesUserAgent(currentUA, policy.UserAgentAllow) {
		return errors.New("当前浏览器 User-Agent 不在允许列表中，为防止锁定面板已拒绝保存")
	}
	raw, _ := json.Marshal(policy)
	if s.db != nil {
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Save(&store.Setting{Key: securityPolicySettingKey, Value: string(raw)}).Error; err != nil {
				return err
			}
			if refreshSessions {
				if err := tx.Model(&store.User{}).Where("id > 0").Update("auth_version", gorm.Expr("auth_version + 1")).Error; err != nil {
					return err
				}
				return tx.Where("user_id > 0").Delete(&store.AuthSession{}).Error
			}
			return nil
		}); err != nil {
			return errors.New("无法保存安全访问策略")
		}
	}
	s.policyMu.Lock()
	s.policyCache = policy
	s.policyLoaded = true
	s.policyMu.Unlock()
	return nil
}

func (s *Service) abortUnauthenticated(c *gin.Context, policy SecurityPolicy) {
	status := policy.UnauthStatus
	if !slices.Contains(allowedUnauthStatuses, status) {
		status = http.StatusNotFound
	}
	if status == 444 {
		if hj, ok := c.Writer.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				c.Abort()
				return
			}
		}
		c.AbortWithStatus(444)
		return
	}
	if strings.TrimSpace(policy.UnauthHTML) != "" {
		c.Data(status, "text/html; charset=utf-8", []byte(policy.UnauthHTML))
		c.Abort()
		return
	}
	c.AbortWithStatus(status)
}
