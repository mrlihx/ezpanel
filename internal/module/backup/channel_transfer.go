package backup

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/store"
	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type channelTarget struct {
	backupChannel
	url      *url.URL
	password string
	storage  *channelStorage
}

func (s *channelStorage) target(ctx context.Context, id string, uploading bool) (channelTarget, error) {
	var row backupChannel
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return channelTarget{}, errors.New("远程备份渠道不存在")
	}
	if uploading && !row.Enabled {
		return channelTarget{}, errors.New("远程备份渠道已停用")
	}
	return s.openTarget(row)
}

func (s *channelStorage) openTarget(row backupChannel) (channelTarget, error) {
	if strings.HasPrefix(row.Protocol, PluginProtocolPrefix) {
		return channelTarget{backupChannel: row, storage: s}, nil
	}
	address, err := url.Parse(row.Endpoint)
	if err != nil {
		return channelTarget{}, errors.New("远程备份渠道地址无效")
	}
	password, err := store.Decrypt(s.key, row.Secret)
	if err != nil {
		return channelTarget{}, errors.New("无法读取远程备份渠道凭据")
	}
	return channelTarget{backupChannel: row, url: address, password: password, storage: s}, nil
}

func (target channelTarget) remotePath(key string) string {
	return path.Join("/", target.url.Path, key)
}

func (target channelTarget) address(defaultPort string) string {
	port := target.url.Port()
	if port == "" {
		port = defaultPort
	}
	return net.JoinHostPort(target.url.Hostname(), port)
}

func backupDial(ctx context.Context, network, address string) (net.Conn, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, network, address)
	if err == nil {
		context.AfterFunc(ctx, func() { _ = conn.Close() })
	}
	return conn, err
}

func (target channelTarget) ftp(ctx context.Context) (*ftp.ServerConn, error) {
	conn, err := ftp.Dial(target.address("21"), ftp.DialWithDialFunc(func(network, address string) (net.Conn, error) {
		return backupDial(ctx, network, address)
	}))
	if err != nil {
		return nil, errors.New("无法连接 FTP 备份服务器")
	}
	if err := conn.Login(target.Username, target.password); err != nil {
		_ = conn.Quit()
		return nil, errors.New("FTP 登录失败")
	}
	return conn, nil
}

func (target channelTarget) sftp(ctx context.Context) (*sftp.Client, error) {
	address := target.address("22")
	conn, err := backupDial(ctx, "tcp", address)
	if err != nil {
		return nil, errors.New("无法连接 SFTP 备份服务器")
	}
	fingerprint := ""
	config := &ssh.ClientConfig{
		User: target.Username, Auth: []ssh.AuthMethod{ssh.Password(target.password)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			fingerprint = ssh.FingerprintSHA256(key)
			if target.Fingerprint != "" && fingerprint != target.Fingerprint {
				return errors.New("SFTP 服务器公钥已变化，确认服务器后请重新填写密码并保存渠道")
			}
			return nil
		},
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	session, channels, requests, err := ssh.NewClientConn(conn, address, config)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("SFTP 认证失败：%w", err)
	}
	_ = conn.SetDeadline(time.Time{})
	client, err := sftp.NewClient(ssh.NewClient(session, channels, requests))
	if err != nil {
		_ = session.Close()
		return nil, errors.New("SFTP 子系统不可用")
	}
	// 首次认证成功后自动记录主机公钥，后续连接校验服务器身份。
	if target.Fingerprint == "" && target.storage != nil {
		result := target.storage.db.WithContext(ctx).Model(&backupChannel{}).
			Where("id = ? AND endpoint = ? AND (fingerprint = '' OR fingerprint = ?)", target.ID, target.Endpoint, fingerprint).
			Update("fingerprint", fingerprint)
		if result.Error != nil || result.RowsAffected == 0 {
			_ = client.Close()
			return nil, errors.New("无法记录 SFTP 服务器公钥，请重新连接")
		}
	}
	return client, nil
}

