package backup

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type backupChannel struct {
	ID          string `json:"id" gorm:"primaryKey"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Endpoint    string `json:"endpoint"`
	Username    string `json:"username"`
	Secret      string `json:"-"`
	Fingerprint string `json:"fingerprint"`
	Enabled     bool   `json:"enabled"`
}

func (backupChannel) TableName() string { return "backup_channels" }

type channelStorage struct {
	db  *gorm.DB
	key string
}

type channelInput struct {
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Endpoint    string `json:"endpoint"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Fingerprint string `json:"fingerprint"`
	Enabled     bool   `json:"enabled"`
}

var (
	errChannelBackups = errors.New("该渠道还有远程备份记录")
	errChannelJobs    = errors.New("该渠道仍被计划任务引用")
	errChannelActive  = errors.New("该渠道正在备份，请在任务结束后修改")
)

func sameChannelServer(a, b *url.URL) bool {
	return a != nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

func (s *channelStorage) StorageTargets() []StorageTarget {
	rows := []StorageTarget{}
	_ = s.db.Model(&backupChannel{}).Where("enabled = ?", true).Select("id", "name").Order("name").Find(&rows).Error
	return rows
}

func (m *Module) listChannels(c *gin.Context) {
	rows := []backupChannel{}
	if err := m.db.WithContext(c.Request.Context()).Order("name").Find(&rows).Error; err != nil {
		response.Fail(c, response.Internal, "读取备份渠道失败")
		return
	}
	response.OK(c, rows)
}

func (s *channelStorage) draft(in channelInput, old backupChannel) (channelTarget, error) {
	in.Name, in.Username = strings.TrimSpace(in.Name), strings.TrimSpace(in.Username)
	endpoint, err := url.Parse(strings.TrimSpace(in.Endpoint))
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
	if in.Protocol == "sftp" {
		fingerprint, ok := strings.CutPrefix(strings.TrimSpace(in.Fingerprint), "SHA256:")
		hash, err := base64.RawStdEncoding.DecodeString(fingerprint)
		if !ok || err != nil || len(hash) != 32 {
			return channelTarget{}, errors.New("请输入 SFTP 主机的 SHA256 公钥指纹")
		}
		in.Fingerprint = "SHA256:" + fingerprint
	} else {
		in.Fingerprint = ""
	}
	if old.ID != "" && in.Password == "" && in.Username != "" {
		password, err := store.Decrypt(s.key, old.Secret)
		if err != nil {
			return channelTarget{}, errors.New("无法读取已有密码，请重新填写")
		}
		address, _ := url.Parse(old.Endpoint)
		if password != "" && (!sameChannelServer(address, endpoint) || in.Protocol != old.Protocol || in.Username != old.Username || in.Fingerprint != old.Fingerprint) {
			return channelTarget{}, errors.New("更改服务器、账号或指纹后，请重新填写密码")
		}
		in.Password = password
	}
	row := backupChannel{ID: old.ID, Name: in.Name, Protocol: in.Protocol, Endpoint: endpoint.String(), Username: in.Username, Fingerprint: in.Fingerprint, Enabled: in.Enabled}
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
				if err := checkBackupChannel(tx, row.ID); err != nil {
					return err
				}
				var count int64
				if err := tx.Table("cron_jobs").Joins("JOIN tasks ON tasks.cron_job_id = cron_jobs.id").Where(channelJobCondition+" AND tasks.status = ?", row.ID, "running").Count(&count).Error; err != nil {
					return err
				}
				if count > 0 {
					return errChannelActive
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
	response.OK(c, row)
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
