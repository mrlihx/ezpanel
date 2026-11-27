package backup

import (
	"context"
	"errors"
	"io"
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type backupChannel struct {
	ID          string            `json:"id" gorm:"primaryKey"`
	Name        string            `json:"name"`
	Protocol    string            `json:"protocol"`
	Endpoint    string            `json:"endpoint"`
	Username    string            `json:"username"`
	Secret      string            `json:"-"`
	Fingerprint string            `json:"-"`
	Port        int               `json:"port,omitempty" gorm:"-"`
	Directory   string            `json:"directory,omitempty" gorm:"-"`
	Enabled     bool              `json:"enabled"`
	Fields      map[string]string `json:"fields,omitempty" gorm:"-"`
	Provider    string            `json:"provider,omitempty" gorm:"-"`
	Unavailable bool              `json:"unavailable,omitempty" gorm:"-"`
}

func (backupChannel) TableName() string { return "backup_channels" }

type channelStorage struct {
	db      *gorm.DB
	key     string
	plugins PluginStorage
}

type channelInput struct {
	Name      string            `json:"name"`
	Protocol  string            `json:"protocol"`
	Endpoint  string            `json:"endpoint"`
	Username  string            `json:"username"`
	Password  string            `json:"password"`
	Port      int               `json:"port"`
	Directory *string           `json:"directory"`
	Enabled   bool              `json:"enabled"`
	Fields    map[string]string `json:"fields"`
}

var (
	errChannelBackups = errors.New("该渠道还有远程备份记录")
	errChannelJobs    = errors.New("该渠道仍被计划任务引用")
	errChannelActive  = errors.New("该渠道正在备份，请在任务结束后修改")
)

func sameChannelServer(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	port := func(endpoint *url.URL) string {
		if endpoint.Port() != "" {
			return endpoint.Port()
		}
		return map[string]string{"sftp": "22", "ftp": "21", "http": "80", "https": "443"}[endpoint.Scheme]
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}

func (s *channelStorage) StorageTargets() []StorageTarget {
	var rows []backupChannel
	_ = s.db.Where("enabled = ?", true).Select("id", "name", "protocol").Order("name").Find(&rows).Error
	providers, _ := s.providers(context.Background())
	available := make(map[string]bool, len(providers))
	for _, provider := range providers {
		available[provider.Protocol] = true
	}
	targets := make([]StorageTarget, 0, len(rows))
	for _, row := range rows {
		if !strings.HasPrefix(row.Protocol, PluginProtocolPrefix) || available[row.Protocol] {
			targets = append(targets, StorageTarget{ID: row.ID, Name: row.Name})
		}
	}
	return targets
}

func (m *Module) listChannels(c *gin.Context) {
	rows := []backupChannel{}
	if err := m.db.WithContext(c.Request.Context()).Order("name").Find(&rows).Error; err != nil {
		response.Fail(c, response.Internal, "读取备份渠道失败")
		return
	}
	providers, err := m.channels.providers(c.Request.Context())
	if err != nil {
		response.Fail(c, response.Internal, "读取插件存储服务失败")
		return
	}
	descriptors := make(map[string]PluginProvider, len(providers))
	for _, provider := range providers {
		descriptors[provider.Protocol] = provider
	}
	for i := range rows {
		if pluginID, ok := strings.CutPrefix(rows[i].Protocol, PluginProtocolPrefix); ok {
			provider := descriptors[rows[i].Protocol]
			rows[i].Provider = provider.Name
			rows[i].Unavailable = rows[i].Provider == ""
			if rows[i].Unavailable {
				rows[i].Provider = pluginID
			}
			if fields, err := m.channels.readPluginFields(rows[i]); err == nil {
				for _, key := range fields.Hidden {
					delete(fields.Values, key)
				}
				for _, field := range provider.Fields {
					if field.Type == "password" {
						delete(fields.Values, field.Key)
					}
				}
				rows[i].Fields = fields.Values
			}
		}
		rows[i] = rows[i].view()
	}
	response.OK(c, rows)
}

// 存储使用完整 URL，接口分别呈现 FTP/SFTP 的服务器、端口与备份目录。
func (row backupChannel) view() backupChannel {
	if row.Protocol == "ftp" || row.Protocol == "sftp" {
		endpoint, err := url.Parse(row.Endpoint)
		if err == nil {
			host := endpoint.Hostname()
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			row.Endpoint = host
			row.Directory = path.Clean("/" + endpoint.Path)
			row.Port, _ = strconv.Atoi(endpoint.Port())
			if row.Port == 0 {
				row.Port = 21
				if row.Protocol == "sftp" {
					row.Port = 22
				}
			}
		}
	}
	return row
}

func (s *channelStorage) draft(in channelInput, old backupChannel) (channelTarget, error) {
	in.Name, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
	address := strings.TrimSpace(in.Endpoint)
	if (in.Protocol == "ftp" || in.Protocol == "sftp") && !strings.Contains(address, "://") {
		if net.ParseIP(address) != nil && strings.Contains(address, ":") {
			address = "[" + address + "]"
		}
		address = in.Protocol + "://" + address
	}
	endpoint, err := url.Parse(address)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || strings.IndexFunc(endpoint.Path, unicode.IsControl) >= 0 {
		return channelTarget{}, errors.New("请填写不带账号、查询参数和片段的服务器目录地址")
	}
	schemes := map[string][]string{"webdav": {"http", "https"}, "ftp": {"ftp"}, "sftp": {"sftp"}}
	validScheme := false
	for _, scheme := range schemes[in.Protocol] {
		validScheme = validScheme || endpoint.Scheme == scheme
	}
	if !validScheme || in.Name == "" || len(in.Name) > 128 || len(in.Endpoint) > 4096 || len(in.Username) > 256 || len(in.Password) > 4096 || strings.IndexFunc(in.Username+in.Password, unicode.IsControl) >= 0 || (in.Protocol != "webdav" && in.Username == "") {
		return channelTarget{}, errors.New("请检查渠道名称、协议地址和账号")
	}
	if in.Protocol == "ftp" || in.Protocol == "sftp" {
		port := in.Port
		if port == 0 && endpoint.Port() != "" {
			port, _ = strconv.Atoi(endpoint.Port())
		}
		if port == 0 {
			port = 21
			if in.Protocol == "sftp" {
				port = 22
			}
		}
		if port < 1 || port > 65535 {
			return channelTarget{}, errors.New("端口须为 1 至 65535")
		}
		endpoint.Host = net.JoinHostPort(endpoint.Hostname(), strconv.Itoa(port))
		if in.Directory != nil {
			directory := strings.TrimSpace(*in.Directory)
			if !path.IsAbs(directory) || len(directory) > 4096 || strings.IndexFunc(directory, unicode.IsControl) >= 0 {
				return channelTarget{}, errors.New("备份目录须为远程服务器上的绝对路径，例如 /backup")
			}
			endpoint.Path = path.Clean(directory)
			endpoint.RawPath = ""
		}
	}
	fingerprint := ""
	previous, _ := url.Parse(old.Endpoint)
	if in.Protocol == "sftp" && in.Password == "" && sameChannelServer(previous, endpoint) {
		fingerprint = old.Fingerprint
	}
	if old.ID != "" && in.Password == "" && in.Username != "" {
		password, err := store.Decrypt(s.key, old.Secret)
		if err != nil {
			return channelTarget{}, errors.New("无法读取已有密码，请重新填写")
		}
		address, _ := url.Parse(old.Endpoint)
		if password != "" && (!sameChannelServer(address, endpoint) || in.Protocol != old.Protocol || in.Username != old.Username) {
			return channelTarget{}, errors.New("更改服务器或账号后，请重新填写密码")
		}
		in.Password = password
	}
	row := backupChannel{ID: old.ID, Name: in.Name, Protocol: in.Protocol, Endpoint: endpoint.String(), Username: in.Username, Fingerprint: fingerprint, Enabled: in.Enabled}
	return channelTarget{backupChannel: row, url: endpoint, password: in.Password}, nil
}

func (m *Module) saveChannel(c *gin.Context) {
	var in channelInput
	if !response.Bind(c, &in) {
		return
	}
	db := m.db.WithContext(c.Request.Context())
	var old backupChannel
	if id := c.Param("id"); id != "" {
		if err := db.First(&old, "id = ?", id).Error; err != nil {
			channelFailure(c, err)
			return
		}
	}
	if strings.HasPrefix(in.Protocol, PluginProtocolPrefix) || strings.HasPrefix(old.Protocol, PluginProtocolPrefix) {
		if old.ID != "" && old.Protocol != in.Protocol {
			response.Fail(c, response.Invalid, "插件渠道类型固定，请新建其他类型渠道")
			return
		}
		m.savePluginChannel(c, in, old)
		return
	}
	target, err := m.channels.draft(in, old)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	row := target.backupChannel
	row.Secret, err = store.Encrypt(m.channels.key, target.password)
	if err != nil {
		response.Fail(c, response.Internal, "无法保存渠道凭据")
		return
	}
	if row.ID == "" {
		row.ID = store.ID()
		err = db.Create(&row).Error
	} else {
		err = db.Transaction(func(tx *gorm.DB) error {
			previousURL, _ := url.Parse(old.Endpoint)
			if old.Protocol != row.Protocol || !sameChannelServer(previousURL, target.url) || path.Clean("/"+previousURL.Path) != target.remotePath("") {
				if err := checkChannelLocation(tx, row.ID); err != nil {
					return err
				}
			}
			result := tx.Model(&row).Select("name", "protocol", "endpoint", "username", "secret", "fingerprint", "enabled").Updates(row)
			if result.Error == nil && result.RowsAffected == 0 {
				return gorm.ErrRecordNotFound
			}
			return result.Error
		})
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed: backup_channels.name") {
			response.Fail(c, response.Conflict, "备份渠道名称已存在")
		} else {
			channelFailure(c, err)
		}
		return
	}
	response.OK(c, row.view())
}

func checkChannelLocation(tx *gorm.DB, id string) error {
	if err := checkBackupChannel(tx, id); err != nil {
		return err
	}
	var count int64
	if err := tx.Table("cron_jobs").Joins("JOIN tasks ON tasks.cron_job_id = cron_jobs.id").Where(channelJobCondition+" AND tasks.status = ?", id, "running").Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errChannelActive
	}
	return nil
}

