package settings

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/certificate"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"fmt"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Module struct {
	networkMu       sync.Mutex
	db              *gorm.DB
	cfg             config.Config
	auth            *auth.Service
	applyNetwork    func(config.Config) error
	refreshFirewall func(context.Context) error
	policy          *pathsafe.Policy
}

func New(db *gorm.DB, cfg config.Config, a *auth.Service, apply func(config.Config) error, refreshFirewall func(context.Context) error) (*Module, error) {
	p, e := pathsafe.New(cfg.InstallDir)
	return &Module{db: db, cfg: cfg, auth: a, applyNetwork: apply, refreshFirewall: refreshFirewall, policy: p}, e
}
func (m *Module) Name() string { return "settings" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/settings")
	r.GET("", m.read)
	r.PUT("/basic", m.basic)
	r.PUT("/account", m.account)
	r.PUT("/network", m.network)
	r.GET("/terminal", m.terminal)
	r.PUT("/terminal", m.terminal)
	r.GET("/navigation", m.navigation)
	r.PUT("/navigation", m.navigation)
}
func failed(c *gin.Context, e error) { response.Fail(c, response.Invalid, response.Summary(e.Error())) }
func (m *Module) read(c *gin.Context) {
	s := m.cfg.Current()
	cert := ""
	var certificateInfo any
	if s.TLSCert != "" {
		b, e := os.ReadFile(s.TLSCert)
		if e != nil {
			failed(c, errors.New("无法读取当前面板证书"))
			return
		}
		cert = string(b)
		if block, _ := pem.Decode(b); block != nil {
			if leaf, err := x509.ParseCertificate(block.Bytes); err == nil {
				domains := append([]string{}, leaf.DNSNames...)
				for _, ip := range leaf.IPAddresses {
					domains = append(domains, ip.String())
				}
				certificateInfo = gin.H{"domains": domains, "issuer": leaf.Issuer.String(), "notAfter": leaf.NotAfter}
			}
		}
	}
	privateKey := ""
	if s.TLSKey != "" {
		if b, e := os.ReadFile(s.TLSKey); e == nil {
			privateKey = string(b)
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"alias": s.Alias, "idleMinutes": s.IdleMinutes, "siteRoot": s.SiteRoot, "backupRoot": s.BackupRoot, "username": auth.Current(c).User.Username, "boundDomain": s.BoundDomain, "sslEnabled": s.TLSCert != "", "certificate": cert, "privateKey": privateKey, "certificateId": s.PanelCertificateID, "certificateInfo": certificateInfo, "keyConfigured": s.TLSKey != "", "publicUrl": s.PublicURL, "port": s.Port, "entry": s.Entry})
}
func (m *Module) basic(c *gin.Context) {
	var in struct {
		Alias       string `json:"alias"`
		IdleMinutes int    `json:"idleMinutes"`
		SiteRoot    string `json:"siteRoot"`
		BackupRoot  string `json:"backupRoot"`
	}
	if !response.Bind(c, &in) {
		return
	}
	_, e := m.cfg.Apply(func(next *config.Config) error {
		next.Alias = strings.TrimSpace(in.Alias)
		next.IdleMinutes = in.IdleMinutes
		next.SiteRoot = in.SiteRoot
		next.BackupRoot = in.BackupRoot
		roots := []*string{&next.SiteRoot, &next.BackupRoot}
		for _, path := range roots {
			real, err := m.policy.Check(*path, true)
			if err != nil || m.policy.CheckTree(real) != nil {
				return errors.New("默认目录受保护或包含无效链接")
			}
			if info, err := os.Stat(real); err == nil && !info.IsDir() {
				return errors.New("默认路径不是目录")
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, managed := range []string{m.cfg.InstallDir} {
				if pathsafe.Contains(managed, real) || pathsafe.Contains(real, managed) {
					return errors.New("默认目录不能与应用程序或服务数据目录重叠")
				}
			}
			*path = real
		}
		if pathsafe.Contains(next.SiteRoot, next.BackupRoot) || pathsafe.Contains(next.BackupRoot, next.SiteRoot) {
			return errors.New("建站目录与备份目录不能相同或互相包含")
		}
		return nil
	})
	if e != nil {
		failed(c, e)
		return
	}
	m.read(c)
}
func (m *Module) account(c *gin.Context) {
	var in struct {
		Username        string `json:"username"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if err := auth.ValidateUsername(in.Username); err != nil {
		failed(c, err)
		return
	}
	user := auth.Current(c).User
	if e := m.auth.ConfirmPassword(m.auth.ClientIP(c), user, in.CurrentPassword); e != nil {
		failed(c, e)
		return
	}
	values := map[string]any{"username": in.Username, "auth_version": gorm.Expr("auth_version + 1")}
	if in.NewPassword != "" {
		if e := auth.ValidatePassword(in.NewPassword); e != nil {
			failed(c, e)
			return
		}
		hash, e := bcrypt.GenerateFromPassword([]byte(in.NewPassword), bcrypt.DefaultCost)
		if e != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		values["password_hash"] = string(hash)
	}
	e := m.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if e := tx.Model(&store.User{}).Where("username = ? AND id <> ?", in.Username, user.ID).Count(&count).Error; e != nil {
			return e
		}
		if count > 0 {
			return errors.New("账号已被使用")
		}
		if e := tx.Model(&store.User{}).Where("id = ?", user.ID).Updates(values).Error; e != nil {
			return e
		}
		return tx.Where("user_id = ?", user.ID).Delete(&store.AuthSession{}).Error
	})
	if e != nil {
		failed(c, e)
		return
	}
	response.OK(c, gin.H{"loginRequired": true, "loginUrl": m.auth.RequestOrigin(c).String() + "/" + m.cfg.Current().Entry})
}
func (m *Module) network(c *gin.Context) {
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	var in struct {
		BoundDomain   string  `json:"boundDomain"`
		SSLEnabled    bool    `json:"sslEnabled"`
		Certificate   string  `json:"certificate"`
		CertificateID string  `json:"certificateId"`
		PrivateKey    string  `json:"privateKey"`
		Port          *int    `json:"port"`
		Entry         *string `json:"entry"`
	}
	if !response.Bind(c, &in) {
		return
	}
	old := m.cfg.Current()
	oldOrigin := m.auth.RequestOrigin(c)
	nextDomain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(in.BoundDomain), "."))
	host := nextDomain
	if host == "" {
		origin := oldOrigin
		if old.BoundDomain != "" {
			origin, _ = url.Parse(old.UnboundURL)
		}
		if origin != nil {
			host = origin.Hostname()
		}
		if host == "" {
			host = old.LocalListenHost()
		}
	}
	certPath, keyPath := "", ""
	// 一对证书放在独立目录，配置只在全部写好后切换，API永不回显私钥。
	var staged string
	if in.SSLEnabled {
		if in.CertificateID != "" {
			var err error
			in.Certificate, in.PrivateKey, err = certificate.ReadPEM(m.db, old, in.CertificateID)
			if err != nil {
				failed(c, err)
				return
			}
		}
		key := []byte(in.PrivateKey)
		if len(key) == 0 && old.TLSKey != "" {
			var e error
			key, e = os.ReadFile(old.TLSKey)
			if e != nil {
				failed(c, e)
				return
			}
		}
		pair, e := tls.X509KeyPair([]byte(in.Certificate), key)
		if e != nil || len(pair.Certificate) == 0 {
			failed(c, errors.New("证书与私钥不匹配或格式无效"))
			return
		}
		leaf, e := x509.ParseCertificate(pair.Certificate[0])
		if e != nil || time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
			failed(c, errors.New("证书尚未生效或已过期"))
			return
		}
		if e = leaf.VerifyHostname(host); e != nil {
			failed(c, fmt.Errorf("证书未覆盖面板访问地址 %s", host))
			return
		}
		if len(in.Certificate) > 1<<20 || len(key) > 64<<10 {
			failed(c, errors.New("证书或私钥过大"))
			return
		}
		if in.CertificateID == "" {
			stored, err := certificate.Store(m.db, old, in.Certificate, string(key), "import", "", "", "")
			if err != nil {
				failed(c, err)
				return
			}
			in.CertificateID = stored.ID
		}
		base := filepath.Join(old.DataDir(), "panel-ssl")
		if e = os.MkdirAll(base, 0700); e != nil {
			failed(c, e)
			return
		}
		staged, e = os.MkdirTemp(base, "certificate-")
		if e != nil {
			failed(c, e)
			return
		}
		certPath = filepath.Join(staged, "fullchain.pem")
		keyPath = filepath.Join(staged, "private.key")
		if e = os.WriteFile(certPath, []byte(in.Certificate), 0600); e == nil {
			e = os.WriteFile(keyPath, key, 0600)
		}
		if e != nil {
			os.RemoveAll(staged)
			failed(c, e)
			return
		}
	}
	next, e := m.cfg.Apply(func(out *config.Config) error {
		if in.Port != nil {
			out.Port = *in.Port
		}
		if in.Entry != nil {
			out.Entry = strings.TrimSpace(*in.Entry)
		}
		if old.BoundDomain == "" && nextDomain != "" {
			out.UnboundURL = old.PublicURL
			if old.DirectIPAccess() {
				out.UnboundURL = oldOrigin.String()
			}
		}
		out.BoundDomain = nextDomain
		out.TLSCert = certPath
		out.TLSKey = keyPath
		out.PanelCertificateID = ""
		if in.SSLEnabled {
			out.PanelCertificateID = in.CertificateID
		}
		// 只修改端口或安全入口时，继续允许使用任一有效的服务器 IP 直连。
		if old.DirectIPAccess() && nextDomain == "" && !in.SSLEnabled {
			return nil
		}
		scheme := "http"
		if in.SSLEnabled {
			scheme = "https"
		}
		out.PublicURL = scheme + "://" + net.JoinHostPort(host, strconv.Itoa(out.Port))
		return nil
	})
	if e != nil {
		if staged != "" {
			os.RemoveAll(staged)
		}
		failed(c, e)
		return
	}
	portChanged := next.Port != old.Port
	listenerApplied := false
	restoreNetwork := func(cause error) {
		_, restore := m.cfg.Apply(func(out *config.Config) error {
			out.Port, out.Entry = old.Port, old.Entry
			out.UnboundURL = old.UnboundURL
			out.BoundDomain = old.BoundDomain
			out.TLSCert = old.TLSCert
			out.TLSKey = old.TLSKey
			out.PanelCertificateID = old.PanelCertificateID
			out.PublicURL = old.PublicURL
			return nil
		})
		if restore == nil {
			if listenerApplied {
				restore = m.applyNetwork(old)
			}
			if portChanged && m.refreshFirewall != nil {
				restore = errors.Join(restore, m.refreshFirewall(context.WithoutCancel(c.Request.Context())))
			}
			if staged != "" {
				os.RemoveAll(staged)
			}
		}
		failed(c, errors.Join(cause, restore))
	}
	if portChanged && m.refreshFirewall != nil {
		if e = m.refreshFirewall(c.Request.Context()); e != nil {
			restoreNetwork(e)
			return
		}
	}
	if next.Port != old.Port || next.TLSCert != old.TLSCert {
		if e = m.applyNetwork(next); e != nil {
			restoreNetwork(e)
			return
		}
		listenerApplied = true
	}
	nextOrigin := next.PanelOrigin(oldOrigin.Hostname()).String()
	loginRequired := nextOrigin != oldOrigin.String()
	if loginRequired {
		if e = m.db.Model(&store.User{}).Where("id > 0").Update("auth_version", gorm.Expr("auth_version + 1")).Error; e != nil {
			restoreNetwork(errors.New("无法刷新面板登录会话"))
			return
		}
	}
	response.OK(c, gin.H{"publicUrl": next.PublicURL, "loginRequired": loginRequired, "loginUrl": nextOrigin + "/" + next.Entry})
}

func (m *Module) terminal(c *gin.Context) {
	if c.Request.Method == "GET" {
		response.OK(c, gin.H{"fontSize": m.cfg.Current().TerminalFontSize})
		return
	}
	var in struct {
		FontSize int `json:"fontSize"`
	}
	if !response.Bind(c, &in) {
		return
	}
	next, err := m.cfg.Apply(func(next *config.Config) error {
		next.TerminalFontSize = in.FontSize
		return nil
	})
	if err != nil {
		failed(c, err)
		return
	}
	response.OK(c, gin.H{"fontSize": next.TerminalFontSize})
}
