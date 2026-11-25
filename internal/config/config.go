package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"gopkg.in/yaml.v3"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	PHPMyAdminPHPVersion string            `yaml:"phpMyAdminPHPVersion,omitempty" json:"-"`
	PanelCertificateID   string            `yaml:"panelCertificateId,omitempty" json:"-"`
	TerminalFontSize     int               `yaml:"terminalFontSize" json:"terminalFontSize"`
	SoftwareMirrors      map[string]string `yaml:"softwareMirrors" json:"softwareMirrors"`
	UnboundURL           string            `yaml:"unboundUrl" json:"-"`
	runtime              *runtimeConfig
	Alias                string            `yaml:"alias" json:"alias"`
	IdleMinutes          int               `yaml:"idleMinutes" json:"idleMinutes"`
	SiteRoot             string            `yaml:"siteRoot" json:"siteRoot"`
	BackupRoot           string            `yaml:"backupRoot" json:"backupRoot"`
	BoundDomain          string            `yaml:"boundDomain" json:"boundDomain"`
	GitHubProxy          string            `yaml:"githubProxy" json:"githubProxy"`
	SoftwareSources      map[string]string `yaml:"softwareSources" json:"softwareSources"`
	InstallDir           string            `yaml:"-" json:"-"`
	ListenAddr           string            `yaml:"listenAddr" json:"listenAddr"`
	Port                 int               `yaml:"port" json:"port"`
	PublicURL            string            `yaml:"publicUrl" json:"publicUrl"`
	TrustedProxies       []string          `yaml:"trustedProxies" json:"trustedProxies"`
	TLSCert              string            `yaml:"tlsCert" json:"tlsCert"`
	TLSKey               string            `yaml:"tlsKey" json:"-"`
	Entry                string            `yaml:"entry" json:"-"`
	JWTSecret            string            `yaml:"jwt_secret" json:"-"`
	AESKey               string            `yaml:"aes_key" json:"-"`
	Timezone             string            `yaml:"timezone" json:"timezone"`
	Dev                  bool              `yaml:"-" json:"-"`
	TrashMaxBytes        int64             `yaml:"trashMaxBytes" json:"trashMaxBytes"`
	ExtractMaxBytes      int64             `yaml:"extractMaxBytes" json:"extractMaxBytes"`
	ExtractMaxEntries    int               `yaml:"extractMaxEntries" json:"extractMaxEntries"`
	TaskLogMaxBytes      int64             `yaml:"taskLogMaxBytes" json:"taskLogMaxBytes"`
	TaskRetentionDays    int               `yaml:"taskRetentionDays" json:"taskRetentionDays"`
	OpLogRetentionDays   int               `yaml:"opLogRetentionDays" json:"opLogRetentionDays"`
}

func (c Config) DataDir() string { return filepath.Join(c.InstallDir, "data") }
func defaults(install string) Config {
	return Config{TerminalFontSize: 14, Alias: "ezpanel", IdleMinutes: 30, SiteRoot: "/www/wwwroot", BackupRoot: "/www/backup", SoftwareSources: map[string]string{}, InstallDir: install, ListenAddr: "127.0.0.1", Timezone: "Asia/Shanghai", TrashMaxBytes: 10 << 30, ExtractMaxBytes: 10 << 30, ExtractMaxEntries: 100000, TaskLogMaxBytes: 10 << 20, TaskRetentionDays: 30, OpLogRetentionDays: 90}
}
func New(installDir string) (Config, error) {
	c := defaults(installDir)
	c.ListenAddr = "0.0.0.0"
	var b [32]byte
	for _, dst := range []*string{&c.JWTSecret, &c.AESKey} {
		if _, e := rand.Read(b[:]); e != nil {
			return c, e
		}
		*dst = base64.StdEncoding.EncodeToString(b[:])
	}
	entry, e := NewEntry()
	if e != nil {
		return c, e
	}
	c.Entry = entry
	n, e := rand.Int(rand.Reader, big.NewInt(55536))
	if e != nil {
		return c, e
	}
	c.Port = 10000 + int(n.Int64())
	return c, Validate(c)
}

func (c Config) DirectIPAccess() bool {
	return c.PublicURL == "" && c.BoundDomain == "" && net.ParseIP(c.ListenAddr).IsUnspecified()
}

