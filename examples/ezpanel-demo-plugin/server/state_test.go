package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"testing"
	"time"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

func TestEventDedupAndPersistence(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStateStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	ev := panelsdk.Event{
		ID:         "evt-1",
		Name:       "site.backup.finished",
		Status:     "success",
		SiteID:     "site-1",
		ResourceID: "bak-1",
		TaskID:     "task-1",
		Time:       time.Now().UTC(),
	}
	if !store.AddEvent(ev) {
		t.Fatal("first AddEvent should return true")
	}
	if store.AddEvent(ev) {
		t.Fatal("duplicate AddEvent should return false")
	}

	reloaded, err := NewStateStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Snapshot().Events) != 1 {
		t.Fatalf("expected 1 persisted event, got %d", len(reloaded.Snapshot().Events))
	}
	if reloaded.AddEvent(ev) {
		t.Fatal("reloaded store should reject duplicate event ID")
	}
}

func TestDNSRecordsIdempotentMultiValue(t *testing.T) {
	store, err := NewStateStore(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	name := "_acme-challenge.example.com"
	for _, change := range []panelsdk.DNSChange{
		{Action: "present", Name: name, Value: "token-a"},
		{Action: "present", Name: name, Value: "token-a"},
		{Action: "present", Name: name, Value: "token-b"},
	} {
		if err := store.ApplyDNS(change); err != nil {
			t.Fatal(err)
		}
	}
	vals := store.Snapshot().DNSRecords[name]
	if len(vals) != 2 || vals[0] != "token-a" || vals[1] != "token-b" {
		t.Fatalf("unexpected DNS values: %#v", vals)
	}
	if err := store.ApplyDNS(panelsdk.DNSChange{Action: "remove", Name: name, Value: "token-a"}); err != nil {
		t.Fatal(err)
	}
	vals = store.Snapshot().DNSRecords[name]
	if len(vals) != 1 || vals[0] != "token-b" {
		t.Fatalf("expected only token-b left, got %#v", vals)
	}
}

func TestDemoBackupStorageLifecycle(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStateStore(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	storage := NewDemoBackupStorage(dir, store)
	ctx := context.Background()
	req := panelsdk.StorageRequest{
		ChannelID: "ch-1",
		Key:       "site-1/daily.tar.gz",
		Values: map[string]string{
			"folder":       "prod-backups",
			"access-token": "secret-token",
			"quota-mb":     "1",
		},
	}
	if err := storage.Test(ctx, req); err != nil {
		t.Fatalf("Test failed: %v", err)
	}
	if err := storage.Upload(ctx, req, bytes.NewBufferString("archive-payload")); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	rc, err := storage.Open(ctx, req)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	got, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil || string(got) != "archive-payload" {
		t.Fatalf("unexpected Open payload: %q, err=%v", string(got), err)
	}
	if err := storage.Delete(ctx, req); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := storage.Open(ctx, req); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist after delete, got %v", err)
	}
	if err := storage.Delete(ctx, req); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected fs.ErrNotExist on second delete, got %v", err)
	}
	badReq := panelsdk.StorageRequest{
		Key: "../escape.tar.gz",
		Values: map[string]string{
			"folder":       "prod-backups",
			"access-token": "secret-token",
		},
	}
	if err := storage.Upload(ctx, badReq, bytes.NewBufferString("bad")); err == nil {
		t.Fatal("expected traversal key to fail")
	}
}

func TestSummarizeSecretRedactsPlaintext(t *testing.T) {
	s := SummarizeSecret("super-secret-value-1234567890")
	if !s.Configured || s.ByteLength != 29 || len(s.Fingerprint) != 12 {
		t.Fatalf("unexpected secret summary: %+v", s)
	}
	if empty := SummarizeSecret(""); empty.Configured || empty.ByteLength != 0 || empty.Fingerprint != "" {
		t.Fatalf("expected empty summary, got %+v", empty)
	}
}
