package pkgmgr

import (
	"context"
	"path/filepath"
)

var phpBranches = []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}

func phpBranch(id string) string {
	for _, branch := range phpBranches {
		if id == "php-"+branch {
			return branch
		}
	}
	return ""
}
func IsPHP(id string) bool { return phpBranch(id) != "" }
func (m *Manager) phpDirectory(id string) string {
	return filepath.Join(m.defaultInstallRoot(id), "conf")
}

func init() {
	for _, branch := range phpBranches {
		id := "php-" + branch
		applications = append(applications, application{id: id, name: "PHP-" + branch, category: "web", description: "PHP " + branch + " 独立CLI与FPM，可与其他PHP版本共存", units: []string{"ezpanel-" + id + ".service"}})
	}
}

func phpEnvironment(id, prefix string, env []string) []string {
	standard := "gnu99"
	if phpBranch(id) >= "8.4" {
		standard = "gnu11"
	}
	compat := prefix + "/compat"
	return append(env, "PKG_CONFIG_PATH="+compat+"/libxml/lib/pkgconfig:"+compat+"/openssl/lib/pkgconfig:"+compat+"/curl/lib/pkgconfig", "CFLAGS=-O2 -std="+standard+" -fcommon -Wno-error=incompatible-pointer-types -Wno-error=implicit-function-declaration", "CPPFLAGS=-I"+compat+"/libxml/include/libxml2 -I"+compat+"/openssl/include -I"+compat+"/curl/include", "LDFLAGS=-L"+compat+"/libxml/lib -L"+compat+"/openssl/lib -L"+compat+"/curl/lib")
}

func (m *Manager) PHPVersions(ctx context.Context) []map[string]any {
	rows := make([]map[string]any, 0, len(phpBranches))
	for _, branch := range phpBranches {
		state, e := m.Status(ctx, "php-"+branch)
		running := e == nil && state.Running != nil && *state.Running
		rows = append(rows, map[string]any{"version": branch, "installed": e == nil && state.Installed, "running": running})
	}
	return rows
}
