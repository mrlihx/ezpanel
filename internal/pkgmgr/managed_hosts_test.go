package pkgmgr

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureManagedHostsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureManagedHostsFile(path); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1 localhost\n" + managedHostsLine + "\n"
	if string(got) != want {
		t.Fatalf("got:\n%q\nwant:\n%q", string(got), want)
	}
	// 再次调用应保持幂等，不重复追加
	if err := ensureManagedHostsFile(path); err != nil {
		t.Fatal(err)
	}
	got2, _ := os.ReadFile(path)
	if strings.Count(string(got2), managedHostsMarker) != 1 {
		t.Fatalf("expected exactly one managed hosts entry, got:\n%s", string(got2))
	}
	// 旧的托管行应被原地更新
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n127.0.0.1 ezpanel-mysql "+managedHostsMarker+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ensureManagedHostsFile(path); err != nil {
		t.Fatal(err)
	}
	got3, _ := os.ReadFile(path)
	if string(got3) != want {
		t.Fatalf("expected updated line:\n%q\nwant:\n%q", string(got3), want)
	}
}
