package file

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

func (m *Module) remote(c *gin.Context) {
	var in struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Dir    string `json:"dir"`
		Name   string `json:"name"`
		Branch string `json:"branch"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.URL = strings.TrimSpace(in.URL)
	if (in.Type != "download" && in.Type != "git") || len(in.URL) > 8192 || len(in.Branch) > 255 || strings.ContainsAny(in.URL+in.Branch, "\x00\r\n") {
		response.Fail(c, response.Invalid, "下载或克隆参数无效")
		return
	}
	sourcePath := ""
	if in.Type == "git" && regexp.MustCompile(`^[a-zA-Z0-9._-]+@[a-zA-Z0-9.-]+:.+$`).MatchString(in.URL) {
		_, sourcePath, _ = strings.Cut(in.URL, ":")
	} else {
		parsed, err := url.Parse(in.URL)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && (in.Type != "git" || parsed.Scheme != "ssh")) {
			response.Fail(c, response.Invalid, "请输入HTTP(S)链接或有效的Git SSH仓库地址")
			return
		}
		sourcePath = parsed.Path
	}
	if in.Name == "" {
		in.Name = filepath.Base(strings.TrimRight(sourcePath, "/"))
		if in.Type == "git" {
			in.Name = strings.TrimSuffix(in.Name, ".git")
		}
		if in.Name == "." || in.Name == "" {
			in.Name = "download"
		}
	}
	if !nameValid(in.Name) {
		response.Fail(c, response.Invalid, "保存名称必须为单个文件或目录名称")
		return
	}
	if !filepath.IsAbs(in.Dir) {
		response.Fail(c, response.Invalid, "保存目录须为绝对路径")
		return
	}
	dest, err := m.policy.CheckLink(filepath.Join(in.Dir, in.Name), true)
	if err != nil {
		fail(c, err)
		return
	}
	if _, err = os.Lstat(dest); !os.IsNotExist(err) {
		fail(c, errConflict)
		return
	}
	git := ""
	if in.Type == "git" {
		git, err = executor.LookPath("git")
		if err != nil {
			response.Fail(c, response.System, "系统未安装Git，请先安装Git后再克隆")
			return
		}
	}
	m.submit(c, in.Type, []string{dest}, func(ctx context.Context, target string) error {
		if in.Type == "download" {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, in.URL, nil)
			if err != nil {
				return err
			}
			transport := m.safeDownloadTransport()
			defer transport.CloseIdleConnections()
			client := &http.Client{
				Transport: transport,
				Timeout:   time.Hour,
				CheckRedirect: func(r *http.Request, via []*http.Request) error {
					if len(via) > 5 {
						return errors.New("重定向超过5次")
					}
					if r.URL.Scheme != "http" && r.URL.Scheme != "https" {
						return errors.New("仅允许通过 HTTP/HTTPS 下载")
					}
					return nil
				},
			}
			res, err := client.Do(req)
			if err != nil {
				return err
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusOK {
				return fmt.Errorf("下载返回HTTP %d", res.StatusCode)
			}
			return m.atomic(ctx, target, res.Body, false, 2<<30, 0755)
		}
		if _, err := m.policy.CheckLink(target, true); err != nil {
			return err
		}
		if err := os.Mkdir(target, 0755); err != nil {
			return err
		}
		cleanupDir := true
		defer func() {
			if cleanupDir {
				_ = os.RemoveAll(target)
			}
		}()
		if err := os.Chmod(target, 0755); err != nil {
			return err
		}
		args := []string{"-c", "umask 022; exec \"$@\"", "ezpanel-git", git, "-c", "protocol.file.allow=never", "-c", "protocol.ext.allow=never", "clone"}
		if in.Branch != "" {
			args = append(args, "--branch", in.Branch)
		}
		args = append(args, "--", in.URL, target)
		env := append(executor.DefaultEnv(), "GIT_TERMINAL_PROMPT=0", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
		_, err := executor.RunFixed(ctx, time.Hour, filepath.Dir(target), env, "/bin/sh", args...)
		if err == nil {
			cleanupDir = false
		}
		return err
	})
}

func (m *Module) safeDownloadTransport() *http.Transport {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 30 * time.Second,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, portText, err := net.SplitHostPort(address)
			if err != nil {
				return nil, errors.New("下载地址无效")
			}
			port, err := strconv.Atoi(portText)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				return nil, errors.New("下载域名解析失败")
			}
			panelPort := m.cfg.Current().Port
			var local []net.IP
			if addrs, err := net.InterfaceAddrs(); err == nil {
				for _, a := range addrs {
					if ip, _, err := net.ParseCIDR(a.String()); err == nil {
						local = append(local, ip)
					}
				}
			}
			for _, resolved := range ips {
				ip := resolved.IP
				if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
					ip.Equal(net.ParseIP("100.100.100.200")) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
					return nil, errors.New("禁止从链路本地或云元数据地址下载")
				}
				isLocal := ip.IsLoopback()
				for _, own := range local {
					if ip.Equal(own) {
						isLocal = true
						break
					}
				}
				if isLocal && port == panelPort {
					return nil, errors.New("禁止从本机面板管理端口下载")
				}
			}
			var last error
			for _, resolved := range ips {
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), portText))
				if err == nil {
					return conn, nil
				}
				last = err
			}
			return nil, last
		},
	}
}
