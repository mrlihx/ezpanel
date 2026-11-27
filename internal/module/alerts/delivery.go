package alerts

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const deliveryTimeout = 15 * time.Second

var (
	errDeliveryMetadata = errors.New("禁止访问链路本地或云元数据地址")
	errDeliveryPanel    = errors.New("禁止访问面板、Host API 或插件监听端口")
)

func (m *Module) send(ctx context.Context, item channel, title, message string) error {
	settings, err := item.readSettings(m.cfg.AESKey)
	if err != nil {
		return err
	}
	if id, ok := strings.CutPrefix(item.Kind, "plugin:"); ok && m.providers != nil {
		return m.providers.SendProviderNotification(ctx, id, ProviderNotice{ChannelID: item.ID, Target: settings.Target, Title: title, Message: message})
	}
	switch item.Kind {
	case "email":
		return m.sendEmail(ctx, settings, title, message)
	case "webhook":
		return m.sendWebhook(ctx, settings, title, message)
	default:
		return errors.New("告警渠道类型无效")
	}
}

type ProviderNotice struct {
	ChannelID string `json:"channelId"`
	Target    string `json:"target"`
	Title     string `json:"title"`
	Message   string `json:"message"`
}

type NotificationProviders interface {
	NotificationProviderReady(id string) bool
	SendProviderNotification(ctx context.Context, id string, notice ProviderNotice) error
}

func (m *Module) SetNotificationProviders(providers NotificationProviders) { m.providers = providers }

func (m *Module) PluginProviderInUse(id string) error {
	var count int64
	if err := m.db.Model(&channel{}).Where("kind = ?", "plugin:"+id).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("该插件仍有通知渠道，请先移除渠道")
	}
	return nil
}

type PluginChannel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (m *Module) PluginChannels(ctx context.Context) ([]PluginChannel, error) {
	rows := []PluginChannel{}
	err := m.db.WithContext(ctx).Model(&channel{}).Select("id, name").Where("enabled = ?", true).Order("name, id").Find(&rows).Error
	return rows, err
}

func (m *Module) SendPluginNotice(ctx context.Context, pluginID, channelID, title, message string) error {
	var item channel
	if err := m.db.WithContext(ctx).First(&item, "id = ? AND enabled = ?", channelID, true).Error; err != nil {
		return err
	}
	title = "[" + pluginID + "] " + title
	err := m.send(ctx, item, title, message)
	return errors.Join(err, m.writeDelivery("plugin:"+pluginID, title, item, err))
}

func (m *Module) sendWebhook(ctx context.Context, s channelSettings, title, message string) error {
	u, err := url.Parse(s.URL)
	if err != nil {
		return errors.New("Webhook 地址无效")
	}
	var params map[string]any
	if s.Params == nil {
		params = map[string]any{"title": title, "message": message}
	} else {
		replacer := strings.NewReplacer("{{title}}", title, "{{message}}", message)
		params = make(map[string]any, len(s.Params))
		for key, value := range s.Params {
			params[key] = renderWebhookParams(value, replacer)
		}
	}

	var body io.Reader
	var contentType string
	switch {
	case s.Method == http.MethodGet || s.Format == "form":
		values := make(url.Values)
		for key, value := range params {
			if str, ok := value.(string); ok {
				values.Add(key, str)
			} else {
				encoded, err := json.Marshal(value)
				if err != nil {
					return errors.New("Webhook 参数编码失败")
				}
				values.Add(key, string(encoded))
			}
		}
		if s.Method == http.MethodGet {
			if query := values.Encode(); query != "" {
				// 保留已配置查询串的原始编码与顺序，避免改写 URL 签名参数。
				if u.RawQuery != "" {
					u.RawQuery += "&"
				}
				u.RawQuery += query
			}
		} else {
			body = strings.NewReader(values.Encode())
			contentType = "application/x-www-form-urlencoded"
		}
	default:
		data, err := json.Marshal(params)
		if err != nil {
			return errors.New("Webhook 参数编码失败")
		}
		body = strings.NewReader(string(data))
		contentType = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, s.Method, u.String(), body)
	if err != nil {
		return errors.New("无法构造 Webhook 请求")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, value := range s.Headers {
		req.Header.Set(key, value)
	}

	transport := &http.Transport{
		DialContext: m.dialDelivery, DisableKeepAlives: true,
		// 只有渠道显式关闭证书验证时才允许自签名证书。
		TLSClientConfig: &tls.Config{InsecureSkipVerify: !s.VerifyTLS},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: deliveryTimeout,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			first := via[0]
			if len(via) >= 5 || next.Method != first.Method || next.URL.User != nil ||
				next.URL.Scheme != first.URL.Scheme || !strings.EqualFold(next.URL.Host, first.URL.Host) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	res, err := client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, errDeliveryPanel):
			return errDeliveryPanel
		case errors.Is(err, errDeliveryMetadata):
			return errDeliveryMetadata
		default:
			return errors.New("Webhook 连接失败，请检查目标地址、证书或网络")
		}
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("Webhook 返回 HTTP %d", res.StatusCode)
	}
	return nil
}

func renderWebhookParams(value any, replacer *strings.Replacer) any {
	switch v := value.(type) {
	case string:
		return replacer.Replace(v)
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, child := range v {
			result[key] = renderWebhookParams(child, replacer)
		}
		return result
	case []any:
		result := make([]any, len(v))
		for i, child := range v {
			result[i] = renderWebhookParams(child, replacer)
		}
		return result
	default:
		return value
	}
}

