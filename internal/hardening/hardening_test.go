package hardening

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLargeFileProtectionDoesNotRequireSnapshot(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.state.Rules = nil
	path := filepath.Join(t.TempDir(), "large.log")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 33<<20); err != nil {
		t.Fatal(err)
	}
	flags := uint32(0)
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			flags = *set
		}
		return flags, "large-file", nil
	}
	if _, err := m.SaveRule("", RuleInput{Path: path, Category: "critical", Mode: "immutable", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := m.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	rule := m.State().Rules[0]
	if flags&0x10 == 0 || rule.Status != "protected" || rule.Baseline != nil || !strings.Contains(rule.Message, "32 MiB") {
		t.Fatalf("expected protection with a snapshot limit warning, got flags=%x, rule=%+v", flags, rule)
	}
}

func TestDefaultOffAndBaseline(t *testing.T) {
	m, e := New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if m.State().Enabled {
		t.Fatal("default enabled")
	}
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("before"), 0600)
	r, e := m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "monitor", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	os.WriteFile(p, []byte("after"), 0600)
	m.Scan()
	for _, v := range m.State().Rules {
		if v.ID == r.ID && v.Status != "changed" {
			t.Fatalf("status %s", v.Status)
		}
	}
	m.SetEnabled(false)
	if m.State().Enabled {
		t.Fatal("still enabled")
	}
}

func TestOwnedFlagsAndPause(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "config")
	os.WriteFile(p, []byte("test"), 0600)
	flags := uint32(0x80)
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			flags = *set
		}
		return flags, "inode-one", nil
	}
	r, e := m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "immutable", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	if flags != 0x80 {
		t.Fatal("applied while global off")
	}
	if e = m.SetEnabled(true); e != nil {
		t.Fatal(e)
	}
	if flags != 0x90 {
		t.Fatalf("flags %x", flags)
	}
	if e = m.Action(r.ID, "pause", 10); e != nil {
		t.Fatal(e)
	}
	if flags != 0x80 {
		t.Fatal("pause did not restore")
	}
	if e = m.Action(r.ID, "resume", 0); e != nil {
		t.Fatal(e)
	}
	if flags != 0x90 {
		t.Fatal("resume failed")
	}
	if e = m.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
	if flags != 0x80 {
		t.Fatal("original flags lost")
	}
	// 用户已有的保护属性不归面板所有，关闭时不能删除。
	flags = 0x90
	m.SetEnabled(true)
	m.SetEnabled(false)
	if flags != 0x90 {
		t.Fatal("removed preexisting immutable")
	}
}
func TestOfflineScanAndSymlink(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("before"), 0600)
	r, e := m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "monitor", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	m.Scan()
	os.WriteFile(p, []byte("after"), 0600)
	m.Scan()
	if m.State().Enabled || m.State().Rules[0].Status != "changed" {
		t.Fatal("offline inspection failed")
	}
	link := p + "link"
	os.Symlink(p, link)
	if _, e = m.SaveRule("", RuleInput{Path: link, Category: "critical", Mode: "monitor"}); e == nil {
		t.Fatal("accepted symlink")
	}
	if e = m.Action(r.ID, "baseline", 0); e != nil {
		t.Fatal(e)
	}
}

func TestRestartRestoresExpiredPause(t *testing.T) {
	dir := t.TempDir()
	m, _ := New(dir)
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("test"), 0600)
	flags := uint32(0)
	fake := func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			flags = *set
		}
		return flags, "one", nil
	}
	m.flags = fake
	r, _ := m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "immutable", Enabled: true})
	m.SetEnabled(true)
	m.Action(r.ID, "pause", 1)
	past := time.Now().Add(-time.Minute)
	m.state.Rules[0].PausedUntil = &past
	m.save()
	next, e := New(dir)
	if e != nil {
		t.Fatal(e)
	}
	next.flags = fake
	next.Scan()
	if flags&0x10 == 0 || next.State().Rules[0].Status != "protected" {
		t.Fatal("expired pause not restored")
	}
	next.SetEnabled(false)
}
func TestReplacedFileIsNotTouchedOnRecovery(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("test"), 0600)
	id := "one"
	sets := 0
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			sets++
		}
		return 0, id, nil
	}
	m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "immutable", Enabled: true})
	m.SetEnabled(true)
	id = "two"
	if e := m.Recover(); e == nil {
		t.Fatal("replaced file must require review")
	}
	if sets != 1 {
		t.Fatal("modified replacement inode")
	}
}

