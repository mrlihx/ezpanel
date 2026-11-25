package pkgmgr

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestRunningDatabaseInstanceMatchesIDAndPublishedPort(t *testing.T) {
	m := New()
	const first = "aaaaaaaaaaaaaaaaaaaaaaaaaa"
	const second = "bbbbbbbbbbbbbbbbbbbbbbbbbb"
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
