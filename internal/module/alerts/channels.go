package alerts

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/http/httpguts"
	"gorm.io/gorm"
)

type channel struct {
	ID        string
	Kind      string
	Name      string
	Enabled   bool
	IsDefault bool
	Secret    string `json:"-"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (channel) TableName() string { return "alert_channels" }

type channelSettings struct {
	SMTPHost   string
	SMTPPort   int
	SMTPMode   string
	From       string
	Username   string
	Password   string
	Recipients []string

	URL       string
	Method    string
	Format    string
	VerifyTLS bool
	Params    map[string]any
	Headers   map[string]string
}

type channelDTO struct {
	ID            string   `json:"id,omitempty"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	Enabled       bool     `json:"enabled"`
	IsDefault     bool     `json:"isDefault"`
	SMTPHost      string   `json:"smtpHost,omitempty"`
	SMTPPort      int      `json:"smtpPort,omitempty"`
	SMTPMode      string   `json:"smtpMode,omitempty"`
	From          string   `json:"from,omitempty"`
	Username      string   `json:"username,omitempty"`
	Password      string   `json:"password,omitempty"`
	ClearPassword bool     `json:"clearPassword,omitempty"`
	HasPassword   bool     `json:"hasPassword,omitempty"`
	Recipients    []string `json:"recipients,omitempty"`
	URL           string   `json:"url,omitempty"`
	HasURL        bool     `json:"hasURL,omitempty"`
	Method        string   `json:"method,omitempty"`
	Format        string   `json:"format,omitempty"`
	VerifyTLS     *bool    `json:"verifyTLS,omitempty"`
	Params        string   `json:"params,omitempty"`
	HasParams     bool     `json:"hasParams,omitempty"`
	Headers       string   `json:"headers,omitempty"`
	HasHeaders    bool     `json:"hasHeaders,omitempty"`
}

func (item channel) dto(settings channelSettings) channelDTO {
	dto := channelDTO{ID: item.ID, Kind: item.Kind, Name: item.Name, Enabled: item.Enabled, IsDefault: item.IsDefault}
	if item.Kind == "email" {
		dto.SMTPHost, dto.SMTPPort, dto.SMTPMode = settings.SMTPHost, settings.SMTPPort, settings.SMTPMode
		dto.From, dto.Username, dto.Recipients = settings.From, settings.Username, settings.Recipients
		dto.HasPassword = settings.Password != ""
	} else {
		dto.Method, dto.Format, dto.VerifyTLS = settings.Method, settings.Format, &settings.VerifyTLS
		dto.HasURL = settings.URL != ""
		dto.HasParams = settings.Params != nil
		dto.HasHeaders = len(settings.Headers) > 0
	}
	return dto
}

func (item channel) readSettings(key string) (channelSettings, error) {
	var settings channelSettings
	raw, err := store.Decrypt(key, item.Secret)
	if err == nil {
		dec := json.NewDecoder(strings.NewReader(raw))
		dec.UseNumber()
		err = dec.Decode(&settings)
	}
	if err != nil {
		return settings, errors.New("无法读取告警渠道配置")
	}
	return settings, nil
}

