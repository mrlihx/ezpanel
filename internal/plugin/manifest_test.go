package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManifestMenuChildren(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "entry.js"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	const base = `id: test-plugin
name: Test Plugin
version: 1.0.0
type: static
minPanelVersion: 0.0.0
sdkVersion: 1
ui: web/entry.js
menu:
  title: 网盘
  children:
`
	const child = "    - {title: 文件, path: files}\n"
	for _, tc := range []struct {
		name, children string
		valid          bool
	}{
		{"legacy and tools", child + "    - {title: 工具, path: tools}\n", true},
		{"absolute", "    - {title: 文件, path: https://example.test/tools}\n", false},
		{"traversal", "    - {title: 文件, path: ../tools}\n", false},
		{"duplicate", child + child, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ReadManifest(strings.NewReader(base+tc.children), root, root, true, CoreVersion)
			if (err == nil) != tc.valid {
				t.Fatalf("menu children validation: %v", err)
			}
		})
	}
}

func TestPreviewShowsDeclaredAndRuntimeRisks(t *testing.T) {
	m := Manifest{
		Type: "process", UIMode: "isolated", Sandbox: &Sandbox{Network: "outbound"},
		Permissions: []string{"fs.write", "credentials.read", "compose.deploy", "jobs.run", "containers.logs.read", "sites.restart", "databases.backup", "notifications.send", "jobs.webhook"},
		Jobs:        []Job{{Title: "巡检"}}, CredentialSlots: []CredentialSlot{{Title: "备份凭据"}},
	}
	preview := previewManifest(m)
	if len(preview.Permissions) != len(m.Permissions) || preview.Permissions[0].Risk != highRisk || preview.Permissions[1].Risk != highRisk || preview.Permissions[2].Risk != attention {
		t.Fatalf("unexpected permission risks: %+v", preview.Permissions)
	}
	for _, item := range preview.Permissions[3:] {
		if item.Risk != "" {
			t.Errorf("routine permission %s should only appear in the permission list", item.Code)
		}
	}
	if len(preview.RuntimeRisks) != 1 || preview.RuntimeRisks[0].Label != "允许出站网络" || preview.RuntimeRisks[0].Risk != attention {
		t.Fatalf("unexpected runtime risks: %+v", preview.RuntimeRisks)
	}
	if len(preview.Capabilities) != 2 || preview.Capabilities[0].Items[0] != "巡检" || preview.Capabilities[1].Items[0] != "备份凭据" {
		t.Fatalf("unexpected capabilities: %+v", preview.Capabilities)
	}
	m.Sandbox, m.UIMode = nil, "legacy"
	if risks := previewManifest(m).RuntimeRisks; len(risks) != 2 || risks[0].Risk != attention || risks[1].Risk != attention {
		t.Fatalf("native/backend compatibility risks were not shown: %+v", risks)
	}
}

func TestReadManifestHomepageOpenModeAndUIMode(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "entry.js"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	const base = `id: test-plugin
name: Test Plugin
version: 1.0.0
type: static
minPanelVersion: 0.0.0
sdkVersion: 1
ui: web/entry.js
`
	for _, tc := range []struct {
		name, extra string
		wantOpen    string
		wantHome    string
		wantUI      string
		wantError   string
	}{
		{"defaults", "", "embed", "", "isolated", ""},
		{"valid homepage and new_tab", "homepage: https://example.com/plugin\nopenMode: new_tab\n", "new_tab", "https://example.com/plugin", "isolated", ""},
		{"valid embed", "openMode: embed\n", "embed", "", "isolated", ""},
		{"invalid homepage scheme", "homepage: javascript:alert(1)\n", "", "", "", "作者主页"},
		{"invalid homepage format", "homepage: /relative\n", "", "", "", "作者主页"},
		{"invalid openMode", "openMode: popup\n", "", "", "", "openMode"},
		{"legacy uiMode compatible", "uiMode: legacy\n", "embed", "", "legacy", ""},
		{"invalid uiMode rejected", "uiMode: popup\n", "", "", "", "uiMode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := ReadManifest(strings.NewReader(base+tc.extra), root, root, true, CoreVersion)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if manifest.OpenMode != tc.wantOpen || manifest.Homepage != tc.wantHome || manifest.UIMode != tc.wantUI {
				t.Fatalf("manifest = %+v", manifest)
			}
		})
	}
}

func TestReadManifestSettingsWindow(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "web", "entry.js"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	const base = `id: test-plugin
name: Test Plugin
version: 1.0.0
type: static
minPanelVersion: 0.0.0
sdkVersion: 1
ui: web/entry.js
`
	for _, tc := range []struct {
		name, extra string
		want        *SettingsWindow
		wantError   string
	}{
		{"legacy", "", nil, ""},
		{"modal", "settings: {type: modal, width: 720, height: 520}", &SettingsWindow{Type: "modal", Width: 720, Height: 520}, ""},
		{"drawer", "settings: {type: drawer, width: 560}", &SettingsWindow{Type: "drawer", Width: 560}, ""},
		{"none", "settings: {type: none}", &SettingsWindow{Type: "none"}, ""},
		{"unknown type", "settings: {type: custom}", nil, "设置窗口类型"},
		{"negative size", "settings: {type: drawer, height: -100}", nil, "尺寸不能为负数"},
		{"quoted size", `settings: {type: modal, width: "720"}`, nil, "类型不匹配"},
		{"none with size", "settings: {type: none, height: 520}", nil, "不能指定尺寸"},
		{"static with backend config", "settings: {type: none}\nenv: [{key: REQUIRED, required: true}]", nil, "static 禁止后端声明"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest, err := ReadManifest(strings.NewReader(base+tc.extra), root, root, true, CoreVersion)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("want error %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got := manifest.Settings
			if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
				t.Fatalf("settings = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadManifestDefaults(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "web"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-amd64"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin", "linux-arm64"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.Join(root, "web", "entry.js"),
		filepath.Join(root, "bin", "linux-amd64", "server"),
		filepath.Join(root, "bin", "linux-arm64", "server"),
	} {
		if err := os.WriteFile(p, []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	staticYAML := `id: min-static
name: Min Static
version: 0.1.0
`
	m, err := ReadManifest(strings.NewReader(staticYAML), root, root, false, CoreVersion)
	if err != nil {
		t.Fatalf("minimal static manifest failed: %v", err)
	}
	if m.Type != "static" || m.SDKVersion != 1 || m.MinPanelVersion != CoreVersion || m.UI != "web/entry.js" || m.UIMode != "isolated" || m.OpenMode != "embed" {
		t.Fatalf("unexpected defaults for static: %+v", m)
	}

	processYAML := `id: min-process
name: Min Process
version: 0.1.0
entry:
  linux-amd64: bin/linux-amd64/server
  linux-arm64: bin/linux-arm64/server
`
	pm, err := ReadManifest(strings.NewReader(processYAML), root, root, false, CoreVersion)
	if err != nil {
		t.Fatalf("minimal process manifest failed: %v", err)
	}
	if pm.Type != "process" || pm.SDKVersion != 1 || pm.MinPanelVersion != CoreVersion || pm.UI != "web/entry.js" {
		t.Fatalf("unexpected defaults for process: %+v", pm)
	}

	if _, err := ReadManifest(strings.NewReader(staticYAML+"sdkVersion: 2\n"), root, root, false, CoreVersion); err == nil {
		t.Fatal("expected error for invalid explicit sdkVersion")
	}
}
