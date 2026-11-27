package pkgmgr

import (
	"context"
	"os/exec"
	"reflect"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestEnsureDockerComposeKeepsExistingCLI(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "plugin"
		want := [][]string{{"/staged/docker", "compose", "version"}}
		if legacy {
			name = "standalone"
			want = append(want, []string{"/usr/bin/docker-compose", "version"})
		}
		t.Run(name, func(t *testing.T) {
			m := New()
			m.LookPath = func(name string) (string, error) {
				if legacy && name == "docker-compose" {
					return "/usr/bin/docker-compose", nil
				}
				return "", exec.ErrNotFound
			}
			var calls [][]string
			m.Run = func(_ context.Context, _ time.Duration, _ []string, bin string, args ...string) (executor.Result, error) {
				calls = append(calls, append([]string{bin}, args...))
				if legacy && bin == "/staged/docker" {
					return executor.Result{}, exec.ErrNotFound
				}
				return executor.Result{Stdout: "Compose version"}, nil
			}
			if err := m.ensureDockerCompose(context.Background(), "/staged/docker", func(string) {}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, want) {
				t.Fatalf("CLI 调用 = %v，期望 %v", calls, want)
			}
		})
	}
}
