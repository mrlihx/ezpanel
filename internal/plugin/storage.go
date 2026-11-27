package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"ezpanel/internal/module/backup"
	"ezpanel/internal/store"
)

const storageCallback = "_panel/storage/"

func validateBackupProvider(provider backup.PluginProvider) error {
	if strings.TrimSpace(provider.Name) == "" || len([]rune(provider.Name)) > 64 || len(provider.Fields) > 12 {
		return errors.New("存储渠道名称无效或字段超过 12 个")
	}
	keys := map[string]bool{}
	for _, field := range provider.Fields {
		if !idPattern.MatchString(field.Key) || keys[field.Key] || strings.TrimSpace(field.Label) == "" || len([]rune(field.Label)) > 64 {
			return errors.New("存储渠道字段名称无效或重复")
		}
		keys[field.Key] = true
		switch field.Type {
		case "text", "password", "number":
		case "select":
			if len(field.Options) == 0 || len(field.Options) > 50 {
				return errors.New("渠道下拉字段需提供 1 至 50 个选项")
			}
		default:
			return errors.New("渠道字段类型必须为 text、password、number 或 select")
		}
	}
	return nil
}

func (m *Module) BackupProviders(ctx context.Context) ([]backup.PluginProvider, error) {
	var rows []store.Plugin
	if err := m.db.WithContext(ctx).Select("id", "enabled", "manifest").Where("enabled = ? AND type = ?", true, "process").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := []backup.PluginProvider{}
	for _, p := range rows {
		var manifest Manifest
		if m.status(p) == "running" && json.Unmarshal([]byte(p.Manifest), &manifest) == nil && manifest.BackupProvider != nil && m.checkPermission(p.ID, manifest, "backup.storage") == nil {
			provider := *manifest.BackupProvider
			provider.Protocol = backup.PluginProtocolPrefix + p.ID
			out = append(out, provider)
		}
	}
	return out, nil
}

func (m *Module) CallBackupProvider(ctx context.Context, id, operation string, input backup.PluginChannelRequest, source io.Reader) (io.ReadCloser, error) {
	run, manifest, err := m.readyPlugin(id)
	if err != nil || manifest.BackupProvider == nil {
		return nil, errors.New("备份存储插件未运行或未提供存储服务")
	}
	if err := m.checkPermission(id, manifest, "backup.storage"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(run.ctx, cancel)
	select {
	case run.backupSlots <- struct{}{}:
	case <-ctx.Done():
		stop()
		cancel()
		return nil, ctx.Err()
	}
	finish := func() { <-run.backupSlots; stop(); cancel() }
	data, _ := json.Marshal(input)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/api/%s%s", run.port, storageCallback, operation), source)
	if err != nil {
		finish()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Panel-Plugin-Id", id)
	req.Header.Set("X-Panel-Proxy-Token", run.proxy)
	req.Header.Set("X-Panel-User", "system")
	req.Header.Set("X-Panel-Storage-Request", base64.RawURLEncoding.EncodeToString(data))
	client := &http.Client{Transport: sharedPluginProxyTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		finish()
		return nil, errors.New("备份插件连接中断或超时")
	}
	if operation == "delete" && resp.StatusCode == http.StatusNotFound {
		return &storageBody{ReadCloser: resp.Body, finish: finish}, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		finish()
		return nil, fmt.Errorf("备份插件 %s 失败（HTTP %d），请查看插件日志", operation, resp.StatusCode)
	}
	return &storageBody{ReadCloser: resp.Body, finish: finish}, nil
}

type storageBody struct {
	io.ReadCloser
	finish func()
	once   sync.Once
	err    error
}

func (body *storageBody) Close() error {
	body.once.Do(func() {
		body.err = body.ReadCloser.Close()
		body.finish()
	})
	return body.err
}
