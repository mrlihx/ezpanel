package backup

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const PluginProtocolPrefix = "plugin:"

type ChannelField struct {
	Key      string   `json:"key" yaml:"key"`
	Label    string   `json:"label" yaml:"label"`
	Type     string   `json:"type" yaml:"type"`
	Required bool     `json:"required,omitempty" yaml:"required,omitempty"`
	Options  []string `json:"options,omitempty" yaml:"options,omitempty"`
}

type PluginProvider struct {
	Protocol string         `json:"protocol" yaml:"-"`
	Name     string         `json:"name" yaml:"name"`
	Fields   []ChannelField `json:"fields" yaml:"fields"`
}

type PluginChannelRequest struct {
	ChannelID string            `json:"channelId,omitempty"`
	Key       string            `json:"key,omitempty"`
	Values    map[string]string `json:"values"`
}

type PluginStorage interface {
	BackupProviders(context.Context) ([]PluginProvider, error)
	CallBackupProvider(context.Context, string, string, PluginChannelRequest, io.Reader) (io.ReadCloser, error)
}

type pluginChannelFields struct {
	Values map[string]string `json:"values"`
	Hidden []string          `json:"hidden"`
}

func (m *Module) SetPluginStorage(provider PluginStorage) { m.channels.plugins = provider }

func (m *Module) listProviders(c *gin.Context) {
	rows, err := m.channels.providers(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "无法读取插件存储服务")
		return
	}
	response.OK(c, rows)
}

func (s *channelStorage) providers(ctx context.Context) ([]PluginProvider, error) {
	if s.plugins == nil {
		return []PluginProvider{}, nil
	}
	return s.plugins.BackupProviders(ctx)
}

func (s *channelStorage) readPluginFields(channel backupChannel) (pluginChannelFields, error) {
	var fields pluginChannelFields
	data, err := store.Decrypt(s.key, channel.Secret)
	if err == nil {
		err = json.Unmarshal([]byte(data), &fields)
	}
	if err != nil {
		return fields, errors.New("无法读取插件渠道配置")
	}
	return fields, nil
}

func (s *channelStorage) draftPluginChannel(ctx context.Context, in channelInput, old backupChannel) (backupChannel, error) {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 64 || strings.IndexFunc(in.Name, unicode.IsControl) >= 0 {
		return backupChannel{}, errors.New("请输入有效的渠道名称")
	}
	providers, err := s.providers(ctx)
	if err != nil {
		return backupChannel{}, errors.New("无法读取插件存储服务")
	}
	index := slices.IndexFunc(providers, func(p PluginProvider) bool { return p.Protocol == in.Protocol })
	if index < 0 {
		return backupChannel{}, errors.New("存储插件未运行，请先启用插件")
	}
	var previous pluginChannelFields
	if old.ID != "" {
		previous, err = s.readPluginFields(old)
		if err != nil {
			return backupChannel{}, err
		}
	}
	fields := pluginChannelFields{Values: map[string]string{}}
	for _, schema := range providers[index].Fields {
		value := in.Fields[schema.Key]
		if schema.Type == "password" || slices.Contains(previous.Hidden, schema.Key) {
			fields.Hidden = append(fields.Hidden, schema.Key)
			if value == "" {
				value = previous.Values[schema.Key]
			}
		}
		if len(value) > 4096 || strings.ContainsRune(value, 0) || schema.Required && strings.TrimSpace(value) == "" {
			return backupChannel{}, errors.New("请检查字段：" + schema.Label)
		}
		if value != "" {
			switch schema.Type {
			case "number":
				if _, err := strconv.ParseInt(value, 10, 64); err != nil {
					return backupChannel{}, errors.New("请输入整数：" + schema.Label)
				}
			case "select":
				if !slices.Contains(schema.Options, value) {
					return backupChannel{}, errors.New("请选择有效的选项：" + schema.Label)
				}
			}
		}
		fields.Values[schema.Key] = value
	}
	data, _ := json.Marshal(fields)
	if len(data) > 8192 {
		return backupChannel{}, errors.New("插件渠道配置过大")
	}
	secret, err := store.Encrypt(s.key, string(data))
	return backupChannel{ID: old.ID, Name: in.Name, Protocol: in.Protocol, Secret: secret, Enabled: in.Enabled}, err
}

func (m *Module) savePluginChannel(c *gin.Context, in channelInput, old backupChannel) {
	ctx := c.Request.Context()
	row, err := m.channels.draftPluginChannel(ctx, in, old)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	db := m.db.WithContext(ctx)
	if row.ID == "" {
		row.ID = store.ID()
		err = db.Create(&row).Error
	} else {
		fields, _ := m.channels.readPluginFields(row)
		previous, _ := m.channels.readPluginFields(old)
		err = db.Transaction(func(tx *gorm.DB) error {
			// 非秘密字段可能指向新的存储空间，保留已登记备份的原始位置。
			for key, value := range fields.Values {
				if !slices.Contains(fields.Hidden, key) && value != previous.Values[key] {
					if err := checkChannelLocation(tx, row.ID); err != nil {
						return err
					}
					break
				}
			}
			result := tx.Model(&row).Select("name", "secret", "enabled").Updates(row)
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

func (s *channelStorage) callPlugin(ctx context.Context, row backupChannel, action, key string, body io.Reader) (io.ReadCloser, error) {
	if s.plugins == nil {
		return nil, errors.New("存储插件不可用")
	}
	fields, err := s.readPluginFields(row)
	if err != nil {
		return nil, err
	}
	return s.plugins.CallBackupProvider(ctx, strings.TrimPrefix(row.Protocol, PluginProtocolPrefix), action, PluginChannelRequest{
		ChannelID: row.ID, Key: key, Values: fields.Values,
	}, body)
}

func (m *Module) PluginStorageInUse(pluginID string) error {
	var count int64
	if err := m.db.Model(&backupChannel{}).Where("protocol = ?", PluginProtocolPrefix+pluginID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return errors.New("该插件仍有备份渠道，请先迁移备份并删除渠道")
	}
	return nil
}
