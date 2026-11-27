package panelsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
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

type FileMetadata struct {
	Name       string    `json:"name"`
	Mode       string    `json:"mode"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modifiedAt"`
	Directory  bool      `json:"directory"`
	Link       bool      `json:"link"`
}

type FilePage struct {
	Entries    []FileMetadata `json:"entries"`
	NextOffset int            `json:"nextOffset"`
	More       bool           `json:"more"`
}

type FileStreamChunk struct {
	Body       io.ReadCloser
	Size       int64
	NextOffset int64
	Inode      uint64
	ModTime    time.Time
}

// FSOpen 读取不超过 50 MiB 的完整文件。更大的文件请使用 FSOpenRange 分段读取。
func (h *HostClient) FSOpen(ctx context.Context, path string) (io.ReadCloser, error) {
	res, err := h.fileStream(ctx, http.MethodGet, "/fs/stream?path="+url.QueryEscape(path), nil)
	if err != nil {
		return nil, err
	}
	return res.Body, nil
}

func (h *HostClient) FSOpenRange(ctx context.Context, path string, offset, limit int64) (*FileStreamChunk, error) {
	query := url.Values{}
	query.Set("path", path)
	if offset != 0 {
		query.Set("offset", strconv.FormatInt(offset, 10))
	}
	if limit != 0 {
		query.Set("limit", strconv.FormatInt(limit, 10))
	}
	res, err := h.fileStream(ctx, http.MethodGet, "/fs/stream?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	chunk := &FileStreamChunk{Body: res.Body}
	if v, e := strconv.ParseInt(res.Header.Get("X-File-Size"), 10, 64); e == nil {
		chunk.Size = v
	}
	if v, e := strconv.ParseInt(res.Header.Get("X-Next-Offset"), 10, 64); e == nil {
		chunk.NextOffset = v
	}
	if v, e := strconv.ParseUint(res.Header.Get("X-File-Inode"), 10, 64); e == nil {
		chunk.Inode = v
	}
	if raw := res.Header.Get("X-File-ModTime"); raw != "" {
		if t, e := time.Parse(time.RFC3339Nano, raw); e == nil {
			chunk.ModTime = t
		}
	}
	return chunk, nil
}

func (h *HostClient) FSUpload(ctx context.Context, path string, content io.Reader) error {
	res, err := h.fileStream(ctx, http.MethodPut, "/fs/stream?path="+url.QueryEscape(path), content)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, res.Body)
	return errors.Join(err, res.Body.Close())
}

func (h *HostClient) fileStream(ctx context.Context, method, target string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.base+target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plugin-Token", h.token)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	res, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res, nil
	}
	defer res.Body.Close()
	failure := APIError{Status: res.StatusCode, Message: "文件传输失败"}
	var out struct {
		Code int
		Msg  string
	}
	if json.NewDecoder(io.LimitReader(res.Body, 16<<10)).Decode(&out) == nil {
		failure.Code, failure.Message = out.Code, out.Msg
	}
	return nil, &failure
}

func (h *HostClient) FSStat(ctx context.Context, path string) (FileMetadata, error) {
	var out FileMetadata
	err := h.call(ctx, http.MethodGet, "/fs/stat?path="+url.QueryEscape(path), nil, &out)
	return out, err
}

func (h *HostClient) FSList(ctx context.Context, path string, offset int) (FilePage, error) {
	var out FilePage
	err := h.call(ctx, http.MethodGet, "/fs/list?path="+url.QueryEscape(path)+"&offset="+strconv.Itoa(offset), nil, &out)
	return out, err
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

func (h *HostClient) FSHash(ctx context.Context, path string) (string, error) {
	var out struct {
		SHA256 string `json:"sha256"`
	}
	err := h.call(ctx, http.MethodGet, "/fs/hash?path="+url.QueryEscape(path), nil, &out)
	return out.SHA256, err
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
