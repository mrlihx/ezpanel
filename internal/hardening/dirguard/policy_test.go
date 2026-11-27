package dirguard

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestNestedMountAnchors(t *testing.T) {
	got := mountAnchors("1 2 8:1 / /srv/site rw - ext4 /dev/vda1 rw\n2 1 8:2 / /srv/site/uploads rw - ext4 /dev/vdb rw\n3 1 8:3 / /srv/site-neighbor rw - ext4 /dev/vdc rw\n4 1 8:4 / /srv/site/a\\040b rw - ext4 /dev/vdd rw", "/srv/site")
	want := []string{"/srv/site", "/srv/site/uploads", "/srv/site/a b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("锚点=%v", got)
	}
}
func TestCanonicalStateDirectory(t *testing.T) {
	root := t.TempDir()
	path, err := canonicalStatePath(filepath.Join(root, "not-created", "state"))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(resolved, "not-created", "state") {
		t.Fatal(path)
	}
}
