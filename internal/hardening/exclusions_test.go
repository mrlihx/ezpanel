package hardening

import (
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/hardening/dirguard"
)

func TestExclusionPatternsMatchFuturePaths(t *testing.T) {
	for _, row := range []struct {
		pattern, path string
		want          bool
	}{{"*.log", "new.log", true}, {"*.log", "sub/new.log", true}, {"**/*.log", "new.log", true}, {"**/*.log", "new/deep/file.log", true}, {"cache/**", "cache/a/b", true}, {"uploads/?.[ab]", "uploads/x.a", true}, {"uploads/?.[ab]", "uploads/xx.a", false}} {
		matcher, err := dirguard.Compile([]string{row.pattern})
		if err != nil {
			t.Fatal(err)
		}
		if got := matcher.Match(row.path); got != row.want {
			t.Errorf("pattern %q path %q got %v", row.pattern, row.path, got)
		}
	}
}
func TestSnapshotIgnoresFutureExcludedPaths(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "keep"), []byte("stable"), 0600)
	before, e := snapshot(root, true, "**/*.log\ncache")
	if e != nil {
		t.Fatal(e)
	}
	os.Mkdir(filepath.Join(root, "cache"), 0700)
	os.WriteFile(filepath.Join(root, "cache", "future"), []byte("ignored"), 0600)
	os.WriteFile(filepath.Join(root, "new.log"), []byte("ignored"), 0600)
	after, e := snapshot(root, true, "**/*.log\ncache")
	if e != nil {
		t.Fatal(e)
	}
	if before != after {
		t.Fatal("excluded future paths changed baseline")
	}
	os.WriteFile(filepath.Join(root, "keep"), []byte("changed"), 0600)
	changed, _ := snapshot(root, true, "**/*.log\ncache")
	if changed == before {
		t.Fatal("non-excluded mutation hidden")
	}
}
