package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"net/url"
	"time"
)

func tokenHash(raw string) string { h := sha256.Sum256([]byte(raw)); return hex.EncodeToString(h[:]) }
func (s *Service) RequestOrigin(c *gin.Context) *url.URL {
	if origin, ok := c.Get("panel.origin"); ok {
		return origin.(*url.URL)
	}
	return s.cfg.Current().PanelOrigin("")
}
func (s *Service) userInfo(u store.User) gin.H {
	c := s.cfg.Current()
	return gin.H{"username": u.Username, "timezone": c.Timezone, "alias": c.Alias, "idleMinutes": c.IdleMinutes, "siteRoot": c.SiteRoot, "backupRoot": c.BackupRoot}
}
func (s *Service) Touch(raw string) bool {
	now := s.now()
	cutoff := now.Add(-time.Duration(s.cfg.Current().IdleMinutes) * time.Minute)
	result := s.db.Model(&store.AuthSession{}).Where("token_hash = ? AND last_active > ? AND expires_at > ?", tokenHash(raw), cutoff, now).Update("last_active", now)
	return result.Error == nil && result.RowsAffected == 1
}
func (s *Service) activity(c *gin.Context) {
	if !s.Touch(Current(c).Token) {
		response.Fail(c, response.Unauthorized, "会话已超时，请重新登录")
		return
	}
	response.OK(c, s.userInfo(Current(c).User))
}
