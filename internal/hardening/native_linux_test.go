//go:build linux

package hardening

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeFileProtection(t *testing.T) {
	if os.Getenv("EZPANEL_TEST_FILE_FLAGS") != "1" {
		t.Skip("仅在专用 Linux 测试环境显式启用")
	}
	for _, mode := range []string{"immutable", "append"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "protected.conf")
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			original, _, err := fileFlags(path, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, _, err := fileFlags(path, &original); err != nil {
					t.Error("清理测试文件属性", err)
				}
			}()
			manager, err := New(filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			manager.state.Rules = nil
			rule, err := manager.SaveRule("", RuleInput{Path: path, Category: "critical", Mode: mode, Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.SetEnabled(true); err != nil {
				t.Fatal(err)
			}
			if manager.State().Rules[0].Status != "protected" {
				t.Fatalf("保护未生效: %+v", manager.State().Rules[0])
			}
			if err = os.WriteFile(path, []byte("replacement"), 0600); err == nil {
				t.Fatal("保护后仍可覆盖")
			}
			if err = os.Remove(path); err == nil {
				t.Fatal("保护后仍可删除")
			}
			if mode == "append" {
				file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString(" appended")
				file.Close()
				if err != nil {
					t.Fatal(err)
				}
				if err = manager.Scan(); err != nil {
					t.Fatal(err)
				}
				if manager.State().Rules[0].Status != "protected" {
					t.Fatal("合法追加被误报")
				}
			}
			if err = manager.Action(rule.ID, "pause", 5); err != nil {
				t.Fatal(err)
			}
			actual, _, err := fileFlags(path, nil)
			if err != nil || actual != original {
				t.Fatalf("暂停未恢复原属性: flags=%x err=%v", actual, err)
			}
			if err = manager.Action(rule.ID, "resume", 0); err != nil {
				t.Fatal(err)
			}
			// 重新打开持久化状态，模拟面板不可用时的本机恢复。
			restored, err := New(filepath.Join(root, "state"))
			if err != nil {
				t.Fatal(err)
			}
			if err = restored.Recover(); err != nil {
				t.Fatal(err)
			}
			if restored.State().Enabled {
				t.Fatal("恢复后总开关仍开启")
			}
			actual, _, err = fileFlags(path, nil)
			if err != nil || actual != original {
				t.Fatalf("恢复未保留原属性: flags=%x err=%v", actual, err)
			}
			if err = os.WriteFile(path, []byte("maintained"), 0600); err != nil {
				t.Fatal("解除后不可编辑", err)
			}
			if err = restored.DeleteRule(rule.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(path); err != nil {
				t.Fatal("移除规则删除了文件", err)
			}
		})
	}
}
