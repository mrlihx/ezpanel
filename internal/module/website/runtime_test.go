package website

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
)

func TestPythonLaunch(t *testing.T) {
	for _, tc := range []struct {
		mode, entry, command string
		port, wantPort       int
		wantErr              bool
	}{
		{mode: "command", command: "python app.py", port: 8080},
		{mode: "uwsgi", entry: "project.wsgi:application", command: "exec pyuwsgi --ini /tmp/uwsgi.ini", port: 8080, wantPort: 8080},
		{mode: "gunicorn", entry: "app:app", command: "exec gunicorn --config /tmp/gunicorn.conf.py", port: 8080, wantPort: 8080},
		{mode: "gunicorn", entry: "app:app", wantErr: true},
		{mode: "uwsgi", entry: "app:app; echo foo", port: 8000, wantErr: true},
	} {
		in := input{ProjectType: "python", RuntimeLaunch: tc.mode, RuntimeEntry: tc.entry, RuntimePort: tc.port}
		if tc.mode == "command" {
			in.RuntimeCommand = tc.command
		}
		err := in.validateRuntime(store.Website{})
		if tc.wantErr {
			if err == nil {
				t.Fatalf("无效配置通过校验：%+v", tc)
			}
		} else if err != nil || in.RuntimePort != tc.wantPort || !strings.Contains(in.RuntimeCommand, tc.command) {
			t.Fatalf("启动设置错误：%+v，%v", in, err)
		} else if tc.mode != "command" {
			config := wsgiConfig(tc.mode, tc.entry, tc.port, 0, 0, 0)
			if !strings.Contains(config.Content, "0.0.0.0:8080") || !strings.Contains(in.RuntimeCommand, config.Content) {
				t.Fatalf("启动文件与所显示的配置不一致：%s", config.Content)
			}
		}
	}
}

func TestRuntimeProjectDirectoryCanBeCreatedEmpty(t *testing.T) {
	base := t.TempDir()
	policy, err := pathsafe.New(filepath.Join(base, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	m := Module{db: db, cfg: config.Config{SiteRoot: base}, policy: policy}
	for _, runtime := range []string{"python", "nodejs", "go"} {
		t.Run(runtime, func(t *testing.T) {
			in := input{
				ProjectType: runtime, Type: "proxy", Name: runtime,
				Root:               filepath.Join(base, runtime),
				RuntimeEnvironment: "env",
			}
			row, err := m.validate(in, store.Website{})
			if err != nil {
				t.Fatal(err)
			}
			if row.RuntimeCommand != "" || row.RuntimePort != 0 || row.RuntimeLaunch != "" {
				t.Fatal("添加项目时应允许稍后配置启动方式")
			}
			if len(siteServers(row)) != 0 {
				t.Fatal("未绑定域名的项目不应生成监听配置")
			}
			cleanup, err := prepareRoot(&row, base, "")
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(row.Root)
			if err != nil || len(entries) != 0 {
				t.Fatalf("项目目录应为空：%v %v", entries, err)
			}
			if runtime == "python" {
				in.RuntimeLaunch, in.RuntimeEntry, in.RuntimePort = "gunicorn", "app:app", 8000
				in.RuntimeAutoStart = true
				if _, err := m.validate(in, row); err == nil {
					t.Fatal("入口文件不存在时应拒绝保存")
				}
				entry := filepath.Join(row.Root, "app.py")
				if err := os.WriteFile(entry, nil, 0644); err != nil {
					t.Fatal(err)
				}
				configured, err := m.validate(in, row)
				if err != nil || configured.RuntimeEntry != in.RuntimeEntry || !strings.Contains(configured.RuntimeCommand, "gunicorn") {
					t.Fatalf("无法配置入口文件：%v", err)
				}
				if !configured.RuntimeAutoStart {
					t.Fatal("Python 项目应保留开机自启动设置")
				}
				if err := os.Remove(entry); err != nil {
					t.Fatal(err)
				}
			} else if runtime == "go" {
				for _, tc := range []struct {
					file, command, want string
					mode                os.FileMode
				}{
					{"main.go", "", goSourceCommand, 0644},
					{"main.go", goBinaryCommand, goSourceCommand, 0644},
					{"main.go", "go run . --config app.yaml", "go run . --config app.yaml", 0644},
					{"app", goSourceCommand, goBinaryCommand, 0755},
					{"app", "", "", 0644},
				} {
					entry := filepath.Join(row.Root, tc.file)
					if err := os.WriteFile(entry, nil, tc.mode); err != nil {
						t.Fatal(err)
					}
					in.RuntimeFile, in.RuntimeCommand, in.RuntimePort = tc.file, tc.command, 8000
					configured, err := m.validate(in, row)
					if tc.want == "" {
						if err == nil {
							t.Fatal("没有执行权限的二进制文件通过校验")
						}
					} else if err != nil || configured.RuntimeCommand != tc.want {
						t.Fatalf("%s 的启动命令为 %q，预期 %q：%v", tc.file, configured.RuntimeCommand, tc.want, err)
					}
					if err := os.Remove(entry); err != nil {
						t.Fatal(err)
					}
				}
			}
			cleanup()
			if _, err := os.Stat(row.Root); !os.IsNotExist(err) {
				t.Fatalf("项目目录应在创建失败时清理：%v", err)
			}
		})
	}
}
