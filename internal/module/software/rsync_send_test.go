package software

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
)

func TestRsyncSenderSyncOptions(t *testing.T) {
	m := &Module{cfg: config.Config{InstallDir: t.TempDir(), AESKey: base64.StdEncoding.EncodeToString(make([]byte, 32))}}
	ruleID := store.ID()
	in := rsyncWorkspace{Senders: []rsyncSender{{Name: "同步", Source: t.TempDir(), Destination: "rsync://192.0.2.1:873/sync_worker", Username: "sync_worker", Password: "test-secret", RuleID: ruleID, Mode: "full", BandwidthKB: 1024, Schedule: "realtime"}}, Rules: []rsyncRule{{ID: ruleID, Name: "缓存", Patterns: "cache/\n*.log"}}}
	if err := m.prepareRsyncWorkspace(&in, rsyncWorkspace{}); err != nil {
		t.Fatal(err)
	}
	row := in.Senders[0]
	if row.Password != "" || in.Secrets[row.ID] == "" || strings.Contains(in.Secrets[row.ID], "test-secret") {
		t.Fatal("发送凭据未加密保存")
	}
	args := rsyncSendArgs(row, in, true)
	for _, required := range []string{"--delete-delay", "--bwlimit=1024", "--contimeout=15", "--exclude=cache/", "--exclude=*.log"} {
		if !slices.Contains(args, required) {
			t.Errorf("同步参数缺少 %q", required)
		}
	}
	row.Mode, row.BandwidthKB = "incremental", 0
	args = rsyncSendArgs(row, in, false)
	for _, arg := range args {
		if strings.HasPrefix(arg, "--delete") || strings.HasPrefix(arg, "--bwlimit") || strings.HasPrefix(arg, "--contimeout") {
			t.Fatalf("增量本机同步的参数错误：%q", arg)
		}
	}
	old := in
	in.Senders = append([]rsyncSender(nil), old.Senders...)
	in.Senders[0].CheckMinutes = -1
	if err := m.prepareRsyncWorkspace(&in, old); err == nil {
		t.Fatal("未拒绝无效检查周期")
	}
}

func TestRsyncSenderDefaultsRemainManual(t *testing.T) {
	var row rsyncSender
	normalizeRsyncSender(&row)
	if row.Mode != "incremental" || row.Schedule != "interval" || row.CheckMinutes != 0 {
		t.Fatalf("已有发送任务应保持手动执行：%+v", row)
	}
	target, err := rsyncDaemonTarget("rsync://[2001:db8::1]:873/sync_worker", "sync_worker")
	if err != nil || target != "rsync://sync_worker@[2001:db8::1]:873/sync_worker/" {
		t.Fatalf("内部接收地址错误：%q %v", target, err)
	}
}
