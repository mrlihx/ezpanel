package pkgmgr

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

type FRPConfig struct {
	Settings
	Values map[string]any `json:"values"`
}
type FRPConfigInput struct {
	Revision string                     `json:"revision"`
	Content  *string                    `json:"content"`
	Values   map[string]json.RawMessage `json:"values"`
}
type FRPProxy struct {
	OriginalName  string   `json:"originalName"`
	Name          string   `json:"name"`
	Type          string   `json:"type"`
	LocalIP       string   `json:"localIP"`
	LocalPort     int      `json:"localPort"`
	RemotePort    int      `json:"remotePort"`
	CustomDomains []string `json:"customDomains"`
}

func (m *Manager) frpDefault(role string) ([]byte, error) {
	values := map[string]any{"log": map[string]any{"to": filepath.Join(m.layout().AppLogDir("frp"), role+".log"), "level": "info", "maxDays": 7, "disablePrintColor": true}, "auth": map[string]any{"method": "token", "token": rand.Text()}}
	if role == "frps" {
		values["bindAddr"] = "0.0.0.0"
		values["bindPort"] = 7000
	} else {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return nil, err
		}
		port := listener.Addr().(*net.TCPAddr).Port
		listener.Close()
		values["serverAddr"], values["serverPort"], values["loginFailExit"] = "127.0.0.1", 7000, false
		values["webServer"] = map[string]any{"addr": "127.0.0.1", "port": port, "user": "ezpanel", "password": rand.Text()}
		values["transport"] = map[string]any{"tls": map[string]any{"enable": true}}
	}
	return toml.Marshal(values)
}
func (m *Manager) FRPConfig(role string) (FRPConfig, error) {
	path := filepath.Join(m.layout().AppDir("frp"), role+".toml")
	b, err := os.ReadFile(path)
	if err != nil {
		return FRPConfig{}, err
	}
	if len(b) > 1<<20 {
		return FRPConfig{}, errors.New("配置超过 1 MiB，请使用文件管理编辑")
	}
	hash := sha256.Sum256(b)
	out := FRPConfig{Settings: Settings{Path: path, Content: string(b), Revision: hex.EncodeToString(hash[:])}}
	err = toml.Unmarshal(b, &out.Values)
	return out, err
}

func (m *Manager) verifyFRP(ctx context.Context, binary, path string) error {
	result, err := m.Run(ctx, 20*time.Second, nil, binary, "verify", "-c", path)
	if err != nil {
		return fmt.Errorf("Frp 配置校验失败：%s: %w", strings.TrimSpace(result.Stderr+" "+result.Stdout), err)
	}
	return nil
}

func patchFRP(role string, config map[string]any, patch map[string]json.RawMessage) error {
	integer := []string{"serverPort", "bindPort", "kcpBindPort", "quicBindPort", "vhostHTTPPort", "vhostHTTPSPort", "webServer.port", "log.maxDays"}
	text := []string{"bindAddr", "serverAddr", "user", "auth.token", "transport.protocol", "webServer.addr", "webServer.user", "webServer.password", "log.level"}
	allowed := map[string]bool{}
	keys := []string{"auth.token", "log.level", "log.maxDays"}
	if role == "frps" {
		keys = append(keys, "bindAddr", "bindPort", "kcpBindPort", "quicBindPort", "vhostHTTPPort", "vhostHTTPSPort", "allowPorts", "webServer.addr", "webServer.port", "webServer.user", "webServer.password")
	} else {
		keys = append(keys, "serverAddr", "serverPort", "user", "transport.protocol", "transport.tls.enable", "proxies")
	}
	for _, key := range keys {
		allowed[key] = true
	}
	for key, raw := range patch {
		if !allowed[key] {
			return fmt.Errorf("不支持的 Frp 可视化配置项 %s", key)
		}
		var value any
		var err error
		switch {
		case contains(integer, key):
			var v int
			err = json.Unmarshal(raw, &v)
			value = v
		case contains(text, key):
			var v string
			err = json.Unmarshal(raw, &v)
			value = v
		case key == "transport.tls.enable":
			var v bool
			err = json.Unmarshal(raw, &v)
			value = v
		case key == "allowPorts":
			var v []struct {
				Single int `json:"single" toml:"single,omitempty"`
				Start  int `json:"start" toml:"start,omitempty"`
				End    int `json:"end" toml:"end,omitempty"`
			}
			err = json.Unmarshal(raw, &v)
			value = v
		case key == "proxies":
			var proxies []FRPProxy
			if err = json.Unmarshal(raw, &proxies); err != nil {
				return errors.New("代理列表格式无效")
			}
			originals := map[string]map[string]any{}
			old, _ := config["proxies"].([]any)
			result := []map[string]any{}
			// 可视化不支持的代理类型仍保留，交给原始配置编辑。
			for _, entry := range old {
				row, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				name, _ := row["name"].(string)
				originals[name] = row
				kind, _ := row["type"].(string)
				if !contains([]string{"tcp", "udp", "http", "https"}, kind) {
					result = append(result, row)
				}
			}
			for _, proxy := range proxies {
				if !contains([]string{"tcp", "udp", "http", "https"}, proxy.Type) {
					return errors.New("不支持的可视化代理类型")
				}
				row := originals[proxy.OriginalName]
				if row == nil {
					row = map[string]any{}
				}
				row["name"], row["type"], row["localIP"], row["localPort"] = proxy.Name, proxy.Type, proxy.LocalIP, proxy.LocalPort
				delete(row, "remotePort")
				delete(row, "customDomains")
				if proxy.Type == "tcp" || proxy.Type == "udp" {
					row["remotePort"] = proxy.RemotePort
				} else {
					row["customDomains"] = proxy.CustomDomains
				}
				result = append(result, row)
			}
			value = result
		}
		if err != nil {
			return fmt.Errorf("配置项 %s 格式无效", key)
		}
		parts := strings.Split(key, ".")
		parent := config
		for _, part := range parts[:len(parts)-1] {
			child, ok := parent[part].(map[string]any)
			if !ok {
				child = map[string]any{}
				parent[part] = child
			}
			parent = child
		}
		parent[parts[len(parts)-1]] = value
	}
	return nil
}

