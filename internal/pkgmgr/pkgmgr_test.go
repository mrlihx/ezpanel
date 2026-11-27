package pkgmgr

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"strings"
	"testing"
	"time"
)

func TestEnableSystemdStartupRestoresServiceAndStaticTrigger(t *testing.T) {
	for _, tc := range []struct{ name, serviceState, trigger, want string }{
		{name: "service", serviceState: "disabled", want: "enable app.service"},
		{name: "static service with socket", serviceState: "static", trigger: "app.socket", want: "enable app.socket"},
		{name: "already enabled", serviceState: "enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			var enable string
			m.Run = func(_ context.Context, _ time.Duration, _ []string, cmd string, args ...string) (executor.Result, error) {
				if cmd != systemctlPath {
					t.Errorf("unexpected command %s", cmd)
				}
				switch strings.Join(args, " ") {
				case "show app.service --property=TriggeredBy --value":
					return executor.Result{Stdout: tc.trigger}, nil
				case "is-enabled app.service":
					if tc.serviceState == "disabled" {
						return executor.Result{Stdout: "disabled", ExitCode: 1}, errors.New("disabled")
					}
					return executor.Result{Stdout: tc.serviceState}, nil
				case "is-enabled app.socket":
					return executor.Result{Stdout: "disabled", ExitCode: 1}, errors.New("disabled")
				case "enable app.service", "enable app.socket":
					enable = strings.Join(args, " ")
					return executor.Result{}, nil
				default:
					t.Errorf("unexpected systemctl arguments: %v", args)
					return executor.Result{}, nil
				}
			}
			if err := m.enableSystemdStartup(t.Context(), time.Second, "app.service"); err != nil {
				t.Fatal(err)
			}
			if enable != tc.want {
				t.Errorf("persisted boot startup with %q, want %q", enable, tc.want)
			}
		})
	}
}

func TestStopDockerDisablesServiceAndSocket(t *testing.T) {
	m := New()
	m.ReadFile = func(path string) ([]byte, error) {
		if path == "/etc/os-release" {
			return []byte("ID=ubuntu\n"), nil
		}
		return []byte("systemd\n"), nil
	}
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	running := true
	m.Run = func(_ context.Context, _ time.Duration, _ []string, cmd string, args ...string) (executor.Result, error) {
		switch {
		case cmd == "/usr/bin/dpkg-query" && args[0] == "-W":
			return executor.Result{Stdout: "docker.io\tinstalled\n"}, nil
		case cmd == "/usr/bin/dpkg-query" && args[0] == "-S":
			return executor.Result{Stdout: "docker.io: /usr/bin/dockerd\n"}, nil
		case cmd == "/usr/bin/dockerd" && args[0] == "--version":
			return executor.Result{Stdout: "Docker version 29.1.3"}, nil
		case cmd == systemctlPath:
			switch strings.Join(args, " ") {
			case "show docker.service --property=LoadState --value":
				return executor.Result{Stdout: "loaded\n"}, nil
			case "is-active docker.service":
				if running {
					return executor.Result{Stdout: "active\n"}, nil
				}
				return executor.Result{Stdout: "inactive\n"}, nil
			case "show docker.service --property=TriggeredBy --value":
				return executor.Result{Stdout: "docker.socket\n"}, nil
			case "disable --now docker.service docker.socket":
				running = false
				return executor.Result{}, nil
			}
		}
		t.Fatalf("意外的命令 %s %v", cmd, args)
		return executor.Result{}, nil
	}
	state, err := m.Service(context.Background(), "docker", "stop")
	if err != nil || running || state.Running == nil || *state.Running {
		t.Fatalf("停止服务并关闭自启动失败：%+v，%v", state, err)
	}
}

