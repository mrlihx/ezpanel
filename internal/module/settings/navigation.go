package settings

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

var navigationTabs = map[string][]string{
	"oplogs": {
		"panel/operation", "panel/login", "panel/runtime", "panel/cron", "ssh",
		"website/logs", "website/operations", "website/analysis",
		"apps/openresty", "apps/mysql", "apps/mariadb", "apps/redis", "apps/php",
		"apps/mongodb", "apps/postgresql", "apps/memcached", "apps/docker", "apps/ftp", "apps/rsync", "apps/clamav", "apps/fail2ban", "apps/supervisor", "apps/frp",
	},
	"databases":      {"mysql", "postgresql", "mongodb", "redis", "sqlite"},
	"apps":           {"web", "database", "tools"},
	"tools":          {"ip", "swap", "time", "password", "disks", "hosts", "mirrors", "commands", "audit"},
	"settings":       {"basic", "network", "backup", "alerts", "credentials", "sso"},
	"alerts":         {"rules", "channels", "logs"},
	"websites":       {"php", "proxy", "go", "python", "nodejs"},
	"docker":         {"overview", "containers", "images", "networks", "volumes", "registries", "compose", "settings"},
	"dockerImages":   {"local", "search"},
	"dockerCompose":  {"projects", "templates"},
	"dockerSettings": {"basic", "full"},
	"ssh":            {"settings", "users", "logs"},
	"terminal":       {"sessions", "hosts", "commands"},
	"security":       {"firewall", "ssh", "hardening"},
	"waf":            {"overview", "events", "rules", "sites", "policy"},
}

const databaseInstanceSuffix = `(?:[a-z2-7]{6}|[a-z2-7]{26})`

var databaseNavigation = map[string]*regexp.Regexp{
	"oplogSite":          regexp.MustCompile(`^[0-9a-f]{32}$`),
	"postgresqlInstance": regexp.MustCompile(`^(?:__all__|default|` + databaseInstanceSuffix + `)$`),
	"mysqlInstance":      regexp.MustCompile(`^(?:local-mysql|[0-9a-f]{32}|mysql-` + databaseInstanceSuffix + `)$`),
	"mongodbInstance":    regexp.MustCompile(`^(?:local|[0-9a-f]{32}|mongodb-` + databaseInstanceSuffix + `)$`),
	"redisInstance":      regexp.MustCompile(`^(?:local|[0-9a-f]{32}|redis-` + databaseInstanceSuffix + `)$`),
}

var supervisorLogSource = regexp.MustCompile(`^[0-9a-f]{16}$`)

func validNavigation(section, value string) bool {
	if format := databaseNavigation[section]; format != nil {
		return format.MatchString(value)
	}
	if section == "oplogs" && strings.HasPrefix(value, "apps/") {
		if app, source, ok := strings.Cut(strings.TrimPrefix(value, "apps/"), "/"); ok {
			switch app {
			case "supervisor":
				return source == "main" || supervisorLogSource.MatchString(source)
			case "frp":
				return source == "frps" || source == "frpc"
			}
			return slices.Contains(navigationTabs[section], "apps/"+app) &&
				slices.Contains([]string{"access", "container", "error", "file", "journal", "slow", "transfer"}, source)
		}
	}
	return slices.Contains(navigationTabs[section], value)
}

func (m *Module) navigation(c *gin.Context) {
	prefix := fmt.Sprintf("navigation.%d.", auth.Current(c).User.ID)
	if c.Request.Method == "GET" {
		var rows []store.Setting
		if err := m.db.Where("key LIKE ?", prefix+"%").Find(&rows).Error; err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		tabs := map[string]string{}
		for _, row := range rows {
			section := strings.TrimPrefix(row.Key, prefix)
			if validNavigation(section, row.Value) {
				tabs[section] = row.Value
			}
		}
		response.OK(c, tabs)
		return
	}
	var in struct {
		Section string `json:"section"`
		Tab     string `json:"tab"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !validNavigation(in.Section, in.Tab) {
		response.Fail(c, response.Invalid, "页面标签无效")
		return
	}
	c.Set("panel.auditRecorded", true)
	if err := m.db.Save(&store.Setting{Key: prefix + in.Section, Value: in.Tab}).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	response.OK(c, nil)
}
