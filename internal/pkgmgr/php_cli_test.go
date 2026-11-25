package pkgmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPHPCLICommandName(t *testing.T) {
	if got := phpCLICommandName("php-8.3"); got != "php83" {
		t.Fatalf("got %q, want php83", got)
	}
	if got := phpCLICommandName("php-7.4"); got != "php74" {
		t.Fatalf("got %q, want php74", got)
	}
	if got := phpCLICommandName("invalid"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestPHPCLIWrapperAndDetection(t *testing.T) {
	dir := t.TempDir()
	wrapper := filepath.Join(dir, "php83")
	content := phpCLIWrapperContent("php-8.3", "/www/wwwroot")
	if !strings.Contains(content, `container="ezpanel-php-8.3"`) {
		t.Fatalf("wrapper missing container name: %s", content)
	}
	if !strings.Contains(content, "-u www-data") {
		t.Fatalf("wrapper missing www-data user: %s", content)
	}
	if err := os.WriteFile(wrapper, []byte(content), 0755); err != nil {
		t.Fatal(err)
	}
	id, ok := isManagedPHPCLIFile(wrapper)
	if !ok || id != "php-8.3" {
		t.Fatalf("isManagedPHPCLIFile(wrapper) = (%q, %v), want (php-8.3, true)", id, ok)
	}
	link := filepath.Join(dir, "php")
	if err := os.Symlink(wrapper, link); err != nil {
		t.Fatal(err)
	}
	id, ok = isManagedPHPCLIFile(link)
	if !ok || id != "php-8.3" {
		t.Fatalf("isManagedPHPCLIFile(link) = (%q, %v), want (php-8.3, true)", id, ok)
	}
	unmanaged := filepath.Join(dir, "custom")
	if err := os.WriteFile(unmanaged, []byte("#!/bin/sh\necho hi\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, ok := isManagedPHPCLIFile(unmanaged); ok {
		t.Fatal("expected unmanaged script to return false")
	}
}
