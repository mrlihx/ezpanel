package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestPackKeepsExistingArchiveOnFailure(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir)
	output := filepath.Join(t.TempDir(), "previous.zip")
	if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pack(dir, output, true, false); err == nil {
		t.Fatal("expected invalid package to fail")
	}
	data, err := os.ReadFile(output)
	if err != nil || string(data) != "previous" {
		t.Fatalf("previous archive lost: %q, %v", data, err)
	}
}

func TestPackExcludesOwnOutput(t *testing.T) {
	dir := t.TempDir()
	writePackage(t, dir)
	output := filepath.Join(dir, "web", "previous.zip")
	if err := os.WriteFile(output, []byte("previous"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pack(dir, output, true, false); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var names []string
	for _, file := range archive.File {
		names = append(names, file.Name)
	}
	if !slices.Equal(names, []string{"plugin.yaml", "web/entry.js"}) {
		t.Fatalf("unexpected files: %v", names)
	}
}

func writePackage(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "web"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "web", "entry.js"), []byte("export {};"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte("id: test-package\nname: Test\nversion: '1.0.0'\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
