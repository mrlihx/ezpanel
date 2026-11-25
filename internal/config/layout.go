package config

import (
	"path/filepath"
	"strings"
)

const DefaultInstallDir = "/opt/ezpanel"

// Layout derives all panel-managed directories from InstallDir.
type Layout struct {
	root string
}

func NewLayout(installDir string) Layout {
	root := strings.TrimSpace(installDir)
	if root == "" {
		root = DefaultInstallDir
	}
	return Layout{root: filepath.Clean(root)}
}

func (c Config) Layout() Layout { return NewLayout(c.InstallDir) }

func (l Layout) Root() string          { return l.root }
func (l Layout) Binary() string        { return filepath.Join(l.root, "ezpanel") }
func (l Layout) DataDir() string       { return filepath.Join(l.root, "data") }
func (l Layout) PluginsDir() string    { return filepath.Join(l.root, "plugins") }
func (l Layout) PluginDataDir() string { return filepath.Join(l.root, "data", "plugin") }
func (l Layout) PluginData(id string) string {
	return filepath.Join(l.root, "data", "plugin", id)
}
func (l Layout) AppsDir() string      { return filepath.Join(l.root, "apps") }
func (l Layout) LogDir() string       { return filepath.Join(l.root, "log") }
func (l Layout) TempDir() string      { return filepath.Join(l.root, "tmp") }
func (l Layout) TrashDir() string     { return filepath.Join(l.root, "trash") }
func (l Layout) SnapshotsDir() string { return filepath.Join(l.root, ".snapshots") }

func normInstance(instance string) string {
	instance = strings.TrimSpace(instance)
	if instance == "" {
		return "default"
	}
	return instance
}

func (l Layout) AppDir(app string) string { return filepath.Join(l.AppsDir(), app) }
func (l Layout) AppInstanceDir(app, instance string) string {
	return filepath.Join(l.AppsDir(), app, normInstance(instance))
}
func (l Layout) AppLogDir(app string) string { return filepath.Join(l.LogDir(), app) }
func (l Layout) AppInstanceLogDir(app, instance string) string {
	return filepath.Join(l.LogDir(), app, normInstance(instance))
}

func (l Layout) OpenRestyDir() string      { return l.AppDir("openresty") }
func (l Layout) OpenRestyConfig() string   { return filepath.Join(l.OpenRestyDir(), "nginx.conf") }
func (l Layout) OpenRestyConfDir() string  { return filepath.Join(l.OpenRestyDir(), "conf.d") }
func (l Layout) OpenRestyLogDir() string   { return l.AppLogDir("openresty") }
func (l Layout) OpenRestyErrorLog() string { return filepath.Join(l.OpenRestyLogDir(), "error.log") }
func (l Layout) ACMEDir() string           { return filepath.Join(l.OpenRestyDir(), "acme") }

func (l Layout) WAFDir() string      { return filepath.Join(l.OpenRestyDir(), "ezwaf") }
func (l Layout) WAFConf() string     { return filepath.Join(l.OpenRestyConfDir(), "00-panel-waf.conf") }
func (l Layout) WAFLogDir() string   { return l.AppLogDir("waf") }
func (l Layout) WAFEventLog() string { return filepath.Join(l.WAFLogDir(), "events.jsonl") }

func (l Layout) PHPBaseDir() string { return l.AppDir("php") }
func (l Layout) PHPDir(branch string) string {
	return filepath.Join(l.PHPBaseDir(), strings.ReplaceAll(branch, ".", ""))
}
func (l Layout) PHPLogDir(branch string) string {
	return filepath.Join(l.AppLogDir("php"), strings.ReplaceAll(branch, ".", ""))
}

func (l Layout) PHPMyAdminDir() string        { return l.AppDir("phpmyadmin") }
func (l Layout) PHPMyAdminPrivateDir() string { return filepath.Join(l.PHPMyAdminDir(), ".private") }

func (l Layout) MySQLSocket(instance string) string {
	return filepath.Join(l.AppInstanceDir("mysql", instance), "run", "mysql.sock")
}

func (l Layout) PureFTPdLogDir() string { return l.AppLogDir("pure-ftpd") }
func (l Layout) PureFTPdLog() string    { return filepath.Join(l.PureFTPdLogDir(), "transfer.log") }
func (l Layout) RsyncLogDir() string    { return l.AppLogDir("rsync") }
func (l Layout) RsyncLog() string       { return filepath.Join(l.RsyncLogDir(), "rsyncd.log") }
