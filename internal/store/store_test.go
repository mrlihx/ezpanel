package store

import (
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"
)

func TestStore(t *testing.T) {
	p := filepath.Join(t.TempDir(), "db")
	db, e := Open(p)
	if e != nil {
		t.Fatal(e)
	}
	if !db.Migrator().HasTable("users") || !db.Migrator().HasTable("websites") {
		t.Fatal("expected core tables to exist")
	}
	if db.Migrator().HasTable("op_logs") || db.Migrator().HasTable("schema_migrations") {
		t.Fatal("removed tables should not be created")
	}
	s, _ := db.DB()
	s.Close()

	// Re-opening must be idempotent
	db, e = Open(p)
	if e != nil {
		t.Fatal(e)
	}
	s, _ = db.DB()
	defer s.Close()

	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	a, e := Encrypt(key, "secret")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := Encrypt(key, "secret")
	if a == b {
		t.Fatal("nonce reused")
	}
	if v, e := Decrypt(key, a); e != nil || v != "secret" {
		t.Fatal(v, e)
	}
	bad := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("y", 32)))
	if _, e := Decrypt(bad, a); e == nil {
		t.Fatal("wrong key")
	}
}
