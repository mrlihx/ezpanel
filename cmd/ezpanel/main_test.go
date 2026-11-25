package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUninitializedDoesNotCreateConfig(t *testing.T) {
	dir := t.TempDir()
	if run([]string{"server", "--dev", "--install-dir", dir}) == nil {
		t.Fatal("uninitialized started")
	}
	if _, e := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(e) {
		t.Fatal("created data without initialization")
	}
}
func TestInitRequiresInteractiveTerminal(t *testing.T) {
	if terminal() {
		t.Skip("test requires redirected stdin")
	}
	dir := t.TempDir()
	if initialize(dir, true) == nil {
		t.Fatal("noninteractive init")
	}
	if _, e := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(e) {
		t.Fatal("init wrote config")
	}
}
func TestCLIRejectsUnknownCommand(t *testing.T) {
	if run([]string{"delete-system", "--dev"}) == nil {
		t.Fatal("unknown command")
	}
}
