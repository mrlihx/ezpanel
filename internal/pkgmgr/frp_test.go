package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"github.com/pelletier/go-toml/v2"
)

func TestFRPReleaseCatalog(t *testing.T) {
	data := fmt.Sprintf(`[
 {"tag_name":"v0.72.0","assets":[{"name":"frp_0.72.0_linux_%s.tar.gz","browser_download_url":"https://github.com/fatedier/frp/releases/download/v0.72.0/frp_0.72.0_linux_%s.tar.gz","digest":"sha256:abc"}]},
 {"tag_name":"v0.73.0","prerelease":true,"assets":[]},
 {"tag_name":"v0.70.0","draft":true,"assets":[]}
 ]`, runtime.GOARCH, runtime.GOARCH)
	rows, err := frpReleases([]byte(data))
	if err != nil || len(rows) != 1 || rows[0].Version != "0.72.0" || rows[0].SHA256 != "abc" {
		t.Fatalf("catalog=%+v err=%v", rows, err)
	}
}
func TestFRPVisualPreservesAdvancedOptions(t *testing.T) {
	raw := `serverAddr = "old.example.com"
 [auth]
 token = "original"
 [transport]
 poolCount = 8
 [[proxies]]
 name = "web"
 type = "http"
 localIP = "127.0.0.1"
 localPort = 80
 customDomains = ["old.example.com"]
 [proxies.requestHeaders.set]
 X-Custom = "keep"
 [[proxies]]
 name = "private"
 type = "stcp"
 secretKey = "keep-this"
 `
	var values map[string]any
	if err := toml.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatal(err)
	}
	patch := map[string]json.RawMessage{
		"serverPort": json.RawMessage(`7001`),
		"proxies":    json.RawMessage(`[{"originalName":"web","name":"new-web","type":"http","localIP":"127.0.0.1","localPort":8080,"customDomains":["new.example.com"]}]`),
	}
	if err := patchFRP("frpc", values, patch); err != nil {
		t.Fatal(err)
	}
	out, err := toml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"poolCount = 8", "X-Custom = 'keep'", "secretKey = 'keep-this'", "name = 'new-web'", "serverPort = 7001"} {
		if !strings.Contains(string(out), part) {
			t.Fatalf("missing %q in %s", part, out)
		}
	}
	if err = patchFRP("frpc", values, map[string]json.RawMessage{"bindPort": json.RawMessage(`7000`)}); err == nil {
		t.Fatal("accepted server option for client")
	}
}

func TestFRPSaveAppliesAndRestores(t *testing.T) {
	for _, scenario := range []struct {
		name, role            string
		running, global, fail bool
		mode                  string
	}{
		{name: "client proxy reload", role: "frpc", running: true, mode: "reload"},
		{name: "client common restart", role: "frpc", running: true, global: true, mode: "restart"},
		{name: "server restart", role: "frps", running: true, global: true, mode: "restart"},
		{name: "stopped save", role: "frpc", global: true, mode: "saved"},
		{name: "restore after reload failure", role: "frpc", running: true, fail: true, mode: "reload"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := New()
			m.Configure(config.Config{InstallDir: t.TempDir()})
			root := m.layout().AppDir("frp")
			if err := os.MkdirAll(root, 0700); err != nil {
				t.Fatal(err)
			}
			content := fmt.Sprintf("serverAddr = '127.0.0.1'\nserverPort = 7000\n[log]\nto = %q\n[webServer]\naddr = '127.0.0.1'\nport = 7400\n", filepath.Join(m.layout().AppLogDir("frp"), scenario.role+".log"))
			path := filepath.Join(root, scenario.role+".toml")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			record, _ := json.Marshal(Release{Version: "0.72.0"})
			if err := os.WriteFile(m.frpRecord(scenario.role), record, 0600); err != nil {
				t.Fatal(err)
			}
			var calls []string
			applyCount := 0
			m.Run = func(_ context.Context, _ time.Duration, _ []string, command string, args ...string) (executor.Result, error) {
				calls = append(calls, filepath.Base(command)+" "+strings.Join(args, " "))
				if args[0] == "is-active" {
					if scenario.running {
						return executor.Result{Stdout: "active\n"}, nil
					}
					return executor.Result{}, nil
				}
				if args[0] == "reload" || args[0] == "restart" {
					applyCount++
					if scenario.fail && applyCount == 1 {
						return executor.Result{}, errors.New("apply failed")
					}
				}
				return executor.Result{}, nil
			}
			current, err := m.FRPConfig(scenario.role)
			if err != nil {
				t.Fatal(err)
			}
			revised := content + "\n[[proxies]]\nname='one'\ntype='tcp'\nlocalPort=80\nremotePort=6000\n"
			if scenario.global {
				revised = strings.Replace(content, "serverPort = 7000", "serverPort = 7001", 1)
			}
			mode, err := m.SaveFRPConfig(context.Background(), scenario.role, FRPConfigInput{Revision: current.Revision, Content: &revised})
			if scenario.fail {
				if err == nil {
					t.Fatal("expected failure")
				}
			} else if err != nil || mode != scenario.mode {
				t.Fatalf("mode=%s err=%v calls=%v", mode, err, calls)
			}
			saved, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			expected := revised
			if scenario.fail {
				expected = content
			}
			if string(saved) != expected {
				t.Fatalf("saved content mismatch: %s", saved)
			}
			expectedCount := 1
			if !scenario.running {
				expectedCount = 0
			}
			if scenario.fail {
				expectedCount = 2
			}
			if applyCount != expectedCount {
				t.Fatalf("apply count %d calls=%v", applyCount, calls)
			}
			if _, err = m.SaveFRPConfig(context.Background(), scenario.role, FRPConfigInput{Revision: "stale", Content: &revised}); err == nil {
				t.Fatal("accepted stale revision")
			}
		})
	}
}
func TestFRPGlobalChangesRequireRestart(t *testing.T) {
	before := map[string]any{"serverPort": int64(7000), "proxies": []any{"old"}}
	after := map[string]any{"serverPort": int64(7000), "proxies": []any{"new"}, "start": []any{"one"}}
	if frpNeedsRestart(before, after) {
		t.Fatal("proxy-only edit requires restart")
	}
	after["serverPort"] = int64(7001)
	if !frpNeedsRestart(before, after) {
		t.Fatal("global edit did not require restart")
	}
	if !reflect.DeepEqual(before["proxies"], []any{"old"}) {
		t.Fatal("mutated original")
	}
}

func TestFRPUnitWorkingDirectory(t *testing.T) {
	for _, role := range []string{"frps", "frpc"} {
		t.Run(role, func(t *testing.T) {
			root := "/opt/panel data/100%/$panel/frp"
			unit := frpUnitContent(role, root+"/bin/"+role, root+"/"+role+".toml", root)
			if !strings.Contains(unit, "\nWorkingDirectory=/opt/panel data/100%%/$panel/frp\n") {
				t.Fatalf("WorkingDirectory must be an unquoted path with escaped specifiers: %s", unit)
			}
			if !strings.Contains(unit, `ExecStart="/opt/panel data/100%%/$$panel/frp/bin/`+role+`" -c "/opt/panel data/100%%/$$panel/frp/`+role+`.toml"`) {
				t.Fatalf("ExecStart arguments must remain quoted: %s", unit)
			}
		})
	}
}
