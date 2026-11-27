package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestRemoveDatabaseInstanceChecksContents(t *testing.T) {
	for _, tc := range []struct {
		name, engine, instance, result, wantError string
		running, probeFails                       bool
	}{
		{"默认实例有库", "postgresql", "default", "2\n", "请先删除全部数据库", true, false},
		{"附加实例有库", "mongodb", "bbbb22", "1\n", "请先删除全部数据库", true, false},
		{"默认实例已清空", "postgresql", "default", "0\n", "", true, false},
		{"附加实例已清空", "mongodb", "bbbb22", "0\n", "", true, false},
		{"实例已停止", "postgresql", "bbbb22", "", "请先启动实例", false, false},
		{"检查失败", "mongodb", "bbbb22", "0\n", "无法检查实例", true, true},
		{"Redis 有键", "redis", "bbbb22", "# Keyspace\r\ndb1:keys=1,expires=0,avg_ttl=0\r\n", "请先清空", true, false},
		{"Redis 已清空", "redis", "bbbb22", "# Keyspace\r\n", "", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			m.LookPath = func(name string) (string, error) { return name, nil }
			var info containerInfo
			info.ID = "target-container"
			info.Config.Labels = map[string]string{"ezpanel.managed": "true", "ezpanel.database": tc.engine}
			info.State.Running = tc.running
			inspection, err := json.Marshal([]containerInfo{info})
			if err != nil {
				t.Fatal(err)
			}
			removed, checked := false, false
			m.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
				switch args[0] {
				case "container":
					if removed {
						return executor.Result{Stderr: "No such container"}, errors.New("not found")
					}
					return executor.Result{Stdout: string(inspection)}, nil
				case "exec":
					if !tc.running || args[1] != info.ID {
						t.Fatalf("unexpected command %v", args)
					}
					checked = true
					if tc.probeFails {
						return executor.Result{Stdout: tc.result}, errors.New("query failed")
					}
					return executor.Result{Stdout: tc.result}, nil
				case "rm":
					if !checked || tc.wantError != "" || args[2] != info.ID {
						t.Fatalf("unexpected removal %v", args)
					}
					removed = true
				case "ps":
				default:
					t.Fatalf("unexpected command %v", args)
				}
				return executor.Result{}, nil
			}
			err = m.DatabaseInstanceAction(context.Background(), tc.engine, tc.instance, "remove")
			if tc.wantError == "" {
				if err != nil || !removed {
					t.Fatalf("empty instance was not removed: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("want %q, got %v", tc.wantError, err)
			}
		})
	}
}

func TestRunningDatabaseInstanceMatchesIDAndPublishedPort(t *testing.T) {
	m := New()
	const first = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const second = "bbbb22"
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	m.Run = func(_ context.Context, _ time.Duration, _ []string, name string, args ...string) (executor.Result, error) {
		if name != "/usr/bin/docker" || len(args) != 3 || args[0] != "container" || args[1] != "inspect" {
			t.Fatalf("unexpected command %s %v", name, args)
		}
		id := strings.TrimPrefix(args[2], "ezpanel-mysql-")
		ports := map[string]string{first: "33060", second: "33061"}
		data, err := json.Marshal([]any{map[string]any{
			"Id": id,
			"Config": map[string]any{"Labels": map[string]string{
				"ezpanel.managed": "true", "ezpanel.database": "mysql",
				"ezpanel.instance-name": "同名实例",
			}},
			"HostConfig": map[string]any{"PortBindings": map[string]any{
				"3306/tcp": []map[string]string{{"HostIP": "127.0.0.1", "HostPort": ports[id]}},
			}},
			"State": map[string]bool{"Running": true},
		}})
		return executor.Result{Stdout: string(data)}, err
	}
	ctx := context.Background()
	for id, port := range map[string]int{first: 33060, second: 33061} {
		row, err := m.RunningDatabaseInstance(ctx, "mysql", id, port)
		if err != nil || row.ContainerID != id || row.Port != port {
			t.Fatalf("matching connection rejected: %+v %v", row, err)
		}
	}
	if _, err := m.RunningDatabaseInstance(ctx, "mysql", first, 33061); err == nil {
		t.Fatal("accepted an address belonging to another instance")
	}
}