func checkBackupChannel(tx *gorm.DB, id string) error {
	for _, table := range []string{"site_backups", "db_backups", "mongo_backups"} {
		var count int64
		if err := tx.Table(table).Where("storage_id = ?", id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errChannelBackups
		}
	}
	return nil
}

func channelFailure(c *gin.Context, err error) {
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "备份渠道不存在")
	case errors.Is(err, errChannelBackups), errors.Is(err, errChannelJobs), errors.Is(err, errChannelActive):
		response.Fail(c, response.Conflict, err.Error())
	default:
		response.Fail(c, response.Internal, "备份渠道操作失败")
	}
}

const channelJobCondition = "CASE WHEN json_valid(cron_jobs.config) THEN json_extract(cron_jobs.config, '$.backupStorageId') END = ?"

func (m *Module) deleteChannel(c *gin.Context) {
	id := c.Param("id")
	err := m.db.WithContext(c.Request.Context()).Transaction(func(tx *gorm.DB) error {
		if err := checkBackupChannel(tx, id); err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&store.CronJob{}).Where(channelJobCondition, id).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return errChannelJobs
		}
		result := tx.Delete(&backupChannel{}, "id = ?", id)
		if result.Error == nil && result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return result.Error
	})
	if err != nil {
		channelFailure(c, err)
		return
	}
	response.OK(c, nil)
}

