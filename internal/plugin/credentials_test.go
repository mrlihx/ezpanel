package plugin

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestCredentialVaultScopeAndRedaction(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { _ = sql.Close() })
	m := &Module{db: db, cfg: config.Config{AESKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	for _, id := range []string{"owner-plugin", "other-plugin"} {
		if err := db.Create(&store.Plugin{ID: id, Name: id, Type: "process"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	g := gin.New()
	m.Routes(g.Group(""))
	g.GET("/test/:plugin/:slot", func(c *gin.Context) {
		c.Set("plugin.identity", hostIdentity{id: c.Param("plugin"), manifest: Manifest{CredentialSlots: []CredentialSlot{{ID: "token", Title: "API Token"}}}, run: &running{log: &pluginLog{}}})
		m.hostCredential(c)
	})
	call := func(method, url, body string, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(method, url, strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, url, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	const url = "/api/plugin-credentials"
	call(http.MethodPost, url, `{"name":"存储密钥","value":"demo-secret"}`, http.StatusOK)
	var entry vaultCredential
	if err := db.First(&entry).Error; err != nil || entry.Secret == "demo-secret" {
		t.Fatalf("encrypted credential was not saved: %v", err)
	}
	if body := call(http.MethodGet, url, "", http.StatusOK); strings.Contains(body, "demo-secret") || strings.Contains(body, entry.Secret) {
		t.Fatal("credential leaked through administrative list")
	}
	if err := db.Create(&credentialBinding{PluginID: "owner-plugin", Slot: "token", CredentialID: entry.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if body := call(http.MethodGet, "/test/owner-plugin/token", "", http.StatusOK); !strings.Contains(body, "demo-secret") {
		t.Fatal("authorized plugin did not receive its value")
	}
	call(http.MethodGet, "/test/other-plugin/token", "", http.StatusForbidden)
	call(http.MethodDelete, url+"/"+entry.ID, "", http.StatusConflict)
}
