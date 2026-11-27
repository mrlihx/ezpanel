package pkgmgr

import (
	"context"
	"errors"
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
		if args[0] == "ps" {
			return executor.Result{}, nil
		}
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

func TestProjectContainerNameAndRollback(t *testing.T) {
	m := New()
	m.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	m.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
		switch {
		case args[0] == "ps":
			if slices.Contains(args, "label=ezpanel.project-id=website") {
				return executor.Result{Stdout: "python-z2z2z2-failed\npython-y2y2y2-previous\npython-x2x2x2\n"}, nil
			}
			return executor.Result{}, nil
		case slices.Equal(args, []string{"rename", "python-y2y2y2-previous", "python-y2y2y2"}):
			return executor.Result{}, nil
		default:
			return executor.Result{}, errors.New("意外的 Docker 命令")
		}
	}
	for id, want := range map[string]string{
		"website":        "python-x2x2x2",
		"website-failed": "python-z2z2z2-failed",
		"legacy":         "ezpanel-site-legacy",
	} {
		if got, err := m.projectContainerName(context.Background(), id); err != nil || got != want {
			t.Fatalf("项目 %s 容器 = %q，期望 %q，错误 %v", id, got, want, err)
		}
	}
	if err := m.RenameProject(context.Background(), "website-previous", "website"); err != nil {
		t.Fatal(err)
	}
}

func TestRunningProjectsIncludesLegacyAndShortNames(t *testing.T) {
	m := New()
	m.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	m.Run = func(context.Context, time.Duration, []string, string, ...string) (executor.Result, error) {
		return executor.Result{Stdout: "go-abcd23\tgo-site\nezpanel-site-legacy\t\npython-bcde34-previous\told-site\n"}, nil
	}
	rows := m.RunningProjects(context.Background())
	if len(rows) != 2 || !rows["go-site"] || !rows["legacy"] {
		t.Fatalf("运行中项目归属错误：%v", rows)
	}
}
