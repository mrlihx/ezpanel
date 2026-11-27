package security

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"ezpanel/internal/hardening"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

type Module struct {
	cfg       config.Config
	auth      *auth.Service
	mu        sync.Mutex
	hardening *hardening.Manager
}

func New(cfg config.Config, a *auth.Service) *Module { return &Module{cfg: cfg, auth: a} }
func (m *Module) Name() string                       { return "security" }

type PluginSecurityStatus struct {
	FirewallEnabled    bool       `json:"firewallEnabled"`
	FirewallRules      int        `json:"firewallRules"`
	HardeningEnabled   bool       `json:"hardeningEnabled"`
	HardeningRules     int        `json:"hardeningRules"`
	HardeningCheckedAt *time.Time `json:"hardeningCheckedAt,omitempty"`
}

func (m *Module) PluginStatus() (PluginSecurityStatus, error) {
	m.mu.Lock()
	firewall, err := m.readFirewall()
	m.mu.Unlock()
	if err != nil {
		return PluginSecurityStatus{}, err
	}
	var state hardening.State
	if m.hardening != nil {
		state = m.hardening.State()
	}
	return PluginSecurityStatus{
		FirewallEnabled: firewall.Enabled, FirewallRules: len(firewall.Rules),
		HardeningEnabled: state.Enabled, HardeningRules: len(state.Rules), HardeningCheckedAt: state.CheckedAt,
	}, nil
}

func (m *Module) ScanHardening() error {
	if m.hardening == nil {
		return errors.New("系统加固模块未初始化")
	}
	return m.hardening.Scan()
}
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/security")
	m.hardeningRoutes(r)
	r.GET("/firewall", m.firewall)
	r.PUT("/firewall", m.firewallSettings)
	r.POST("/firewall/rules", m.saveRule)
	r.POST("/firewall/rules/batch", m.batchRules)
	r.PUT("/firewall/rules/:id", m.saveRule)
	r.DELETE("/firewall/rules/:id", m.deleteRule)
	r.POST("/firewall/import", m.importRules)
	r.POST("/firewall/ip-pools/update", m.updateIPPools)
	r.GET("/ssh", m.ssh)
	r.PUT("/ssh", m.saveSSH)
	r.POST("/ssh/service", m.sshService)
	r.GET("/ssh/users", m.sshUsers)
	r.POST("/ssh/users", m.createSSHUser)
	r.DELETE("/ssh/users/:name", m.deleteSSHUser)
	r.PUT("/ssh/users/:name/password", m.sshPassword)
	r.GET("/ssh/users/:name/keys", m.sshKeys)
	r.POST("/ssh/users/:name/keys", m.sshKeys)
}
func fail(c *gin.Context, err error) {
	response.Fail(c, response.System, response.Summary(err.Error()))
}
func run(ctx context.Context, name string, args ...string) (string, error) {
	bin := name
	if !filepath.IsAbs(name) {
		var err error
		bin, err = executor.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("系统未安装%s", name)
		}
	}
	result, err := executor.Run(ctx, 30*time.Second, bin, args...)
	if err != nil {
		return "", fmt.Errorf("%s操作失败：%s", name, strings.TrimSpace(result.Stderr+result.Stdout))
	}
	if result.Truncated {
		return "", errors.New("系统响应超过读取上限")
	}
	return result.Stdout, nil
}

// 面板只写自己管理的文件，保存前保留一份备份。
func write(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if old, err := os.ReadFile(path); err == nil {
		if err = os.WriteFile(path+".panel-backup", old, 0600); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".security-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	err = file.Chmod(mode)
	if err == nil {
		_, err = file.Write(data)
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}