func (in channelDTO) settings(kind string, old channelSettings) (channelSettings, error) {
	switch kind {
	case "email":
		s := channelSettings{
			SMTPHost: strings.TrimSpace(in.SMTPHost), SMTPPort: in.SMTPPort, SMTPMode: in.SMTPMode,
			Username: strings.TrimSpace(in.Username), Password: old.Password,
		}
		if strings.HasPrefix(s.SMTPHost, "[") && strings.HasSuffix(s.SMTPHost, "]") {
			s.SMTPHost = s.SMTPHost[1 : len(s.SMTPHost)-1]
		}
		if s.SMTPHost == "" || len(s.SMTPHost) > 253 || (net.ParseIP(s.SMTPHost) == nil &&
			(strings.ContainsAny(s.SMTPHost, ":/\\?#@%[]") || strings.ContainsFunc(s.SMTPHost, func(r rune) bool {
				return unicode.IsSpace(r) || unicode.IsControl(r)
			}))) {
			return s, errors.New("请输入有效的 SMTP 主机名或 IP 地址")
		}
		if s.SMTPPort < 1 || s.SMTPPort > 65535 {
			return s, errors.New("SMTP 端口须在 1 至 65535 之间")
		}
		if s.SMTPMode == "" {
			s.SMTPMode = "starttls"
		}
		switch s.SMTPMode {
		case "tls", "starttls", "plain":
		default:
			return s, errors.New("SMTP 加密方式无效")
		}
		if in.ClearPassword && in.Password != "" {
			return s, errors.New("清除已保存密码时不能同时填写新密码")
		}
		if in.ClearPassword {
			s.Password = ""
		} else if in.Password != "" {
			s.Password = in.Password
		}
		if strings.ContainsRune(s.Username, 0) || strings.ContainsRune(s.Password, 0) {
			return s, errors.New("SMTP 认证信息无效")
		}
		if s.SMTPMode == "plain" && (s.Password != "" || s.Username != "") {
			return s, errors.New("SMTP 认证需要 TLS 或 STARTTLS")
		}
		if s.Username != "" && s.Password == "" {
			return s, errors.New("请输入 SMTP 密码")
		}
		from, err := smtpMailbox(in.From)
		if err != nil {
			return s, errors.New("发件人邮箱无效")
		}
		s.From = from
		if len(in.Recipients) == 0 {
			return s, errors.New("请至少填写一个收件人")
		}
		for _, recipient := range in.Recipients {
			address, err := smtpMailbox(recipient)
			if err != nil {
				return s, errors.New("存在无效的收件人邮箱")
			}
			s.Recipients = append(s.Recipients, address)
		}
		return s, nil
	case "webhook":
		s := channelSettings{URL: old.URL, Params: old.Params, Headers: old.Headers, VerifyTLS: true}
		if old.URL != "" {
			s.VerifyTLS = old.VerifyTLS
		}
		if in.VerifyTLS != nil {
			s.VerifyTLS = *in.VerifyTLS
		}
		if value := strings.TrimSpace(in.URL); value != "" {
			s.URL = value
		}
		u, err := url.Parse(s.URL)
		if err != nil || u.Hostname() == "" || u.User != nil || u.Fragment != "" ||
			(u.Scheme != "http" && u.Scheme != "https") || strings.Contains(u.Hostname(), "%") {
			return s, errors.New("Webhook 地址须为无内嵌账号的 HTTP 或 HTTPS URL")
		}
		if raw := u.Port(); raw != "" {
			port, err := strconv.Atoi(raw)
			if err != nil || port < 1 || port > 65535 {
				return s, errors.New("Webhook 端口无效")
			}
		}
		s.Method = strings.ToUpper(strings.TrimSpace(in.Method))
		switch s.Method {
		case "":
			s.Method = http.MethodPost
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			return s, errors.New("Webhook 请求方式须为 GET、POST、PUT 或 PATCH")
		}
		s.Format = in.Format
		switch s.Format {
		case "":
			s.Format = "json"
		case "json", "form":
		default:
			return s, errors.New("Webhook 请求格式须为 JSON 或表单")
		}

		if raw := strings.TrimSpace(in.Params); raw != "" {
			dec := json.NewDecoder(strings.NewReader(raw))
			dec.UseNumber()
			s.Params = nil
			if err := dec.Decode(&s.Params); err != nil || s.Params == nil || dec.Decode(new(any)) != io.EOF {
				return s, errors.New("Webhook 参数须为 JSON 对象")
			}
		}
		if raw := strings.TrimSpace(in.Headers); raw != "" {
			var headers map[string]*string
			if err := json.Unmarshal([]byte(raw), &headers); err != nil || headers == nil {
				return s, errors.New("Webhook 请求头须为 JSON 对象")
			}
			s.Headers = make(map[string]string, len(headers))
			for name, value := range headers {
				name = http.CanonicalHeaderKey(name)
				if !httpguts.ValidHeaderFieldName(name) || value == nil || !httpguts.ValidHeaderFieldValue(*value) {
					return s, errors.New("Webhook 请求头名称或值无效")
				}
				switch name {
				case "Host", "Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Proxy-Connection",
					"Proxy-Authorization", "Upgrade", "Trailer", "Te":
					return s, errors.New("Webhook 请求头包含保留字段")
				}
				if _, exists := s.Headers[name]; exists {
					return s, errors.New("Webhook 请求头名称重复")
				}
				s.Headers[name] = *value
			}
		}
		return s, nil
	default:
		return channelSettings{}, errors.New("告警渠道类型无效")
	}
}