var webDAVClient = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func (target channelTarget) webdav(ctx context.Context, method, key string, body io.Reader) (io.ReadCloser, error) {
	address := *target.url
	address.Path, address.RawPath = target.remotePath(key), ""
	if (method == "PROPFIND" || method == "MKCOL") && !strings.HasSuffix(address.Path, "/") {
		address.Path += "/"
	}
	request, err := http.NewRequestWithContext(ctx, method, address.String(), body)
	if err != nil {
		return nil, err
	}
	if target.Username != "" {
		request.SetBasicAuth(target.Username, target.password)
	}
	if method == http.MethodPut {
		// 归档名保留原样，同名对象不能被覆盖。
		request.Header.Set("If-None-Match", "*")
	}
	if method == "PROPFIND" {
		request.Header.Set("Depth", "0")
		request.Header.Set("Content-Type", "application/xml; charset=utf-8")
	}
	if file, ok := body.(*os.File); ok {
		if info, err := file.Stat(); err == nil {
			request.ContentLength = info.Size()
		}
	}
	res, err := webDAVClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("WebDAV 连接失败：%w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		alreadyExists := method == "MKCOL" && res.StatusCode == http.StatusMethodNotAllowed
		alreadyGone := method == http.MethodDelete && res.StatusCode == http.StatusNotFound
		if !alreadyExists && !alreadyGone {
			res.Body.Close()
			return nil, fmt.Errorf("WebDAV 返回 HTTP %d", res.StatusCode)
		}
	}
	return res.Body, nil
}

func (target channelTarget) test(ctx context.Context) error {
	switch target.Protocol {
	case "ftp":
		client, err := target.ftp(ctx)
		if err != nil {
			return err
		}
		defer client.Quit()
		if err = client.ChangeDir(target.remotePath("")); err != nil {
			return errors.New("无法访问 FTP 备份目录，请检查路径与权限")
		}
	case "sftp":
		client, err := target.sftp(ctx)
		if err != nil {
			return err
		}
		defer client.Close()
		info, err := client.Stat(target.remotePath(""))
		if err != nil || !info.IsDir() {
			return errors.New("无法访问 SFTP 备份目录，请检查路径与权限")
		}
	case "webdav":
		const props = `<propfind xmlns="DAV:"><prop><resourcetype/></prop></propfind>`
		body, err := target.webdav(ctx, "PROPFIND", "", strings.NewReader(props))
		if err != nil {
			return err
		}
		defer body.Close()
		var result struct {
			XMLName xml.Name `xml:"DAV: multistatus"`
			Props   []struct {
				Status     string    `xml:"status"`
				Collection *struct{} `xml:"prop>resourcetype>collection"`
			} `xml:"response>propstat"`
		}
		if err := xml.NewDecoder(io.LimitReader(body, 64<<10)).Decode(&result); err != nil {
			return errors.New("服务器未返回有效的 WebDAV 目录信息")
		}
		for _, prop := range result.Props {
			status := strings.Fields(prop.Status)
			if prop.Collection != nil && len(status) >= 2 && status[1] == "200" {
				return nil
			}
		}
		return errors.New("无法访问 WebDAV 备份目录，请检查路径与权限")
	default:
		return errors.New("备份渠道类型无效")
	}
	return nil
}

