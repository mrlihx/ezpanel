package alerts

import (
	"bytes"
	"context"
	"encoding/json"
	"ezpanel/internal/config"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestCertificateAlertDeliveryAndQuota(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := db.DB()
	t.Cleanup(func() { conn.Close() })
	cfg, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m := New(db, cfg, pkgmgr.New(), nil)
	router := gin.New()
	m.Routes(router.Group(""))
	var requests atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var payload map[string]string
		if err := json.NewDecoder(req.Body).Decode(&payload); req.Method != "PATCH" || err != nil || payload["title"] != "证书检查" || payload["message"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	call := func(method, path string, body any, status int, dst any) {
		t.Helper()
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(method, "/api/settings/alerts"+path, bytes.NewReader(buf))
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, res.Code, res.Body.String())
		}
		if dst == nil {
			return
		}
		var envelope struct{ Data json.RawMessage }
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			t.Fatal(err)
		}
	}
	var destination channelDTO
	call("POST", "/channels", map[string]any{
		"name": "测试 Webhook", "kind": "webhook", "url": receiver.URL, "method": "PATCH", "enabled": true,
	}, 200, &destination)
	if destination.ID == "" || destination.URL != "" || !destination.HasURL {
		t.Fatal("unexpected channel response", destination)
	}
	now := time.Now().UTC()
	cert := store.Certificate{ID: store.ID(), Fingerprint: "near-expiry", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	if err := db.Create(&cert).Error; err != nil {
		t.Fatal(err)
	}
	site := store.Website{ID: store.ID(), Name: "证书测试站点", CertificateID: cert.ID, SSLEnabled: true}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	var result struct{ List []target }
	call("GET", "/targets?kind=certificate", nil, 200, &result)
	if len(result.List) != 1 || result.List[0].ID != site.ID {
		t.Fatalf("certificate targets = %+v", result.List)
	}
	var saved rule
	input := RuleInput{Title: "证书检查", Kind: "certificate", Target: site.ID, Enabled: true,
		IntervalSeconds: 60, MaxDaily: 1, Days: 7, ChannelIDs: []string{destination.ID}}
	call("POST", "/rules", input, 200, &saved)
	if saved.TargetName != site.Name || saved.Title != input.Title || !saved.Enabled {
		t.Fatalf("saved rule = %+v", saved)
	}
	call("DELETE", "/channels/"+destination.ID, nil, 409, nil)
	check := func(force bool) {
		t.Helper()
		if force {
			if err := db.Model(&rule{}).Where("id = ?", saved.ID).UpdateColumn("next_check_at", time.Now().UTC().Add(-time.Second)).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := m.checkDue(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	check(false)
	check(true)
	queued, err := m.queuedDeliveries(context.Background(), 2, nil)
	if err != nil || len(queued) != 1 {
		t.Fatalf("queued deliveries = %d, %v", len(queued), err)
	}
	if err := m.deliverQueued(context.Background(), queued[0]); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one delivery round within the daily quota, got %d", requests.Load())
	}
	if err := m.writeDelivery("", "证书检查测试失败", channel{Kind: "webhook", Name: "Webhook"}, context.DeadlineExceeded); err != nil {
		t.Fatal(err)
	}
	var logsResult struct {
		List  []deliveryLog
		Total int
	}
	call("GET", "/logs?status=success&keyword=Webhook&page=1&page_size=20", nil, 200, &logsResult)
	if logsResult.Total != 1 || len(logsResult.List) != 1 || logsResult.List[0].RuleID != saved.ID || !logsResult.List[0].Success {
		t.Fatalf("delivery logs = %+v", logsResult)
	}
	renewed := store.Certificate{ID: store.ID(), Fingerprint: "renewed", NotBefore: now, NotAfter: now.AddDate(0, 2, 0)}
	if err := db.Create(&renewed).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&store.Website{}).Where("id = ?", site.ID).UpdateColumn("certificate_id", renewed.ID).Error; err != nil {
		t.Fatal(err)
	}
	check(true)
	var rows struct{ List []rule }
	call("GET", "/rules", nil, 200, &rows)
	if len(rows.List) != 1 || rows.List[0].State != "normal" || rows.List[0].LastCheckedAt == nil || len(rows.List[0].ChannelIDs) != 1 {
		t.Fatalf("rules after certificate renewal = %+v", rows.List)
	}
	call("PATCH", "/rules/"+saved.ID, map[string]bool{"enabled": false}, 200, nil)
	call("DELETE", "/rules/"+saved.ID, nil, 200, nil)
	call("DELETE", "/channels/"+destination.ID, nil, 200, nil)
	call("GET", "/logs?ruleId="+saved.ID, nil, 200, &logsResult)
	if logsResult.Total != 1 {
		t.Fatal("deleting the rule removed its delivery history")
	}
}