func (m *Module) sendEmail(parent context.Context, s channelSettings, title, message string) error {
	ctx, cancel := context.WithTimeout(parent, deliveryTimeout)
	defer cancel()
	conn, err := m.dialDelivery(ctx, "tcp", net.JoinHostPort(s.SMTPHost, strconv.Itoa(s.SMTPPort)))
	if err != nil {
		if errors.Is(err, errDeliveryMetadata) || errors.Is(err, errDeliveryPanel) {
			return err
		}
		return errors.New("SMTP 连接失败，请检查服务器地址或网络")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	tlsConfig := &tls.Config{ServerName: s.SMTPHost}
	var session net.Conn = conn
	if s.SMTPMode == "tls" {
		secure := tls.Client(conn, tlsConfig)
		if err := secure.HandshakeContext(ctx); err != nil {
			return errors.New("SMTP TLS 握手失败，请检查加密方式和证书")
		}
		session = secure
	}
	client, err := smtp.NewClient(session, s.SMTPHost)
	if err != nil {
		return smtpFailure("SMTP 握手失败", err)
	}
	defer client.Close()
	if s.SMTPMode == "starttls" {
		if err := client.StartTLS(tlsConfig); err != nil {
			return smtpFailure("SMTP STARTTLS 握手失败", err)
		}
	}

	if s.Password != "" {
		username := s.Username
		if username == "" {
			username = s.From
		}
		ok, methods := client.Extension("AUTH")
		if !ok {
			return errors.New("SMTP 服务器未启用认证")
		}
		auth := smtp.PlainAuth("", username, s.Password, s.SMTPHost)
		advertised := " " + strings.ToUpper(methods) + " "
		if !strings.Contains(advertised, " PLAIN ") && strings.Contains(advertised, " LOGIN ") {
			auth = smtpLoginAuth{username: username, password: s.Password}
		}
		if err := client.Auth(auth); err != nil {
			return smtpFailure("SMTP 认证失败", err)
		}
	}
	if err := client.Mail(s.From); err != nil {
		return smtpFailure("SMTP 服务器拒绝发件人", err)
	}
	for _, recipient := range s.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return smtpFailure("SMTP 服务器拒绝收件人", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return smtpFailure("SMTP 服务器拒绝邮件", err)
	}

	subject := mime.BEncoding.Encode("UTF-8", strings.NewReplacer("\r", " ", "\n", " ").Replace(title))
	if _, err := fmt.Fprintf(writer, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n",
		s.From, strings.Join(s.Recipients, ",\r\n "), subject, time.Now().Format(time.RFC1123Z)); err != nil {
		return smtpFailure("SMTP 写入邮件失败", err)
	}
	content := quotedprintable.NewWriter(writer)
	if _, err := io.WriteString(content, message); err != nil {
		return smtpFailure("SMTP 写入邮件失败", err)
	}
	if err := content.Close(); err != nil {
		return smtpFailure("SMTP 写入邮件失败", err)
	}
	if err := writer.Close(); err != nil {
		return smtpFailure("SMTP 服务器未确认收件", err)
	}
	// DATA 已确认投递，退出会话失败不能将已发送的邮件记为失败。
	_ = client.Quit()
	return nil
}

type smtpLoginAuth struct {
	username string
	password string
}

func (a smtpLoginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		return "", nil, errors.New("SMTP 认证需要 TLS")
	}
	return "LOGIN", nil, nil
}

func (a smtpLoginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:", "user name:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	default:
		return nil, errors.New("SMTP 认证方式不受支持")
	}
}

func smtpFailure(message string, err error) error {
	var reply *textproto.Error
	if errors.As(err, &reply) {
		return fmt.Errorf("%s（SMTP %d）", message, reply.Code)
	}
	return errors.New(message)
}

func (m *Module) dialDelivery(ctx context.Context, network, address string) (net.Conn, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errors.New("投递目标地址无效")
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return nil, errors.New("投递目标端口无效")
	}

	cfg := m.cfg.Current()
	blockedPort := port == cfg.Port
	if m.blockedPorts != nil {
		for _, value := range m.blockedPorts() {
			blockedPort = blockedPort || port == value
		}
	}
	if public, err := url.Parse(cfg.PublicURL); err == nil && public.Hostname() != "" {
		publicPort, _ := strconv.Atoi(public.Port())
		if publicPort == 0 {
			publicPort = 443
			if public.Scheme == "http" {
				publicPort = 80
			}
		}
		if port == publicPort {
			blockedPort = true
			if strings.EqualFold(strings.TrimSuffix(host, "."), strings.TrimSuffix(public.Hostname(), ".")) {
				return nil, errDeliveryPanel
			}
		}
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("投递目标解析失败")
	}
	var interfaces []net.Addr
	if blockedPort {
		interfaces, err = net.InterfaceAddrs()
		if err != nil {
			return nil, errDeliveryPanel
		}
	}
	for _, addr := range ips {
		ip := addr.IP
		if ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() ||
			ip.Equal(net.IPv4(100, 100, 100, 200)) || ip.Equal(net.ParseIP("fd00:ec2::254")) {
			return nil, errDeliveryMetadata
		}
		if blockedPort && ip.IsLoopback() {
			return nil, errDeliveryPanel
		}
		for _, local := range interfaces {
			if ipNet, ok := local.(*net.IPNet); ok && ip.Equal(ipNet.IP) {
				return nil, errDeliveryPanel
			}
		}
	}

	// 用已校验的 IP 拨号，避免域名在连接前被重新解析。
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for _, addr := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), portText))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("投递目标连接失败")
}
