package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ssoPath = "/auth-sso/"
const ssoCallbackPath = ssoPath + "callback"
const ssoSettingKey = "auth.sso.config"
const ssoCookie = "panel_sso"

type ssoConfig struct {
	Enabled      bool   `json:"enabled"`
	PluginID     string `json:"pluginId"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Subject      string `json:"subject"`
	UserID       uint   `json:"-"`
}

type ssoFlow struct {
	StateHash  string `gorm:"primaryKey"`
	Nonce      string
	Verifier   string
	ConfigHash string
	ExpiresAt  int64
}

func (ssoFlow) TableName() string { return "auth_sso_flows" }

func setSSOCookie(w http.ResponseWriter, state string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: state, Path: ssoPath, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}

func (s *Service) SetSSOPlugin(resolve func(string) (string, bool)) { s.ssoPlugin = resolve }

func (s *Service) ssoSettings(ctx context.Context) (ssoConfig, string, error) {
	var cfg ssoConfig
	var setting store.Setting
	err := s.db.WithContext(ctx).Take(&setting, "key = ?", ssoSettingKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return cfg, "", nil
	}
	if err != nil {
		return cfg, "", err
	}
	value, err := store.Decrypt(s.cfg.AESKey, setting.Value)
	if err == nil {
		var data struct {
			ssoConfig
			User uint
		}
		err = json.Unmarshal([]byte(value), &data)
		cfg, cfg.UserID = data.ssoConfig, data.User
	}
	return cfg, tokenHash(setting.Value), err
}

func (s *Service) branding(c *gin.Context) {
	data := gin.H{"alias": s.cfg.Current().Alias}
	if cfg, _, err := s.ssoSettings(c.Request.Context()); err == nil && cfg.Enabled && s.ssoPlugin != nil {
		if title, available := s.ssoPlugin(cfg.PluginID); available {
			data["ssoTitle"] = title
		}
	}
	if totpCfg, err := s.totpSettings(c.Request.Context()); err == nil && totpCfg.Enabled {
		data["totpEnabled"] = true
	}
	response.OK(c, data)
}

func (s *Service) getSSOConfig(c *gin.Context) {
	cfg, _, err := s.ssoSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取单点登录设置")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"enabled": cfg.Enabled, "pluginId": cfg.PluginID, "issuer": cfg.Issuer, "clientId": cfg.ClientID,
		"subject": cfg.Subject, "secretSet": cfg.ClientSecret != "", "callback": s.RequestOrigin(c).String() + ssoCallbackPath})
}

func (s *Service) saveSSOConfig(c *gin.Context) {
	var in struct {
		ssoConfig
	}
	if !response.Bind(c, &in) {
		return
	}
	user := Current(c).User
	cfg := in.ssoConfig
	cfg.UserID = user.ID
	if cfg.ClientSecret == "" {
		old, _, err := s.ssoSettings(c.Request.Context())
		if err != nil {
			response.Fail(c, response.System, "无法读取单点登录设置")
			return
		}
		if old.Issuer == cfg.Issuer && old.ClientID == cfg.ClientID && old.PluginID == cfg.PluginID {
			cfg.ClientSecret = old.ClientSecret
		}
	}
	if cfg.Enabled {
		if s.ssoPlugin == nil {
			response.Fail(c, response.Invalid, "没有可用的单点登录插件")
			return
		}
		_, available := s.ssoPlugin(cfg.PluginID)
		issuer, _ := url.Parse(cfg.Issuer)
		if !available || issuer == nil || issuer.RawQuery != "" || issuer.ForceQuery || strings.HasSuffix(cfg.Issuer, "/") ||
			validateOIDCEndpoint(cfg.Issuer, cfg.Issuer) != nil || cfg.ClientID == "" || cfg.Subject == "" || cfg.ClientSecret == "" ||
			len(cfg.ClientID) > 256 || len(cfg.Subject) > 255 || len(cfg.ClientSecret) > 4096 || s.RequestOrigin(c).Scheme != "https" {
			response.Fail(c, response.Invalid, "请使用 HTTPS 面板地址，选择已启用插件并填写有效的 OIDC 参数和账号 Subject")
			return
		}
	}
	raw, _ := json.Marshal(struct {
		ssoConfig
		User uint
	}{cfg, cfg.UserID})
	secret, err := store.Encrypt(s.cfg.AESKey, string(raw))
	if err == nil {
		err = s.db.WithContext(c.Request.Context()).Save(&store.Setting{Key: ssoSettingKey, Value: secret}).Error
	}
	if err != nil {
		response.Fail(c, response.System, "无法保存单点登录设置")
		return
	}
	s.audit(c, "sso-config", true, 0)
	s.getSSOConfig(c)
}

func validateOIDCEndpoint(issuer, target string) error {
	configured, cfgErr := url.ParseRequestURI(issuer)
	u, err := url.Parse(target)
	if cfgErr != nil || err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
		!strings.EqualFold(u.Host, configured.Host) {
		return errors.New("OIDC 端点必须与签发者使用同一 HTTPS 主机")
	}
	return nil
}

func ssoClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				ip = ip.Unmap()
				if !ip.IsGlobalUnicast() || ip.IsPrivate() || netip.MustParsePrefix("100.64.0.0/10").Contains(ip) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
				if err == nil {
					return conn, nil
				}
			}
			return nil, errors.New("OIDC 服务公网地址不可访问")
		},
		DisableKeepAlives: true,
	}
	return &http.Client{Transport: transport, Timeout: 8 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func discoverSSO(ctx context.Context, cfg ssoConfig, callback string) (*oidc.Provider, oauth2.Config, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, err
	}
	oauth := oauth2.Config{ClientID: cfg.ClientID, ClientSecret: cfg.ClientSecret, RedirectURL: callback,
		Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID}}
	var meta struct {
		Keys string `json:"jwks_uri"`
	}
	if err := provider.Claims(&meta); err != nil {
		return nil, oauth2.Config{}, err
	}
	for _, endpoint := range []string{oauth.Endpoint.AuthURL, oauth.Endpoint.TokenURL, meta.Keys} {
		if err := validateOIDCEndpoint(cfg.Issuer, endpoint); err != nil {
			return nil, oauth2.Config{}, err
		}
	}
	return provider, oauth, nil
}

func (s *Service) ssoRequest(c *gin.Context) (ssoConfig, string, string, bool) {
	cfg, digest, err := s.ssoSettings(c.Request.Context())
	origin := s.RequestOrigin(c)
	if err != nil || !cfg.Enabled || s.ssoPlugin == nil || origin.Scheme != "https" || validateOIDCEndpoint(cfg.Issuer, cfg.Issuer) != nil {
		return cfg, "", "", false
	}
	_, available := s.ssoPlugin(cfg.PluginID)
	return cfg, digest, origin.String() + ssoCallbackPath, available
}

func (s *Service) startSSO(c *gin.Context) {
	cfg, digest, callback, ok := s.ssoRequest(c)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}
	ctx := oidc.ClientContext(c.Request.Context(), ssoClient())
	_, oauth, err := discoverSSO(ctx, cfg, callback)
	if err != nil {
		s.ssoLanding(c, false)
		return
	}
	state := store.ID()
	flow := ssoFlow{StateHash: tokenHash(state), Nonce: store.ID(), Verifier: oauth2.GenerateVerifier(), ConfigHash: digest, ExpiresAt: s.now().Add(5 * time.Minute).Unix()}
	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&ssoFlow{}, "expires_at <= ?", s.now().Unix()).Error; err != nil {
			return err
		}
		if previous, _ := c.Cookie(ssoCookie); previous != "" {
			if err := tx.Delete(&ssoFlow{}, "state_hash = ?", tokenHash(previous)).Error; err != nil {
				return err
			}
		}
		return tx.Create(&flow).Error
	})
	if err != nil {
		s.ssoLanding(c, false)
		return
	}
	setSSOCookie(c.Writer, state, 300)
	c.Header("Cache-Control", "no-store")
	c.Redirect(http.StatusFound, oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(flow.Verifier), oidc.Nonce(flow.Nonce)))
}

func acceptsSSOIdentity(cfg ssoConfig, nonce string, token *oidc.IDToken, party string) bool {
	return token.Subject == cfg.Subject && subtle.ConstantTimeCompare([]byte(token.Nonce), []byte(nonce)) == 1 &&
		(party == cfg.ClientID || party == "" && len(token.Audience) == 1)
}

func (s *Service) finishSSO(c *gin.Context) {
	state := c.Query("state")
	cookie, _ := c.Cookie(ssoCookie)
	if len(state) != 32 || subtle.ConstantTimeCompare([]byte(cookie), []byte(state)) != 1 {
		c.Status(http.StatusNotFound)
		return
	}
	setSSOCookie(c.Writer, "", -1)
	cfg, digest, callback, ok := s.ssoRequest(c)
	var flow ssoFlow
	result := s.db.WithContext(c.Request.Context()).Clauses(clause.Returning{}).
		Where("state_hash = ? AND expires_at > ? AND config_hash = ?", tokenHash(state), s.now().Unix(), digest).Delete(&flow)
	code := c.Query("code")
	if !ok || result.Error != nil || result.RowsAffected != 1 || code == "" || len(code) > 4096 {
		s.ssoLanding(c, false)
		return
	}
	ctx := oidc.ClientContext(c.Request.Context(), ssoClient())
	if err := verifySSO(ctx, cfg, callback, code, flow); err != nil {
		s.ssoLanding(c, false)
		return
	}
	_, latest, _, ok := s.ssoRequest(c)
	var user store.User
	if !ok || latest != digest || s.db.First(&user, cfg.UserID).Error != nil {
		s.ssoLanding(c, false)
		return
	}
	session, err := s.Issue(user)
	if err != nil {
		s.ssoLanding(c, false)
		return
	}
	c.Set("panel.session", Session{User: user})
	s.cookie(c, "panel_token", session, int(sessionLifetime/time.Second))
	s.cookie(c, "panel_entry", "", -1)
	s.ssoLanding(c, true)
}

func verifySSO(ctx context.Context, cfg ssoConfig, callback, code string, flow ssoFlow) error {
	provider, oauth, err := discoverSSO(ctx, cfg, callback)
	if err != nil {
		return err
	}
	tokens, err := oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		return err
	}
	raw, _ := tokens.Extra("id_token").(string)
	var claims struct {
		Party string `json:"azp"`
	}
	token, err := provider.VerifierContext(ctx, &oidc.Config{ClientID: cfg.ClientID}).Verify(ctx, raw)
	if err != nil || token.Claims(&claims) != nil || !acceptsSSOIdentity(cfg, flow.Nonce, token, claims.Party) {
		return ErrCredentials
	}
	return nil
}

func (s *Service) ssoLanding(c *gin.Context, success bool) {
	path := "/login?sso=failed"
	code := response.Forbidden
	if success {
		path, code = "/", 0
	}
	s.audit(c, "login-sso", success, code)
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(`<!doctype html><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=`+path+`"><a href="`+path+`">返回面板</a>`))
}
