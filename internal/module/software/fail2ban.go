package software

import (
	"net/netip"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) fail2banRoutes(r *gin.RouterGroup) {
	r.GET("/protection", m.protectionSettings)
	r.PUT("/protection/rules/:service", m.saveProtectionRule)
	r.DELETE("/protection/rules/:service", m.deleteProtectionRule)
	r.PUT("/protection/ips/:list", m.saveProtectionIPs)
	r.GET("/protection/rules/:service/logs", m.protectionLogs)
	r.POST("/protection/rules/:service/unban", m.unbanProtectionIP)
}

func (m *Module) protectionSettings(c *gin.Context) {
	settings, err := m.packages.Fail2banSettings(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, settings)
}

func (m *Module) saveProtectionRule(c *gin.Context) {
	var rule pkgmgr.Fail2banRule
	if !response.Bind(c, &rule) {
		return
	}
	rule.Service = c.Param("service")
	if err := pkgmgr.ValidateFail2banRule(&rule); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	err := m.packages.UpdateFail2ban(c.Request.Context(), rule.Enabled, func(settings *pkgmgr.Fail2banSettings) {
		settings.Rules[rule.Service] = rule
	})
	m.protectionResult(c, err)
}

func (m *Module) deleteProtectionRule(c *gin.Context) {
	service := c.Param("service")
	if service != "sshd" && service != "ftpd" && service != "mysql" {
		response.Fail(c, response.Invalid, "保护服务无效")
		return
	}
	err := m.packages.UpdateFail2ban(c.Request.Context(), false, func(settings *pkgmgr.Fail2banSettings) {
		settings.Rules[service] = pkgmgr.Fail2banRule{Service: service}
	})
	m.protectionResult(c, err)
}

func (m *Module) saveProtectionIPs(c *gin.Context) {
	var in struct {
		Addresses string `json:"addresses"`
	}
	if !response.Bind(c, &in) {
		return
	}
	list := c.Param("list")
	if list != "blacklist" && list != "whitelist" {
		response.Fail(c, response.Invalid, "IP 名单无效")
		return
	}
	addresses, err := pkgmgr.ParseFail2banIPs(in.Addresses)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	err = m.packages.UpdateFail2ban(c.Request.Context(), list == "blacklist" && len(addresses) > 0, func(settings *pkgmgr.Fail2banSettings) {
		if list == "whitelist" {
			settings.Whitelist = addresses
		} else {
			settings.Blacklist = addresses
		}
	})
	m.protectionResult(c, err)
}

func (m *Module) protectionLogs(c *gin.Context) {
	logs, err := m.packages.Fail2banLogs(c.Request.Context(), c.Param("service"))
	if err != nil {
		fail(c, err)
		return
	}
	response.OK(c, logs)
}

func (m *Module) unbanProtectionIP(c *gin.Context) {
	var in struct {
		IP string `json:"ip"`
	}
	if !response.Bind(c, &in) {
		return
	}
	ip, err := netip.ParseAddr(in.IP)
	if err != nil || ip.Zone() != "" {
		response.Fail(c, response.Invalid, "IP 地址无效")
		return
	}
	m.protectionResult(c, m.packages.UnbanFail2banIP(c.Request.Context(), c.Param("service"), ip))
}

func (m *Module) protectionResult(c *gin.Context, err error) {
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, nil)
}
