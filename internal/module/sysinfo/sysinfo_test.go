package sysinfo

import (
	"context"
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/server"
	"ezpanel/internal/store"
	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/disk"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRate(t *testing.T) {
	if rate(100, 200, 2) == nil || *rate(100, 200, 2) != 50 {
		t.Fatal("actual interval")
	}
	if rate(200, 100, 2) != nil || rate(0, 100, 0) != nil {
		t.Fatal("reset/first must be unknown")
	}
}
func TestQuery(t *testing.T) {
	for _, q := range []string{"sort=bad", "limit=0", "limit=101", "limit=x", "sort=cpu%3Brm"} {
		if _, _, e := parseQuery(q); e == nil {
			t.Fatal(q)
		}
	}
}

func TestSharedWebSocketAndAuthentication(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.Dev = true
	db, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	user := store.User{Username: "admin", PasswordHash: "unused", AuthVersion: 1}
	db.Create(&user)
	a, _ := auth.New(db, cfg)
	token, _ := a.Issue(user)
	m := New(context.Background(), a)
	defer m.Close()
	r, _ := server.New(server.Options{Auth: a})
	r.RegisterModule(m)
	origin := "http://127.0.0.1:" + strconv.Itoa(cfg.Port)
	entryResponse := httptest.NewRecorder()
	r.ServeHTTP(entryResponse, httptest.NewRequest(http.MethodGet, origin+"/"+cfg.Entry, nil))
	if entryResponse.Code != http.StatusFound {
		t.Fatalf("entry returned %d", entryResponse.Code)
	}
	entry := entryResponse.Result().Cookies()[0]
	srv := httptest.NewServer(r)
	defer srv.Close()
	dialer := websocket.Dialer{}
	target := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/sysinfo"
	headers := http.Header{"Origin": {origin}, "Host": {strings.TrimPrefix(origin, "http://")}}
	headers.Set("Cookie", entry.Name+"="+entry.Value)
	if c, response, e := dialer.Dial(target, headers); e == nil {
		c.Close()
		t.Fatal("anonymous accepted")
	} else if response.StatusCode != 401 {
		t.Fatal(response.StatusCode)
	}
	headers.Set("Cookie", entry.Name+"="+entry.Value+"; panel_token="+token)
	headers.Set("Origin", "http://evil.example")
	if c, response, e := dialer.Dial(target, headers); e == nil {
		c.Close()
		t.Fatal("bad origin accepted")
	} else if response.StatusCode != 403 {
		t.Fatal(response.StatusCode)
	}
	headers.Set("Origin", origin)
	for _, query := range []string{"sort=cpu%3Brm", "limit=101", "limit=-1"} {
		req := httptest.NewRequest("GET", origin+"/api/sysinfo/processes?"+query, nil)
		req.AddCookie(entry)
		req.AddCookie(&http.Cookie{Name: "panel_token", Value: token})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"code":1001`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}

	// Host must match configured public origin; the transport still dials the test listener.
	first, _, e := dialer.Dial(target, headers)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	second, _, e := dialer.Dial(target, headers)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	var p, q Point
	first.SetReadDeadline(time.Now().Add(5 * time.Second))
	second.SetReadDeadline(time.Now().Add(5 * time.Second))
	if e := first.ReadJSON(&p); e != nil {
		t.Fatal(e)
	}
	if e := second.ReadJSON(&q); e != nil {
		t.Fatal(e)
	}
	if !p.TS.Equal(q.TS) {
		t.Fatal("clients did not share current sample")
	}
	m.mu.RLock()
	if len(m.clients) != 2 {
		t.Fatal("missing subscriptions")
	}
	m.mu.RUnlock()
	dashboard, _, e := dialer.Dial(target+"?view=dashboard", headers)
	if e != nil {
		t.Fatal(e)
	}
	defer dashboard.Close()
	_ = dashboard.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame dashboardFrame
	if e := dashboard.ReadJSON(&frame); e != nil || frame.Point == nil || frame.Overview == nil || len(frame.Top) != 2 {
		t.Fatalf("dashboard snapshot missing: %v", e)
	}
	if e := dashboard.WriteJSON(dashboardSubscription{Processes: &dashboardProcessFilter{Sort: "mem", Keyword: "unlikely-process-example"}}); e != nil {
		t.Fatal(e)
	}
	_ = dashboard.SetReadDeadline(time.Now().Add(10 * time.Second))
	for frame.Processes == nil {
		frame = dashboardFrame{}
		if e := dashboard.ReadJSON(&frame); e != nil {
			t.Fatal(e)
		}
	}
	if frame.Processes.Filter.Sort != "mem" || frame.Processes.Filter.Keyword != "unlikely-process-example" {
		t.Fatal(frame.Processes.Filter)
	}
	if os.Getenv("PANEL_TEST_WS_REVOCATION") == "1" {
		db.Model(&user).Update("auth_version", 2)
		first.SetReadDeadline(time.Now().Add(65 * time.Second))
		for {
			if _, _, e := first.ReadMessage(); e != nil {
				if !websocket.IsCloseError(e, 4001) {
					t.Fatalf("expected revoked-session close: %v", e)
				}
				break
			}
		}
	}
	m.Close()
	_ = first.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		_, _, e := first.ReadMessage()
		if e != nil {
			if timeout, ok := e.(net.Error); ok && timeout.Timeout() {
				t.Fatal("shutdown kept socket open")
			}
			break
		}
	}
}

func TestFirstSampleAndSlowSubscriber(t *testing.T) {
	ch := make(chan Point, 1)
	ch <- Point{}
	m := &Module{clients: map[chan Point]struct{}{ch: {}}}
	finished := make(chan struct{})
	go func() { m.sample(context.Background()); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("slow subscriber blocked sampling")
	}
	if m.point.CPU != nil || m.point.NetUp != nil || m.point.NetDown != nil || m.point.DiskRead != nil || m.point.DiskWrite != nil {
		t.Fatal("first sample must not invent rates")
	}
	for _, p := range m.processes["cpu"] {
		if p.CPU != nil {
			t.Fatal("first process CPU must be unknown")
		}
	}
	if m.overview.Hostname == "" {
		t.Fatal("real host not collected")
	}
}

func TestPhysicalDisksAvoidDoubleCounting(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sda", "device"), 0700)
	os.MkdirAll(filepath.Join(root, "dm-0"), 0700)
	stats := map[string]disk.IOCountersStat{"sda": {ReadBytes: 100}, "sda1": {ReadBytes: 100}, "dm-0": {ReadBytes: 100}}
	result := physicalDisks(stats, root)
	if len(result) != 1 || result["sda"].ReadBytes != 100 {
		t.Fatal(result)
	}
	if physicalDisks(stats, filepath.Join(root, "missing")) != nil {
		t.Fatal("missing source must be unknown")
	}
}
