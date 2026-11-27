package hardening

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeDirectory struct {
	policies []DirectoryPolicy
	fail     bool
}

func (f *fakeDirectory) Supported() (bool, string) { return true, "test backend" }
func (f *fakeDirectory) Sync(p []DirectoryPolicy) error {
	if f.fail {
		return errors.New("attach failed")
	}
	f.policies = p
	return nil
}

func TestDirectoryFailureDoesNotInterruptFileMonitoring(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.state.Rules = nil
	backend := &fakeDirectory{}
	if err := m.SetDirectoryBackend(backend); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveRule("", RuleInput{Path: t.TempDir(), Category: "critical", Mode: "immutable", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveRule("", RuleInput{Path: path, Category: "critical", Mode: "monitor", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	if err := m.Scan(); err == nil {
		t.Fatal("expected directory synchronization to fail")
	}
	rules := m.State().Rules
	if rules[0].Status != "changed" || rules[1].Status != "error" {
		t.Fatalf("expected file change and directory error, got %+v", rules)
	}
}
func TestDirectoryPolicyLifecycle(t *testing.T) {
	m, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	m.state.Rules = nil
	f := &fakeDirectory{}
	m.SetDirectoryBackend(f)
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "config"), []byte("test"), 0600)
	r, e := m.SaveRule("", RuleInput{Path: root, Category: "critical", Mode: "immutable", Enabled: true, Exclusions: "*.log\ncache/**"})
	if e != nil {
		t.Fatal(e)
	}
	if len(f.policies) != 0 {
		t.Fatal("directory protection active while off")
	}
	if !r.Recursive || !r.Directory {
		t.Fatal("directory must protect subtree")
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	if len(f.policies) != 1 || m.State().Rules[0].Status != "protected" {
		t.Fatal("directory not protected")
	}
	if len(f.policies[0].Exclusions) != 2 {
		t.Fatal("exclusions not synced")
	}
	m.Action(r.ID, "pause", 1)
	if len(f.policies) != 0 {
		t.Fatal("pause didn't clear policy")
	}
	m.Action(r.ID, "resume", 0)
	if len(f.policies) != 1 {
		t.Fatal("resume failed")
	}
	m.DeleteRule(r.ID)
	if len(f.policies) != 0 {
		t.Fatal("delete didn't clear policy")
	}
	r, _ = m.SaveRule("", RuleInput{Path: root, Category: "critical", Mode: "immutable", Enabled: true})
	m.SetEnabled(false)
	if len(f.policies) != 0 {
		t.Fatal("global off didn't clear policy")
	}
	m.SetEnabled(true)
	f.fail = true
	if e = m.Scan(); e == nil || m.State().Rules[0].Status != "error" {
		t.Fatal("backend failure claimed protected")
	}
	_ = r
}

func TestFailedPolicyUpdateDoesNotClaimPauseOrForgetRule(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	backend := &fakeDirectory{}
	m.SetDirectoryBackend(backend)
	r, e := m.SaveRule("", RuleInput{Path: t.TempDir(), Category: "critical", Mode: "immutable", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	m.SetEnabled(true)
	backend.fail = true
	if e = m.Action(r.ID, "pause", 1); e == nil || m.State().Rules[0].Status != "error" {
		t.Fatal("failed backend removal claimed pause")
	}
	if e = m.DeleteRule(r.ID); e == nil || len(m.State().Rules) != 1 {
		t.Fatal("failed backend deletion forgot policy")
	}
	if e = m.SetEnabled(false); e == nil || m.State().Rules[0].Status != "error" {
		t.Fatal("failed global removal claimed off")
	}
}

func TestDirectoryEditFailurePreservesOriginalPolicy(t *testing.T) {
	for _, variant := range []string{"file", "monitor", "path"} {
		t.Run(variant, func(t *testing.T) {
			m, _ := New(t.TempDir())
			m.state.Rules = nil
			backend := &fakeDirectory{}
			m.SetDirectoryBackend(backend)
			original := t.TempDir()
			r, e := m.SaveRule("", RuleInput{Path: original, Category: "critical", Mode: "immutable", Enabled: true})
			if e != nil {
				t.Fatal(e)
			}
			m.SetEnabled(true)
			input := r.RuleInput
			switch variant {
			case "file":
				input.Path = filepath.Join(t.TempDir(), "file")
				os.WriteFile(input.Path, []byte("test"), 0600)
				input.Recursive = false
				input.Mode = "monitor"
			case "monitor":
				input.Mode = "monitor"
			case "path":
				input.Path = t.TempDir()
			}
			backend.fail = true
			if _, e = m.SaveRule(r.ID, input); e == nil {
				t.Fatal("backend failure ignored")
			}
			state := m.State()
			if len(state.Rules) != 1 || state.Rules[0].Path != original || state.Rules[0].Mode != "immutable" {
				t.Fatal("original kernel policy forgotten")
			}
			loaded, _ := New(filepath.Dir(m.path))
			if loaded.State().Rules[0].Path != original {
				t.Fatal("rollback not persisted")
			}
		})
	}
}

func TestFailedDirectorySyncDoesNotOrphanNewFileFlags(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	backend := &fakeDirectory{}
	if err := m.SetDirectoryBackend(backend); err != nil {
		t.Fatal(err)
	}
	rule, err := m.SaveRule("", RuleInput{Path: t.TempDir(), Category: "critical", Mode: "immutable", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "replacement")
	if err = os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := uint32(0)
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			flags = *set
		}
		return flags, "file", nil
	}
	backend.fail = true
	_, err = m.SaveRule(rule.ID, RuleInput{Path: file, Category: "critical", Mode: "immutable", Enabled: true})
	if err == nil {
		t.Fatal("应报告目录同步失败")
	}
	if flags != 0 {
		t.Fatal("目录同步失败后不应施加无法追踪的新文件属性", flags)
	}
	if m.State().Rules[0].ID != rule.ID || !m.State().Rules[0].Directory {
		t.Fatal("旧目录规则应保留")
	}
}

func TestFailedDirectoryEditRestoresOldFileProtection(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	backend := &fakeDirectory{}
	if err := m.SetDirectoryBackend(backend); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "original")
	if err := os.WriteFile(file, []byte("content"), 0600); err != nil {
		t.Fatal(err)
	}
	flags := uint32(0)
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			flags = *set
		}
		return flags, "file", nil
	}
	rule, err := m.SaveRule("", RuleInput{Path: file, Category: "critical", Mode: "immutable", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	backend.fail = true
	_, err = m.SaveRule(rule.ID, RuleInput{Path: t.TempDir(), Category: "critical", Mode: "immutable", Enabled: true})
	if err == nil {
		t.Fatal("应报告目录同步失败")
	}
	restored := m.State().Rules[0]
	if flags&0x10 == 0 || restored.AddedFlags&0x10 == 0 {
		t.Fatal("失败编辑后应恢复旧文件保护及恢复依据")
	}
}
