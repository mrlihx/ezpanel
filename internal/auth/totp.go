package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const totpSettingKey = "auth.totp.config"

var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

type totpConfig struct {
	Enabled bool   `json:"enabled"`
	Secret  string `json:"secret"`
	UserID  uint   `json:"userId"`
}

func generateTOTPSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return totpEncoding.EncodeToString(buf), nil
}

func buildOTPAuthURL(issuer, account, secret string) string {
	if strings.TrimSpace(issuer) == "" {
		issuer = "EzPanel"
	}
	label := issuer + ":" + account
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	u := url.URL{
		Scheme:   "otpauth",
		Host:     "totp",
		Path:     "/" + label,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func totpCodeAt(key []byte, counter uint64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	code := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	return fmt.Sprintf("%06d", code%1000000)
}

func verifyTOTPCode(secret, code string, now time.Time) bool {
	_, ok := totpStep(secret, code, now)
	return ok
}

func totpStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return 0, false
	}
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return 0, false
		}
	}
	key, err := totpEncoding.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil || len(key) < 10 {
		return 0, false
	}
	step := now.Unix() / 30
	for delta := int64(-1); delta <= 1; delta++ {
		if step+delta < 0 {
			continue
		}
		expected := totpCodeAt(key, uint64(step+delta))
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			return step + delta, true
		}
	}
	return 0, false
}

func (s *Service) consumeTOTP(ctx context.Context, cfg totpConfig, code string) (bool, error) {
	step, ok := totpStep(cfg.Secret, code, s.now())
	if !ok {
		return false, nil
	}
	result := s.db.WithContext(ctx).Exec(`INSERT INTO auth_totp_uses (user_id, secret_hash, time_step) VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET secret_hash = excluded.secret_hash, time_step = excluded.time_step
		WHERE auth_totp_uses.secret_hash <> excluded.secret_hash OR auth_totp_uses.time_step < excluded.time_step`,
		cfg.UserID, tokenHash(cfg.Secret), step)
	return result.RowsAffected == 1, result.Error
}

func (s *Service) totpSettings(ctx context.Context) (totpConfig, error) {
	var cfg totpConfig
	if s.db == nil {
		return cfg, nil
	}
	var row store.Setting
	err := s.db.WithContext(ctx).Take(&row, "key = ?", totpSettingKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	plain, err := store.Decrypt(s.cfg.AESKey, row.Value)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal([]byte(plain), &cfg)
	return cfg, err
}

func (s *Service) saveTOTPSettings(ctx context.Context, cfg totpConfig) error {
	raw, _ := json.Marshal(cfg)
	cipherText, err := store.Encrypt(s.cfg.AESKey, string(raw))
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Save(&store.Setting{Key: totpSettingKey, Value: cipherText}).Error
}

func (s *Service) getTOTPStatus(c *gin.Context) {
	cfg, err := s.totpSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取双因素认证状态")
		return
	}
	user := Current(c).User
	hasSecret := cfg.Secret != "" && cfg.UserID == user.ID
	response.OK(c, gin.H{"enabled": cfg.Enabled && cfg.UserID == user.ID, "hasSecret": hasSecret})
}

func (s *Service) setupTOTP(c *gin.Context) {
	var in struct {
		Reset bool `json:"reset"`
	}
	if c.Request.ContentLength > 0 && !response.Bind(c, &in) {
		return
	}
	user := Current(c).User
	cfg, err := s.totpSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取双因素认证配置")
		return
	}
	existing := !in.Reset && cfg.Secret != "" && cfg.UserID == user.ID
	secret := cfg.Secret
	if !existing {
		secret, err = generateTOTPSecret()
		if err != nil {
			response.Fail(c, response.System, "无法生成验证密钥")
			return
		}
	}
	ip := ""
	if s.publicIP != nil {
		ip = strings.TrimSpace(s.publicIP(c.Request.Context()))
	}
	if ip == "" {
		if origin := s.RequestOrigin(c); origin != nil {
			ip = origin.Hostname()
		}
	}
	issuer := "EzPanel"
	if ip != "" {
		issuer = "EzPanel - " + ip
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{
		"secret":     secret,
		"otpauthUrl": buildOTPAuthURL(issuer, user.Username, secret),
		"issuer":     issuer,
		"account":    user.Username,
		"existing":   existing,
	})
}

func (s *Service) enableTOTP(c *gin.Context) {
	var in struct {
		Secret string `json:"secret"`
		Code   string `json:"code"`
	}
	if !response.Bind(c, &in) {
		return
	}
	user := Current(c).User
	secret := strings.ToUpper(strings.TrimSpace(in.Secret))
	if !verifyTOTPCode(secret, in.Code, s.now()) {
		s.audit(c, "totp-enable", false, 1001)
		response.Fail(c, response.Invalid, "动态验证码错误，请确认手机时间已同步后重试")
		return
	}
	if err := s.saveTOTPSettings(c.Request.Context(), totpConfig{Enabled: true, Secret: secret, UserID: user.ID}); err != nil {
		response.Fail(c, response.System, "无法保存双因素认证配置")
		return
	}
	s.audit(c, "totp-enable", true, 0)
	response.OK(c, gin.H{"enabled": true})
}

func (s *Service) disableTOTP(c *gin.Context) {
	cfg, err := s.totpSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取双因素认证配置")
		return
	}
	cfg.Enabled = false
	if err := s.saveTOTPSettings(c.Request.Context(), cfg); err != nil {
		response.Fail(c, response.System, "无法关闭双因素认证")
		return
	}
	s.audit(c, "totp-disable", true, 0)
	response.OK(c, gin.H{"enabled": false})
}
