package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfig(t *testing.T) {
	c, e := New(filepath.Join(t.TempDir(), "panel"))
	if e != nil {
		t.Fatal(e)
	}
	d, _ := New(t.TempDir())
	if c.JWTSecret == d.JWTSecret || c.Entry == d.Entry {
		t.Fatal("random required")
	}
	if e = Save(c); e != nil {
		t.Fatal(e)
	}
	b, e := Load(c.InstallDir)
	if e != nil || b.AESKey != c.AESKey || b.ListenAddr != "0.0.0.0" || b.PublicURL != "" {
		t.Fatalf("load did not preserve fresh setup: %v", e)
	}
	s, _ := os.Stat(filepath.Join(c.DataDir(), "config.yaml"))
	if s.Mode().Perm() != 0600 {
		t.Fatal(s.Mode())
	}
	c.ListenAddr = "0.0.0.0"
	c.PublicURL = "http://192.0.2.10:8080"
	c.Dev = true
	if e = Validate(c); e != nil {
		t.Fatal(e)
	}
	c.Timezone = "bad"
	if Validate(c) == nil {
		t.Fatal("bad timezone")
	}
}

func TestPanelEntryFormat(t *testing.T) {
	cfg, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"12345", "login", "Abc12", "aMuchLonger123"} {
		cfg.Entry = entry
		if err := Validate(cfg); err != nil {
			t.Errorf("entry %q rejected: %v", entry, err)
		}
	}
	for _, entry := range []string{"1234", "pass-word", "pass word", "abc/12"} {
		cfg.Entry = entry
		if Validate(cfg) == nil {
			t.Errorf("entry %q accepted", entry)
		}
	}
}

func TestIdleTimeoutHours(t *testing.T) {
	cfg, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IdleMinutes != 60 {
		t.Fatal("默认应为1小时", cfg.IdleMinutes)
	}
	for _, minutes := range []int{60, 120, 168 * 60} {
		cfg.IdleMinutes = minutes
		if err := Validate(cfg); err != nil {
			t.Errorf("%d rejected: %v", minutes, err)
		}
	}
	for _, minutes := range []int{0, 1, 30, 59, 61, 168*60 + 60} {
		cfg.IdleMinutes = minutes
		if Validate(cfg) == nil {
			t.Errorf("%d accepted", minutes)
		}
	}
}
func TestLegacyIdleTimeoutRoundsUp(t *testing.T) {
	for _, sample := range []struct{ old, want int }{{1, 60}, {30, 60}, {60, 60}, {90, 120}, {720, 720}} {
		cfg, err := New(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err = Save(cfg); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(cfg.DataDir(), "config.yaml")
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Replace(string(raw), "idleMinutes: 60", fmt.Sprintf("idleMinutes: %d", sample.old), 1)
		if err = os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		loaded, err := Load(cfg.InstallDir)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.IdleMinutes != sample.want {
			t.Errorf("%d -> %d, want %d", sample.old, loaded.IdleMinutes, sample.want)
		}
	}
}