func TestMissingRuleCanDisable(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("x"), 0600)
	r, _ := m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "monitor", Enabled: true})
	os.Remove(p)
	in := r.RuleInput
	in.Enabled = false
	if _, e := m.SaveRule(r.ID, in); e != nil {
		t.Fatal(e)
	}
}
func TestAppendPrefix(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("original"), 0600)
	m.flags = func(_ string, set *uint32) (uint32, string, error) { return 0, "one", nil }
	m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "append", Enabled: true})
	m.SetEnabled(true)
	os.WriteFile(p, []byte("original appended"), 0600)
	m.Scan()
	if m.State().Rules[0].Status != "protected" {
		t.Fatal("legitimate append flagged")
	}
	os.WriteFile(p, []byte("modified appended"), 0600)
	m.Scan()
	if m.State().Rules[0].Status != "changed" {
		t.Fatal("prefix edit not detected")
	}
}
func TestOfflineFailureRecorded(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("x"), 0600)
	m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "monitor", Enabled: true})
	os.Remove(p)
	m.Scan()
	s := m.State()
	if s.Rules[0].Status != "missing" || s.Events[0].Action != "check" {
		t.Fatal("offline failure hidden")
	}
	m.state.Rules[0].AddedFlags = 16
	m.state.Rules[0].Status = "error"
	m.state.Rules[0].Message = "unresolved restore"
	m.Scan()
	if m.State().Rules[0].Message != "unresolved restore" {
		t.Fatal("restore error lost")
	}
}
func TestDefaultIDsPersist(t *testing.T) {
	dir := t.TempDir()
	a, _ := New(dir)
	b, _ := New(dir)
	if len(a.State().Rules) > 0 && a.State().Rules[0].ID != b.State().Rules[0].ID {
		t.Fatal("default IDs changed")
	}
}
func TestFailedSetCanDisable(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("x"), 0600)
	m.flags = func(_ string, set *uint32) (uint32, string, error) {
		if set != nil {
			return 0, "one", os.ErrPermission
		}
		return 0, "one", nil
	}
	m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "immutable", Enabled: true})
	m.SetEnabled(true)
	if e := m.SetEnabled(false); e != nil {
		t.Fatal(e)
	}
}
func TestRepeatedDriftProducesEvents(t *testing.T) {
	m, _ := New(t.TempDir())
	m.state.Rules = nil
	p := filepath.Join(t.TempDir(), "file")
	os.WriteFile(p, []byte("one"), 0600)
	m.SaveRule("", RuleInput{Path: p, Category: "critical", Mode: "monitor", Enabled: true})
	m.Scan()
	os.WriteFile(p, []byte("two"), 0600)
	m.Scan()
	before := len(m.State().Events)
	os.WriteFile(p, []byte("three"), 0600)
	m.Scan()
	if len(m.State().Events) != before+1 {
		t.Fatal("distinct drift missing")
	}
	m.Scan()
	if len(m.State().Events) != before+1 {
		t.Fatal("duplicate drift event")
	}
}

func TestNewRulesAppearFirst(t *testing.T) {
	m, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	var ids []string
	for _, name := range []string{"first", "second"} {
		path := filepath.Join(root, name)
		if err = os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		rule, err := m.SaveRule("", RuleInput{Path: path, Category: "critical", Mode: "monitor", Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rule.ID)
	}
	if m.State().Rules[0].ID != ids[1] {
		t.Fatal("最新规则应排在最前")
	}
	old := m.State().Rules[1]
	old.Remark = "edited"
	if _, err = m.SaveRule(old.ID, old.RuleInput); err != nil {
		t.Fatal(err)
	}
	restored, err := New(filepath.Dir(m.path))
	if err != nil {
		t.Fatal(err)
	}
	if restored.State().Rules[0].ID != ids[1] {
		t.Fatal("编辑和重启不应打乱新增顺序")
	}
}
