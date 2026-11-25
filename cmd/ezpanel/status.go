package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"ezpanel/internal/config"
)

func openPanelDatabase(cfg config.Config, mode string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: filepath.Join(cfg.DataDir(), "panel.db")}
	query := url.Values{"mode": []string{mode}, "_pragma": []string{"busy_timeout(5000)"}}
	uri.RawQuery = query.Encode()
	return sql.Open("sqlite", uri.String())
}

func upgradeCheck(cfg config.Config) error {
	db, e := openPanelDatabase(cfg, "ro")
	if e != nil {
		return errors.New("无法读取面板任务状态")
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rows, e := db.QueryContext(ctx, "SELECT id,module,action FROM tasks WHERE status = ? ORDER BY started_at", "running")
	if e != nil {
		return errors.New("无法读取任务表，拒绝在未知状态升级")
	}
	defer rows.Close()
	active := false
	for rows.Next() {
		var id, module, action string
		if e = rows.Scan(&id, &module, &action); e != nil {
			return e
		}
		fmt.Printf("活动任务: %s %s/%s\n", id, module, action)
		active = true
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if active {
		return errors.New("请等待以上任务结束后再升级；面板不会重放中断任务")
	}
	fmt.Println("没有活动任务；可停止面板并创建完整快照")
	return nil
}

// readyCheck 验证真实入口和匿名会话拒绝，不创建登录会话、不执行数据库迁移。
func readyCheck(cfg config.Config) error {
	host := cfg.LocalListenHost()
	scheme := "http"
	transport := &http.Transport{Proxy: nil}
	if cfg.TLSCert != "" {
		scheme = "https"
		b, e := os.ReadFile(cfg.TLSCert)
		if e != nil {
			return errors.New("就绪检查无法读取配置证书")
		}
		roots, e := x509.SystemCertPool()
		if e != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(b) {
			return errors.New("就绪检查证书无效")
		}
		serverName := host
		if public, e := url.Parse(cfg.PublicURL); e == nil && public.Host != "" {
			serverName = public.Hostname()
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: serverName}
	}
	defer transport.CloseIdleConnections()
	origin := scheme + "://" + net.JoinHostPort(host, strconv.Itoa(cfg.Port))
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	entry, e := http.NewRequest(http.MethodGet, origin+"/"+cfg.Entry, nil)
	if e != nil {
		return e
	}
	res, e := client.Do(entry)
	if e != nil {
		return errors.New("面板入口尚未就绪")
	}
	cookies := res.Cookies()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4096))
	res.Body.Close()
	if res.StatusCode != http.StatusFound {
		return errors.New("面板入口响应不符合预期")
	}
	req, e := http.NewRequest(http.MethodGet, origin+"/api/auth/me", nil)
	if e != nil {
		return e
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	res, e = client.Do(req)
	if e != nil {
		return errors.New("面板会话接口尚未就绪")
	}
	defer res.Body.Close()
	var envelope struct {
		Code int `json:"code"`
	}
	if res.StatusCode != http.StatusUnauthorized || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&envelope) != nil || envelope.Code != 1002 {
		return errors.New("匿名会话检查未获得真实认证拒绝")
	}
	fmt.Println("面板已就绪：入口可用，匿名会话按预期拒绝")
	return nil
}