func TestWhitelist(t *testing.T) {
	m := New()
	calls := 0
	m.Run = func(context.Context, time.Duration, []string, string, ...string) (executor.Result, error) {
		calls++
		return executor.Result{}, nil
	}
	if _, e := m.Status(context.Background(), "nginx;touch /tmp/x"); e == nil {
		t.Fatal("software injection")
	}
	if _, e := m.Service(context.Background(), "nginx", "start;id"); e == nil {
		t.Fatal("action injection")
	}
	if calls != 0 {
		t.Fatal("invalid input executed")
	}
	m.ReadFile = func(p string) ([]byte, error) {
		if p == "/etc/os-release" {
			return []byte("ID=unknown\nID_LIKE=debian"), nil
		}
		return []byte("systemd\n"), nil
	}
	m.LookPath = func(s string) (string, error) { return "", errors.New("missing") }
	if _, e := m.Detect(); e == nil {
		t.Fatal("unsupported distro")
	}
}
func TestInstalledNotUpgradedAndSourceUnmanaged(t *testing.T) {
	m := New()
	m.ReadFile = func(p string) ([]byte, error) {
		if p == "/etc/os-release" {
			return []byte("ID=debian"), nil
		}
		return []byte("systemd\n"), nil
	}
	m.LookPath = func(s string) (string, error) { return "/usr/bin/" + s, nil }
	managed := true
	mutated := false
	m.Run = func(ctx context.Context, d time.Duration, env []string, name string, args ...string) (executor.Result, error) {
		switch name {
		case "/usr/bin/dpkg-query":
			if managed {
				if args[0] == "-S" {
					return executor.Result{Stdout: "nginx: /usr/bin/nginx\n"}, nil
				}
				return executor.Result{Stdout: "nginx\tinstalled\n"}, nil
			}
			return executor.Result{ExitCode: 1}, errors.New("not packaged")
		case "/usr/bin/nginx":
			return executor.Result{Stdout: "nginx version: nginx/1.28.0"}, nil
		case "/usr/bin/systemctl":
			if args[0] == "show" {
				return executor.Result{Stdout: "loaded"}, nil
			}
			if args[0] == "is-active" {
				return executor.Result{Stdout: "active"}, nil
			}
			mutated = true
		}
		return executor.Result{}, nil
	}
	m.Stream = func(context.Context, time.Duration, []string, func(string), string, ...string) (executor.Result, error) {
		mutated = true
		return executor.Result{}, nil
	}
	s, e := m.Install(context.Background(), "nginx", nil)
	if e != nil || !s.Installed || mutated {
		t.Fatal(s, e, mutated)
	}
	managed = false
	s, e = m.Status(context.Background(), "nginx")
	if e != nil || !s.Installed || s.Manageable {
		t.Fatal(s, e)
	}
	if _, e = m.Install(context.Background(), "nginx", nil); e == nil || mutated {
		t.Fatal("overwrote source install")
	}
}

func TestHostToolReleasesReadSystemRepository(t *testing.T) {
	m := New()
	m.ReadFile = func(path string) ([]byte, error) {
		if path == "/etc/os-release" {
			return []byte("ID=ubuntu\n"), nil
		}
		return []byte("systemd\n"), nil
	}
	m.LookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	for id, pkg := range map[string]string{"ftp": "pure-ftpd", "fail2ban": "fail2ban", "supervisor": "supervisor", "clamav": "clamav-daemon", "rsync": "rsync"} {
		t.Run(id, func(t *testing.T) {
			m.Run = func(_ context.Context, _ time.Duration, _ []string, name string, args ...string) (executor.Result, error) {
				if name != "/usr/bin/apt-cache" || len(args) != 2 || args[0] != "policy" || args[1] != pkg {
					t.Fatalf("不应使用该命令读取宿主机版本：%s %v", name, args)
				}
				return executor.Result{Stdout: "  Candidate: 1:3.4.1+ds1-7ubuntu0.3\n"}, nil
			}
			rows, err := m.Releases(context.Background(), id, false)
			if err != nil || len(rows) != 1 || rows[0].Version != "1:3.4.1+ds1-7ubuntu0.3" || rows[0].Method != "package" || UsesDocker(id) {
				t.Fatalf("宿主机应用版本读取错误：%+v %v", rows, err)
			}
		})
	}
}
