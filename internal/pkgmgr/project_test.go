package pkgmgr

import (
	"context"
	"slices"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestListeningProjectPorts(t *testing.T) {
	table := `  sl  local_address rem_address   st
   0: 00000000:1F90 00000000:0000 0A
   1: 0100007F:ABCD 0100007F:0050 01
   2: 00000000000000000000000000000000:1F90 00000000000000000000000000000000:0000 0A
   3: 00000000000000000000000000000000:1388 00000000000000000000000000000000:0000 0A
`
	if got := listeningProjectPorts(table); !slices.Equal(got, []string{"8080", "5000"}) {
		t.Fatalf("监听端口识别错误：%v", got)
	}
}

func TestProjectLogsMergeStreamsByTime(t *testing.T) {
	m := New()
	m.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	m.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
		if !slices.Contains(args, "--timestamps") {
			t.Fatalf("日志命令缺少时间戳：%v", args)
		}
		return executor.Result{
			Stdout: "2026-11-19T00:00:01.000000000Z 启动\n2026-11-19T00:00:03.000000000Z 新请求\n",
			Stderr: "2026-11-19T00:00:02.000000000Z 警告",
		}, nil
	}
	got, err := m.ProjectAction(context.Background(), "example", "logs")
	if err != nil || got != "启动\n警告\n新请求\n" {
		t.Fatalf("合并日志得到 %q，错误 %v", got, err)
	}
}