// 公共字段变动必须重启；只有 proxies、visitors、start 可真实热重载。
func frpNeedsRestart(before, after map[string]any) bool {
	common := func(in map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range in {
			if k != "proxies" && k != "visitors" && k != "start" {
				out[k] = v
			}
		}
		return out
	}
	return !reflect.DeepEqual(common(before), common(after))
}

func (m *Manager) SaveFRPConfig(ctx context.Context, role string, input FRPConfigInput) (mode string, err error) {
	err = m.ApplicationChange("frp", func() error {
		current, err := m.FRPConfig(role)
		if err != nil {
			return err
		}
		if current.Revision != input.Revision {
			return errors.New("配置已被修改，请重新加载后保存")
		}
		var content []byte
		if input.Content != nil {
			content = []byte(*input.Content)
		} else {
			if err = patchFRP(role, current.Values, input.Values); err != nil {
				return err
			}
			content, err = toml.Marshal(current.Values)
			if err != nil {
				return err
			}
		}
		if len(content) > 1<<20 {
			return errors.New("配置不能超过 1 MiB")
		}
		var values, before map[string]any
		if err = toml.Unmarshal(content, &values); err != nil {
			return fmt.Errorf("TOML 配置无效：%w", err)
		}
		before = current.Values
		logs, _ := values["log"].(map[string]any)
		if logs["to"] != filepath.Join(m.layout().AppLogDir("frp"), role+".log") {
			return errors.New("请保留 log.to 为当前服务的统一日志文件路径")
		}
		f, err := os.CreateTemp(filepath.Dir(current.Path), ".verify-*.toml")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		_, err = f.Write(content)
		if err = errors.Join(err, f.Close()); err != nil {
			return err
		}
		if err = m.verifyFRP(ctx, m.frpBinary(role), f.Name()); err != nil {
			return err
		}
		rows, err := m.FRPServices(ctx)
		if err != nil {
			return err
		}
		running := false
		for _, row := range rows {
			if row.Role == role {
				if !row.Installed {
					return errors.New("请先安装此服务")
				}
				running = row.Running
			}
		}
		mode = "saved"
		if running {
			mode = "restart"
			admin, _ := values["webServer"].(map[string]any)
			if role == "frpc" && !frpNeedsRestart(before, values) && admin["port"] != nil {
				mode = "reload"
			}
		}
		if err = writeHostConfig(current.Path+".previous", []byte(current.Content)); err != nil {
			return err
		}
		if err = os.Rename(f.Name(), current.Path); err != nil {
			return err
		}
		apply := func(applyCtx context.Context) error {
			if mode == "reload" {
				_, err := m.Run(applyCtx, 30*time.Second, nil, m.frpBinary(role), "reload", "-c", current.Path)
				return err
			}
			_, err := m.Run(applyCtx, 40*time.Second, nil, "/usr/bin/systemctl", "restart", frpUnit(role))
			return err
		}
		if running {
			if err = apply(ctx); err != nil {
				restoreErr := writeHostConfig(current.Path, []byte(current.Content))
				if restoreErr == nil {
					restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
					defer cancel()
					restoreErr = apply(restoreCtx)
				}
				return errors.Join(fmt.Errorf("应用配置失败，已恢复原配置：%w", err), restoreErr)
			}
		}
		return nil
	})
	return mode, err
}