func (m *Module) testChannel(c *gin.Context) {
	var in struct {
		ID     string        `json:"id"`
		Config *channelInput `json:"config"`
	}
	if !response.Bind(c, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	var old backupChannel
	if in.ID != "" {
		if err := m.db.WithContext(ctx).First(&old, "id = ?", in.ID).Error; err != nil {
			channelFailure(c, err)
			return
		}
	}
	if in.Config == nil && old.ID == "" {
		response.Fail(c, response.Invalid, "请选择或填写备份渠道")
		return
	}
	if strings.HasPrefix(old.Protocol, PluginProtocolPrefix) || in.Config != nil && strings.HasPrefix(in.Config.Protocol, PluginProtocolPrefix) {
		if in.Config != nil && old.ID != "" && old.Protocol != in.Config.Protocol {
			response.Fail(c, response.Invalid, "请使用原有渠道类型测试连接")
			return
		}
		row := old
		var err error
		if in.Config != nil {
			row, err = m.channels.draftPluginChannel(ctx, *in.Config, old)
		}
		if err == nil {
			var body io.ReadCloser
			body, err = m.channels.callPlugin(ctx, row, "test", "", nil)
			if err == nil {
				_ = body.Close()
			}
		}
		if err != nil {
			response.Fail(c, response.Invalid, err.Error())
			return
		}
		response.OK(c, nil)
		return
	}
	var target channelTarget
	var err error
	if in.Config != nil {
		target, err = m.channels.draft(*in.Config, old)
	} else {
		target, err = m.channels.openTarget(old)
	}
	if err == nil {
		err = target.test(ctx)
	}
	if err != nil {
		if ctx.Err() != nil {
			err = errors.New("连接测试超时，请检查服务器地址和网络")
		}
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, nil)
}

func (m *Module) toggleChannel(c *gin.Context) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Enabled == nil {
		response.Fail(c, response.Invalid, "请选择渠道状态")
		return
	}
	result := m.db.WithContext(c.Request.Context()).Model(&backupChannel{}).Where("id = ?", c.Param("id")).Update("enabled", *in.Enabled)
	if result.Error != nil {
		response.Fail(c, response.Internal, "无法更新渠道状态")
		return
	}
	if result.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "备份渠道不存在")
		return
	}
	response.OK(c, nil)
}