func (s *channelStorage) Upload(ctx context.Context, id, key string, source io.Reader) (string, error) {
	target, err := s.target(ctx, id, true)
	if err != nil {
		return "", err
	}
	if key == "" || key == "." || key == ".." || path.IsAbs(key) || path.Clean(key) != key ||
		strings.HasPrefix(key, "../") || strings.Contains(key, "\\") || strings.IndexFunc(key, unicode.IsControl) >= 0 {
		return "", errors.New("备份相对路径无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	if strings.HasPrefix(target.Protocol, PluginProtocolPrefix) {
		body, err := s.callPlugin(ctx, target.backupChannel, "upload", key, source)
		if err != nil {
			return "", err
		}
		return key, body.Close()
	}
	parents := strings.Split(key, "/")
	parents = parents[:len(parents)-1]
	switch target.Protocol {
	case "ftp":
		client, err := target.ftp(ctx)
		if err != nil {
			return "", err
		}
		defer client.Quit()
		for i := range parents {
			dir := target.remotePath(strings.Join(parents[:i+1], "/"))
			if err = client.MakeDir(dir); err != nil {
				if err = client.ChangeDir(dir); err != nil {
					return "", fmt.Errorf("FTP 创建备份目录 %s 失败: %w", dir, err)
				}
			}
		}
		if err = client.Stor(target.remotePath(key), source); err != nil {
			return "", fmt.Errorf("FTP 上传失败：%w", err)
		}
	case "sftp":
		client, err := target.sftp(ctx)
		if err != nil {
			return "", err
		}
		defer client.Close()
		if len(parents) > 0 {
			if err := client.MkdirAll(target.remotePath(path.Dir(key))); err != nil {
				return "", fmt.Errorf("SFTP 创建备份目录失败: %w", err)
			}
		}
		file, err := client.OpenFile(target.remotePath(key), os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(file, source)
		if err = errors.Join(err, file.Close()); err != nil {
			_ = client.Remove(target.remotePath(key))
			return "", err
		}
	case "webdav":
		for i := range parents {
			dir := strings.Join(parents[:i+1], "/")
			res, err := target.webdav(ctx, "MKCOL", dir, nil)
			if err != nil {
				return "", fmt.Errorf("WebDAV 创建备份目录 %s 失败: %w", dir, err)
			}
			res.Close()
		}
		res, err := target.webdav(ctx, http.MethodPut, key, source)
		if err != nil {
			return "", err
		}
		res.Close()
	}
	return key, nil
}

type channelDownload struct {
	io.ReadCloser
	finish func()
}

func (download channelDownload) Close() error {
	err := download.ReadCloser.Close()
	download.finish()
	return err
}

func (s *channelStorage) Open(ctx context.Context, id, key string) (reader io.ReadCloser, err error) {
	target, err := s.target(ctx, id, false)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer func() {
		if err != nil {
			cancel()
		}
	}()
	if strings.HasPrefix(target.Protocol, PluginProtocolPrefix) {
		body, err := s.callPlugin(ctx, target.backupChannel, "open", key, nil)
		if err != nil {
			return nil, err
		}
		return channelDownload{body, cancel}, nil
	}
	switch target.Protocol {
	case "ftp":
		client, err := target.ftp(ctx)
		if err != nil {
			return nil, err
		}
		file, err := client.Retr(target.remotePath(key))
		if err != nil {
			_ = client.Quit()
			return nil, err
		}
		return channelDownload{file, func() { _ = client.Quit(); cancel() }}, nil
	case "sftp":
		client, err := target.sftp(ctx)
		if err != nil {
			return nil, err
		}
		file, err := client.Open(target.remotePath(key))
		if err != nil {
			_ = client.Close()
			return nil, err
		}
		return channelDownload{file, func() { _ = client.Close(); cancel() }}, nil
	default:
		res, err := target.webdav(ctx, http.MethodGet, key, nil)
		if err != nil {
			return nil, err
		}
		return channelDownload{res, cancel}, nil
	}
}

func (s *channelStorage) Delete(ctx context.Context, id, key string) error {
	target, err := s.target(ctx, id, false)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	if strings.HasPrefix(target.Protocol, PluginProtocolPrefix) {
		body, err := s.callPlugin(ctx, target.backupChannel, "delete", key, nil)
		if err != nil {
			return err
		}
		return body.Close()
	}
	switch target.Protocol {
	case "ftp":
		client, err := target.ftp(ctx)
		if err != nil {
			return err
		}
		defer client.Quit()
		return client.Delete(target.remotePath(key))
	case "sftp":
		client, err := target.sftp(ctx)
		if err != nil {
			return err
		}
		defer client.Close()
		err = client.Remove(target.remotePath(key))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	default:
		res, err := target.webdav(ctx, http.MethodDelete, key, nil)
		if err != nil {
			return err
		}
		return res.Close()
	}
}