func (c Config) LocalListenHost() string {
	ip := net.ParseIP(c.ListenAddr)
	if !ip.IsUnspecified() {
		return c.ListenAddr
	}
	if ip.To4() != nil {
		return "127.0.0.1"
	}
	return "::1"
}

// PanelOrigin 接受已通过来源校验的 IP，用于直连地址和跳转。
func (c Config) PanelOrigin(host string) *url.URL {
	var origin *url.URL
	if c.PublicURL != "" {
		origin, _ = url.Parse(strings.TrimSuffix(c.PublicURL, "/"))
	} else {
		if !c.DirectIPAccess() || net.ParseIP(host) == nil {
			host = c.LocalListenHost()
		}
		scheme := "http"
		if c.TLSCert != "" {
			scheme = "https"
		}
		origin = &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(c.Port))}
	}
	origin.Scheme = strings.ToLower(origin.Scheme)
	origin.Host = strings.ToLower(origin.Host)
	if (origin.Scheme == "https" && origin.Port() == "443") || (origin.Scheme == "http" && origin.Port() == "80") {
		origin.Host = origin.Hostname()
		if strings.Contains(origin.Host, ":") {
			origin.Host = "[" + origin.Host + "]"
		}
	}
	return origin
}
func NewEntry() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	var entry [8]byte
	for i := range entry {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", err
		}
		entry[i] = alphabet[n.Int64()]
	}
	return string(entry[:]), nil
}
func Load(installDir string) (Config, error) {
	c := defaults(installDir)
	f, e := os.Open(filepath.Join(c.DataDir(), "config.yaml"))
	if e != nil {
		return c, e
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if e = dec.Decode(&c); e != nil {
		return c, e
	}
	var extra any
	if e = dec.Decode(&extra); e != io.EOF {
		return c, errors.New("配置只允许一个文档")
	}
	return c, Validate(c)
}
func Validate(c Config) error {
	if e := validatePanel(c); e != nil {
		return e
	}
	if !filepath.IsAbs(c.InstallDir) || c.InstallDir == "/" {
		return errors.New("安装目录无效")
	}
	ip := net.ParseIP(c.ListenAddr)
	if ip == nil || c.Port < 1 || c.Port > 65535 {
		return errors.New("监听地址或端口无效")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return errors.New("TLS证书与私钥必须同时配置")
	}
	if c.PublicURL != "" {
		u, e := url.Parse(c.PublicURL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && u.Scheme != "http") {
			return errors.New("publicUrl必须为有效origin")
		}
	}
	for _, s := range c.TrustedProxies {
		if net.ParseIP(s) == nil {
			if _, _, e := net.ParseCIDR(s); e != nil {
				return errors.New("可信代理无效")
			}
		}
	}
	if !regexp.MustCompile(`^[a-zA-Z0-9]{5,}$`).MatchString(c.Entry) {
		return errors.New("安全入口须为至少5位字母或数字")
	}
	for i, s := range []string{c.JWTSecret, c.AESKey} {
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil || len(b) < 32 || (i == 1 && len(b) != 32) {
			return errors.New("配置密钥无效")
		}
	}
	if _, e := time.LoadLocation(c.Timezone); e != nil {
		return errors.New("时区无效")
	}
	if c.TrashMaxBytes <= 0 || c.ExtractMaxBytes <= 0 || c.ExtractMaxEntries <= 0 || c.TaskLogMaxBytes <= 0 || c.TaskRetentionDays <= 0 || c.OpLogRetentionDays <= 0 {
		return errors.New("容量与保留策略须为正数")
	}
	return nil
}
func Save(c Config) error {
	if e := Validate(c); e != nil {
		return e
	}
	if e := os.MkdirAll(c.InstallDir, 0755); e != nil {
		return e
	}
	if e := os.Chmod(c.InstallDir, 0755); e != nil {
		return e
	}
	// data/ 目录使用 0711：禁止非 root 枚举目录，但允许 panel-plugin 穿过进入 data/plugin/<id>/。
	if e := os.MkdirAll(c.DataDir(), 0711); e != nil {
		return e
	}
	if e := os.Chmod(c.DataDir(), 0711); e != nil {
		return e
	}
	b, e := yaml.Marshal(c)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(c.DataDir(), ".config-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), filepath.Join(c.DataDir(), "config.yaml"))
}
