package pkgmgr

import (
	"context"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/executor"
)

func TestRsyncUninstallRetainsSystemDependency(t *testing.T) {
	m := New()
	m.Configure(config.Config{InstallDir: t.TempDir()})
	m.ReadFile = func(path string) ([]byte, error) {
		if path == "/etc/os-release" {
			return []byte("ID=ubuntu\n"), nil
		}
		return []byte("systemd\n"), nil
	}
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	running, disabled := true, false
	m.Run = func(_ context.Context, _ time.Duration, _ []string, name string, args ...string) (executor.Result, error) {
		switch name + " " + strings.Join(args, " ") {
		case "/usr/bin/apt-get --simulate remove rsync":
			return executor.Result{Stdout: "Remv ubuntu-standard [1.570.3]\nRemv rsync [3.4.1]\n"}, nil
		case systemctlPath + " show rsync.service --property=LoadState --value":
			return executor.Result{Stdout: "loaded"}, nil
		case systemctlPath + " is-active rsync.service":
			if running {
				return executor.Result{Stdout: "active"}, nil
			}
			return executor.Result{Stdout: "inactive"}, nil
		case systemctlPath + " disable --now rsync.service":
			running, disabled = false, true
			return executor.Result{}, nil
		}
		if name == "/usr/bin/dpkg-query" && args[0] == "-W" {
			return executor.Result{Stdout: "rsync\tinstalled\t3.4.1\n"}, nil
		}
		if name == "/usr/bin/dpkg-query" && args[0] == "-S" {
			return executor.Result{Stdout: "rsync: /usr/bin/rsync\n"}, nil
		}
		t.Fatalf("unexpected command: %s %v", name, args)
		return executor.Result{}, nil
	}
	m.Stream = func(_ context.Context, _ time.Duration, _ []string, _ func(string), name string, args ...string) (executor.Result, error) {
		t.Fatalf("system packages must not be removed: %s %v", name, args)
		return executor.Result{}, nil
	}
	state, err := m.Uninstall(context.Background(), "rsync", nil)
	if err != nil || !disabled || state.Installed || !state.rsyncDetached || !contains(state.packages, "rsync") {
		t.Fatalf("expected the service to be detached and its system package retained, got %+v %v", state, err)
	}
}
