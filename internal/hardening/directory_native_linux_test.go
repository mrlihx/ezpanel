//go:build linux

package hardening

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeDirectoryProtection(t *testing.T) {
	if os.Getenv("EZPANEL_TEST_DIRECTORY_GUARD") != "1" {
		t.Skip("仅在专用 Linux 测试环境显式启用")
	}
	root := t.TempDir()
	site := filepath.Join(root, "site")
	if err := os.Mkdir(site, 0700); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(site, "protected.conf")
	if err := os.WriteFile(protected, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	opened, err := os.OpenFile(protected, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	manager, err := New(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	manager.state.Rules = nil
	if !manager.State().DirectorySupported {
		t.Fatal(manager.State().DirectoryNotice)
	}
	rule, err := manager.SaveRule("", RuleInput{Path: site, Category: "critical", Mode: "immutable", Enabled: true, Exclusions: "uploads/**\n*.log"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := manager.SetEnabled(false); err != nil {
			t.Error("清理目录策略", err)
		}
	}()
	if err = manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if status := manager.State().Rules[0].Status; status != "protected" {
		t.Fatalf("目录保护未生效：%s", status)
	}
	if err = os.WriteFile(filepath.Join(site, "blocked.conf"), nil, 0600); err == nil {
		t.Fatal("未排除的新文件未被实时阻止")
	}
	if _, err = opened.WriteAt([]byte("bad"), 0); err == nil {
		t.Fatal("预先打开的文件仍可写入")
	}
	if err = os.Remove(protected); err == nil {
		t.Fatal("保护文件仍可删除")
	}
	if err = os.Chmod(protected, 0644); err == nil {
		t.Fatal("保护文件权限仍可修改")
	}
	logPath := filepath.Join(site, "future.log")
	if err = os.WriteFile(logPath, []byte("allowed"), 0600); err != nil {
		t.Fatal("未来排除文件不能创建", err)
	}
	upload := filepath.Join(site, "uploads", "nested")
	if err = os.MkdirAll(upload, 0700); err != nil {
		t.Fatal("未来排除目录不能创建", err)
	}
	if err = os.WriteFile(filepath.Join(upload, "future.bin"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(logPath, filepath.Join(site, "renamed.conf")); err == nil {
		t.Fatal("排除路径重命名绕过保护")
	}
	if err = os.Rename(protected, filepath.Join(upload, "moved.conf")); err == nil {
		t.Fatal("保护路径移入排除目录绕过保护")
	}
	if err = os.Remove(logPath); err != nil {
		t.Fatal("排除文件不能删除", err)
	}
	if err = os.WriteFile(filepath.Join(root, "site-neighbor"), nil, 0600); err != nil {
		t.Fatal("保护误伤相邻路径", err)
	}
	if err = manager.Scan(); err != nil {
		t.Fatal(err)
	}
	if manager.State().Rules[0].Status != "protected" {
		t.Fatal("排除路径的合法操作触发告警")
	}
	if err = manager.Action(rule.ID, "pause", 5); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(site, "maintenance.conf"), nil, 0600); err != nil {
		t.Fatal("暂停未解除保护", err)
	}
	if err = manager.Action(rule.ID, "resume", 0); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(site, "blocked-again.conf"), nil, 0600); err == nil {
		t.Fatal("恢复后未重新保护")
	}
	if err = manager.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(site, "unlocked.conf"), nil, 0600); err != nil {
		t.Fatal("总开关关闭后仍拦截", err)
	}
}
