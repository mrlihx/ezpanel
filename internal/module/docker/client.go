package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 只连接本机Unix socket，不开放或代理远程Docker管理端口。
func engineClient() *http.Client {
	return &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}, ResponseHeaderTimeout: 30 * time.Second}, Timeout: 0}
}
func (m *Module) call(ctx context.Context, method, path string, body any, auth string) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("X-Registry-Auth", auth)
	}
	res, err := m.client.Do(req)
	if err != nil {
		return nil, errors.New("无法连接本机Docker，请检查安装和服务状态")
	}
	if res.StatusCode >= 400 {
		defer res.Body.Close()
		var msg struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&msg)
		if msg.Message == "" {
			msg.Message = res.Status
		}
		return nil, fmt.Errorf("Docker：%s", msg.Message)
	}
	return res, nil
}
func (m *Module) json(ctx context.Context, method, path string, body, out any) error {
	res, err := m.call(ctx, method, path, body, "")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if out == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(out)
}
func (m *Module) stream(ctx context.Context, path string, auth string, log func(string)) error {
	res, err := m.call(ctx, "POST", path, nil, auth)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	decoder := json.NewDecoder(res.Body)
	for {
		var event struct {
			Status, ID, Progress, Error string
			ErrorDetail                 struct{ Message string }
		}
		if err := decoder.Decode(&event); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if event.Error != "" {
			return errors.New(event.Error)
		}
		if event.ErrorDetail.Message != "" {
			return errors.New(event.ErrorDetail.Message)
		}
		if event.Status != "" {
			log(strings.TrimSpace(event.ID + " " + event.Status + " " + event.Progress))
		}
	}
}
