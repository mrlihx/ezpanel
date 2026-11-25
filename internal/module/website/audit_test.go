package website

import (
	"strings"
	"testing"

	"ezpanel/internal/store"
)

func TestSiteAuditCapturesChangesWithoutRuntimeSecrets(t *testing.T) {
	old := store.Website{PHPVersion: "8.2", Domains: `["old.example.com","keep.example.com"]`}
	row := old
	row.PHPVersion = "8.5"
	row.Domains = `["new.example.com:8080","keep.example.com"]`
	row.RuntimeCommand = "TOKEN=private-command"
	row.RuntimeEnv = "TOKEN=private-value"
	row.ProxyRoutes = []store.ProxyRoute{{Name: "api", Path: "/api", Target: "https://www.example.com/backend?token=private-query", Enabled: true}}
	_, details := siteChanges(old, row)
	got := strings.Join(details, "\n")
	for _, want := range []string{"PHP 版本 8.2 → 8.5", "添加域名 new.example.com:8080", "移除域名 old.example.com", "修改启动命令", "修改环境变量", "https://www.example.com/backend"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %s", want, got)
		}
	}
	if strings.Contains(got, "private-") {
		t.Fatalf("audit disclosed a secret: %s", got)
	}
}
