package panelsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type APIError struct {
	Code    int
	Message string
	Status  int
}

func (e *APIError) Error() string { return fmt.Sprintf("Host API %d: %s", e.Code, e.Message) }

type HostClient struct {
	base, token string
	client      *http.Client
}
type Info struct {
	PanelVersion string `json:"panelVersion"`
	SDKVersion   int    `json:"sdkVersion"`
	PluginID     string `json:"pluginId"`
	DataDir      string `json:"dataDir"`
}
type KVItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}
type ExecResult struct {
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Truncated bool   `json:"truncated"`
}
type ExecRequest struct {
	Cmd        string   `json:"cmd"`
	Args       []string `json:"args"`
	TimeoutSec int      `json:"timeoutSec,omitempty"`
}
type CPU struct {
	Model string   `json:"model"`
	Cores *int     `json:"cores"`
	Usage *float64 `json:"usage"`
}
type Memory struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"usedPercent"`
	SwapTotal   *uint64 `json:"swapTotal"`
	SwapUsed    *uint64 `json:"swapUsed"`
}
type Disk struct {
	Mount       string  `json:"mount"`
	FSType      string  `json:"fstype"`
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	UsedPercent float64 `json:"usedPercent"`
}
type Overview struct {
	Hostname     string   `json:"hostname"`
	OS           string   `json:"os"`
	Platform     string   `json:"platform"`
	Kernel       string   `json:"kernel"`
	Arch         string   `json:"arch"`
	Uptime       *uint64  `json:"uptime"`
	BootTime     *string  `json:"bootTime"`
	IPs          []string `json:"ips"`
	PanelVersion string   `json:"panelVersion"`
	CPU          CPU      `json:"cpu"`
	Load         *struct {
		L1  float64 `json:"l1"`
		L5  float64 `json:"l5"`
		L15 float64 `json:"l15"`
	} `json:"load"`
	Mem   *Memory `json:"mem"`
	Disks []Disk  `json:"disks"`
}

func (h *HostClient) call(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, e := json.Marshal(in)
		if e != nil {
			return e
		}
		body = bytes.NewReader(b)
	}
	req, e := http.NewRequestWithContext(ctx, method, h.base+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("X-Plugin-Token", h.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, e := h.client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	var envelope struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if e = json.NewDecoder(res.Body).Decode(&envelope); e != nil {
		return fmt.Errorf("Host 响应格式无效: %w", e)
	}
	if out != nil && len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		if e = json.Unmarshal(envelope.Data, out); e != nil {
			return e
		}
	}
	if envelope.Code != 0 || res.StatusCode < 200 || res.StatusCode >= 300 {
		return &APIError{Code: envelope.Code, Message: envelope.Msg, Status: res.StatusCode}
	}
	return nil
}
func (h *HostClient) Info(ctx context.Context) (Info, error) {
	var v Info
	e := h.call(ctx, "GET", "/info", nil, &v)
	return v, e
}
func (h *HostClient) KVGet(ctx context.Context, key string) (string, error) {
	var v struct {
		Value string `json:"value"`
	}
	e := h.call(ctx, "GET", "/kv/"+url.PathEscape(key), nil, &v)
	return v.Value, e
}
func (h *HostClient) KVSet(ctx context.Context, key, value string) error {
	return h.call(ctx, "PUT", "/kv/"+url.PathEscape(key), map[string]string{"value": value}, nil)
}
func (h *HostClient) KVDelete(ctx context.Context, key string) error {
	return h.call(ctx, "DELETE", "/kv/"+url.PathEscape(key), nil, nil)
}
func (h *HostClient) KVList(ctx context.Context, prefix string) ([]KVItem, error) {
	var v struct {
		List []KVItem `json:"list"`
	}
	e := h.call(ctx, "GET", "/kv?prefix="+url.QueryEscape(prefix), nil, &v)
	return v.List, e
}
func (h *HostClient) Exec(ctx context.Context, in ExecRequest) (ExecResult, error) {
	var v ExecResult
	e := h.call(ctx, "POST", "/exec", in, &v)
	return v, e
}
func (h *HostClient) FSRead(ctx context.Context, path string) (string, error) {
	var v struct {
		Content string `json:"content"`
	}
	e := h.call(ctx, "GET", "/fs/read?path="+url.QueryEscape(path), nil, &v)
	return v.Content, e
}
func (h *HostClient) FSWrite(ctx context.Context, path, content, mode string) error {
	return h.call(ctx, "POST", "/fs/write", map[string]string{"path": path, "content": content, "mode": mode}, nil)
}
func (h *HostClient) FSMkdir(ctx context.Context, path string) error {
	return h.call(ctx, "POST", "/fs/mkdir", map[string]string{"path": path}, nil)
}
func (h *HostClient) FSDelete(ctx context.Context, path string) error {
	return h.call(ctx, "POST", "/fs/delete", map[string]string{"path": path}, nil)
}
func (h *HostClient) Sysinfo(ctx context.Context) (Overview, error) {
	var v Overview
	e := h.call(ctx, "GET", "/sysinfo", nil, &v)
	return v, e
}
func (h *HostClient) Log(ctx context.Context, level, msg string) error {
	return h.call(ctx, "POST", "/log", map[string]string{"level": level, "msg": msg}, nil)
}
