package pkgmgr

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"ezpanel/internal/config"
)

func TestRsyncReceiverCredentialsAndDirectoryOwner(t *testing.T) {
	m := New()
	m.Configure(config.Config{InstallDir: t.TempDir()})
	directory := t.TempDir()
	in := RsyncVisual{RsyncGlobal: RsyncGlobal{Address: "0.0.0.0", Port: 873}, Receivers: []RsyncReceiver{
		{Name: "old_module", Username: "sync_user", Password: "test-secret", Path: directory, Enabled: true, Allow: "192.0.2.0/24\n2001:db8::1"},
	}}
	raw := "address = 127.0.0.1\nport = 873\nmotd file = /tmp/welcome\n"
	text, secrets, err := m.renderRsyncVisual(raw, "", in)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	owner := info.Sys().(*syscall.Stat_t)
	for _, required := range []string{"address = 0.0.0.0", "[old_module]", "[sync_user]", "read only = false", "write only = true", "hosts allow = 192.0.2.0/24 2001:db8::1", "hosts deny = *", "motd file = /tmp/welcome", fmt.Sprintf("uid = %d\ngid = %d", owner.Uid, owner.Gid), filepath.Join(m.layout().RsyncLogDir(), "rsyncd.log")} {
		if !strings.Contains(text, required) {
			t.Errorf("接收配置缺少 %q", required)
		}
	}
	if strings.Contains(text, "test-secret") || strings.Contains(text, "refuse options = delete") || secrets != "sync_user:test-secret\n" {
		t.Fatal("接收配置泄漏密码或阻止完全同步")
	}
	parsed, err := parseRsyncVisual(Settings{Content: text})
	if err != nil || len(parsed.Receivers) != 1 || parsed.Receivers[0].Password != "" {
		t.Fatalf("接收任务元数据错误：%+v %v", parsed, err)
	}
	in.Receivers[0].Name = ""
	in.Receivers[0].Password = ""
	text, _, err = m.renderRsyncVisual(raw, secrets, in)
	if err != nil || !strings.Contains(text, "[sync_user]") || strings.Contains(text, "[old_module]") {
		t.Fatalf("自动任务标识或密码沿用错误：%v", err)
	}
	in.Receivers[0].Allow = "not-an-ip"
	if _, _, err := m.renderRsyncVisual(raw, secrets, in); err == nil {
		t.Fatal("未拒绝错误白名单")
	}
}
