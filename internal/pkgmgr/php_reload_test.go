package pkgmgr

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"ezpanel/internal/executor"
)

func TestPHPServiceReload(t *testing.T) {
	for _, valid := range []bool{true, false} {
		manager := New()
		manager.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
		const name = "ezpanel-php-8.5"
		check := append(append([]string{"exec", name}, manager.serviceCommand("php-8.5")...), "-t")
		reloaded := false
		manager.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
			switch {
			case slices.Equal(args, []string{"container", "inspect", name}):
				return executor.Result{Stdout: `[{"Config":{"Labels":{"ezpanel.managed":"true","ezpanel.version":"8.5.12"}},"State":{"Running":true}}]`}, nil
			case slices.Equal(args, check):
				if !valid {
					return executor.Result{}, errors.New("invalid configuration")
				}
			case slices.Equal(args, []string{"kill", "--signal=USR2", name}):
				reloaded = true
			default:
				t.Fatalf("unexpected command: %v", args)
			}
			return executor.Result{}, nil
		}
		_, err := manager.Service(context.Background(), "php-8.5", "reload")
		if (err == nil) != valid || reloaded != valid {
			t.Fatalf("valid=%v, reloaded=%v, err=%v", valid, reloaded, err)
		}
	}
}

func TestContainerDatabaseReloadDoesNotRestart(t *testing.T) {
	manager := New()
	for _, id := range []string{"mysql", "redis", "mongodb", "memcached"} {
		if _, err := manager.Service(context.Background(), id, "reload"); !errors.Is(err, ErrUnsupported) {
			t.Errorf("reload %s: got %v, want ErrUnsupported", id, err)
		}
	}
}
