package task

import (
	"context"
	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManager(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.TaskLogMaxBytes = 40
	os.MkdirAll(cfg.DataDir(), 0700)
	db, e := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	old := store.Task{ID: store.ID(), Status: "running", StartedAt: time.Now()}
	db.Create(&old)
	m, e := New(context.Background(), db, cfg)
	if e != nil {
		t.Fatal(e)
	}
	db.First(&old, "id = ?", old.ID)
	if old.Status != "interrupted" {
		t.Fatal(old.Status)
	}
	started := make(chan struct{})
	finish := make(chan struct{})
	id, e := m.Submit(Meta{Module: "file", Action: "copy", LockKey: "same"}, func(ctx context.Context, l *Log) (any, error) {
		close(started)
		<-finish
		l.Line("one")
		l.Line("two")
		l.Line(strings.Repeat("a", 50))
		return BatchResult{FailedCount: 1}, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	<-started
	if _, e = m.Submit(Meta{LockKey: "same"}, func(context.Context, *Log) (any, error) { return nil, nil }); e == nil {
		t.Fatal("lock allowed")
	}
	close(finish)
	for i := 0; i < 100; i++ {
		var current store.Task
		db.First(&current, "id = ?", id)
		if current.Status != "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	m.Close()
	var row store.Task
	db.First(&row, "id = ?", id)
	if row.Status != "failed" || !row.LogTruncated {
		t.Fatal(row)
	}
	logs, next, e := m.ReadLogs(row, 1, 10)
	if e != nil || next != 2 || len(logs) != 1 || logs[0] != "two" {
		t.Fatal(logs, next, e)
	}
	logs, next, e = m.ReadLogs(row, 99, 10)
	if e != nil || next != 2 || len(logs) != 0 {
		t.Fatal(logs, next, e)
	}
}

func TestResultSecretsAndDeadline(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	os.MkdirAll(cfg.DataDir(), 0700)
	db, e := store.Open(filepath.Join(cfg.DataDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m, _ := New(context.Background(), db, cfg)
	defer m.Close()
	id, e := m.Submit(Meta{}, func(ctx context.Context, l *Log) (any, error) {
		l.Secrets("1", `a"b`)
		return map[string]any{"count": 123, "text": `a"b`, "id": "ab1cd", "whole": "1"}, context.DeadlineExceeded
	})
	if e != nil {
		t.Fatal(e)
	}
	var row store.Task
	for i := 0; i < 100; i++ {
		db.First(&row, "id = ?", id)
		if row.Status != "running" {
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if row.Status != "failed" || row.Result == nil || !strings.Contains(*row.Result, `"count":123`) || !strings.Contains(*row.Result, "[REDACTED]") || !strings.Contains(*row.Result, `"id":"ab1cd"`) {
		t.Fatal(row.Status, row.Result)
	}
}
func TestShutdownAndAudit(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	os.MkdirAll(cfg.DataDir(), 0700)
	db, e := store.Open(filepath.Join(cfg.DataDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	cron := store.CronJob{ID: store.ID()}
	db.Create(&cron)
	m, _ := New(context.Background(), db, cfg)
	started := make(chan struct{})
	id, e := m.Submit(Meta{CronJobID: cron.ID, Username: "admin"}, func(ctx context.Context, l *Log) (any, error) {
		l.Secrets("dbpass")
		l.Line("password=dbpass")
		close(started)
		<-ctx.Done()
		return BatchResult{FailedCount: 1}, ctx.Err()
	})
	if e != nil {
		t.Fatal(e)
	}
	<-started
	m.Close()
	var row store.Task
	db.First(&row, "id = ?", id)
	db.First(&cron, "id = ?", cron.ID)
	if row.Status != "interrupted" || cron.LastStatus == nil || *cron.LastStatus != "interrupted" {
		t.Fatal(row, cron)
	}
	allLogs, _ := store.QueryOpLogs(db, store.OpLogFilter{})
	var n int64
	for _, entry := range allLogs {
		if entry.TaskID != nil && *entry.TaskID == id {
			n++
		}
	}
	if n != 2 {
		t.Fatal(n)
	}
	b, _ := os.ReadFile(row.LogPath)
	if strings.Contains(string(b), "dbpass") {
		t.Fatal("secret in log")
	}
	if _, e = m.Submit(Meta{}, func(context.Context, *Log) (any, error) { return nil, nil }); e == nil {
		t.Fatal("accepted after close")
	}
}
func TestRoutes(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	os.MkdirAll(cfg.DataDir(), 0700)
	db, e := store.Open(filepath.Join(cfg.DataDir(), "db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m, _ := New(context.Background(), db, cfg)
	defer m.Close()
	id, _ := m.Submit(Meta{Module: "test"}, func(ctx context.Context, l *Log) (any, error) {
		l.Line("first")
		l.Line("second")
		return map[string]int{"count": 2}, nil
	})
	for i := 0; i < 100; i++ {
		var r store.Task
		db.First(&r, "id = ?", id)
		if r.Status != "running" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	r := gin.New()
	m.Routes(r.Group(""), nil)
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{{"/api/tasks/" + id + "?offset=1&limit=1", 200, `"nextOffset":2`}, {"/api/tasks/" + id + "?offset=999", 200, `"logs":[]`}, {"/api/tasks?page=1&page_size=0", 400, `"code":1001`}, {"/api/tasks?module=%27%20OR%201%3D1--", 200, `"total":0`}, {"/api/tasks/%27%20OR%201=1--", 404, `"code":1004`}} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	request := httptest.NewRequest("GET", "/api/tasks/"+id+"/stream", nil)
	request.Header.Set("Last-Event-ID", "6") // "first\n" 已由客户端读取。
	w := httptest.NewRecorder()
	r.ServeHTTP(w, request)
	result := w.Body.String()
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") ||
		!strings.Contains(result, "id: 13\nevent: done\n") || !strings.Contains(result, `"logs":["second"]`) ||
		!strings.Contains(result, `"status":"success"`) {
		t.Fatal(w.Code, result)
	}
}
