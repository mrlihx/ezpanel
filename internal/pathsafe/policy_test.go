package pathsafe

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicy(t *testing.T) {
	root := t.TempDir()
	panel := filepath.Join(root, "panel")
	os.Mkdir(panel, 0700)
	p, e := New(panel)
	if e != nil {
		t.Fatal(e)
	}
	if Contains(panel, panel+"2") {
		t.Fatal("prefix collision")
	}
	if _, e = p.Check(panel, false); e == nil {
		t.Fatal("panel readable")
	}
	if e = p.CheckTree(root); e == nil {
		t.Fatal("ancestor archive")
	}
	link := filepath.Join(root, "link")
	os.Symlink(panel, link)
	if _, e = p.Check(link, false); e == nil {
		t.Fatal("link read")
	}
	if _, e = p.CheckLink(link, true); e != nil {
		t.Fatal(e)
	}
	if _, e = p.Check(filepath.Join(link, "new"), true); e == nil {
		t.Fatal("new parent link")
	}
	if _, e = p.Check("/", true); e == nil {
		t.Fatal("root chmod")
	}
}

func TestDanglingLinks(t *testing.T) {
	root := t.TempDir()
	panel := filepath.Join(root, "panel")
	if e := os.Mkdir(panel, 0700); e != nil {
		t.Fatal(e)
	}
	p, e := New(panel)
	if e != nil {
		t.Fatal(e)
	}
	for _, tc := range []struct{ name, target, child string }{{"file", filepath.Join(panel, "missing-file"), ""}, {"parent", filepath.Join(panel, "missing-dir"), "new-file"}} {
		t.Run(tc.name, func(t *testing.T) {
			link := filepath.Join(root, tc.name)
			if e := os.Symlink(tc.target, link); e != nil {
				t.Fatal(e)
			}
			path := link
			if tc.child != "" {
				path = filepath.Join(path, tc.child)
			}
			if _, e := p.Check(path, true); e == nil {
				t.Fatal("dangling link bypassed panel protection")
			}
			if _, e := p.CheckLink(link, true); e != nil {
				t.Fatal("link itself cannot be removed", e)
			}
		})
	}
}
