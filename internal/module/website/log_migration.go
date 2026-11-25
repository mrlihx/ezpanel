package website

import (
	"os"
	"path/filepath"
	"strings"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
)

const siteLogRoot = pkgmgr.WebsiteLogRoot

func siteLogPath(name, kind string) string { return filepath.Join(siteLogRoot, name, kind+".log") }

func siteLogDirectory(site store.Website) string {
	if site.LogDirectory != "" {
		return site.LogDirectory
	}
	return site.ID
}
func validLogName(name string) bool {
	return name != "" && name != "." && name != ".." && name != ".services" && !strings.ContainsAny(name, `/\"$*?[]`)
}

func ensureSiteLogDir(name string) error {
	if err := os.MkdirAll(filepath.Join(siteLogRoot, name), 0755); err != nil {
		return err
	}
	if err := os.Chmod(siteLogRoot, 0755); err != nil {
		return err
	}
	return os.Chmod(filepath.Join(siteLogRoot, name), 0755)
}