func smtpMailbox(raw string) (string, error) {
	address, err := mail.ParseAddress(raw)
	if err != nil {
		return "", err
	}
	address.Name = ""
	// 用标准库格式保留带引号的本地部分，去掉信封会另行添加的尖括号。
	return strings.TrimSuffix(strings.TrimPrefix(address.String(), "<"), ">"), nil
}

func (m *Module) channelRoutes(r *gin.RouterGroup) {
	r.GET("/channels", m.listChannels)
	r.POST("/channels", m.saveChannel)
	r.PUT("/channels/:id", m.saveChannel)
	r.DELETE("/channels/:id", m.deleteChannel)
	r.POST("/channels/:id/test", m.testChannel)
}

func (m *Module) listChannels(c *gin.Context) {
	var items []channel
	if err := m.db.WithContext(c.Request.Context()).Order("created_at, id").Find(&items).Error; err != nil {
		response.Fail(c, response.System, "读取告警渠道失败")
		return
	}
	list := make([]channelDTO, 0, len(items))
	for _, item := range items {
		settings, err := item.readSettings(m.cfg.AESKey)
		if err != nil {
			response.Fail(c, response.System, err.Error())
			return
		}
		list = append(list, item.dto(settings))
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) saveChannel(c *gin.Context) {
	var in channelDTO
	if !response.Bind(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 80 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		response.Fail(c, response.Invalid, "渠道名称须为 1 至 80 个字符")
		return
	}

	id := c.Param("id")
	var item channel
	var settings channelSettings
	var inputErr error
	err := m.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if id == "" {
			item.ID, item.Kind = store.ID(), in.Kind
		} else {
			if err := tx.First(&item, "id = ?", id).Error; err != nil {
				return err
			}
			if in.Kind != "" && in.Kind != item.Kind {
				inputErr = errors.New("渠道类型不能修改")
				return inputErr
			}
			var err error
			settings, err = item.readSettings(m.cfg.AESKey)
			if err != nil {
				return err
			}
		}

		settings, inputErr = in.settings(item.Kind, settings)
		if inputErr != nil {
			return inputErr
		}
		payload, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		item.Secret, err = store.Encrypt(m.cfg.AESKey, string(payload))
		if err != nil {
			return err
		}
		item.Name, item.Enabled, item.IsDefault = in.Name, in.Enabled, in.IsDefault
		if item.IsDefault {
			if err := tx.Model(&channel{}).Where("kind = ? AND id <> ? AND is_default = ?", item.Kind, item.ID, true).
				Update("is_default", false).Error; err != nil {
				return err
			}
		}
		if id == "" {
			return tx.Create(&item).Error
		}
		return tx.Model(&item).Select("name", "enabled", "is_default", "secret").Updates(item).Error
	})
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "告警渠道不存在")
	case inputErr != nil:
		response.Fail(c, response.Invalid, inputErr.Error())
	case err != nil:
		response.Fail(c, response.System, "保存告警渠道失败")
	default:
		response.OK(c, item.dto(settings))
	}
}

func (m *Module) deleteChannel(c *gin.Context) {
	id := c.Param("id")
	referenced := errors.New("告警渠道仍被规则使用")
	err := m.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&ruleChannel{}).Where("channel_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			return referenced
		}
		result := tx.Delete(&channel{}, "id = ?", id)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
	switch {
	case errors.Is(err, referenced):
		response.Fail(c, response.Conflict, err.Error())
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "告警渠道不存在")
	case err != nil:
		response.Fail(c, response.System, "删除告警渠道失败")
	default:
		response.OK(c, nil)
	}
}

func (m *Module) testChannel(c *gin.Context) {
	var item channel
	if err := m.db.WithContext(c.Request.Context()).First(&item, "id = ?", c.Param("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			response.Fail(c, response.NotFound, "告警渠道不存在")
		} else {
			response.Fail(c, response.System, "读取告警渠道失败")
		}
		return
	}
	const title = "通道测试"
	err := m.send(c.Request.Context(), item, title, "这是一条来自 ezpanel 的告警测试通知。")
	if logErr := m.writeDelivery("", title, item, err); logErr != nil {
		response.Fail(c, response.System, "记录告警测试结果失败")
		return
	}
	if err != nil {
		response.OK(c, gin.H{"success": false, "error": err.Error()})
		return
	}
	response.OK(c, gin.H{"success": true})
}
