package plugin

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type vaultCredential struct {
	ID        string    `json:"id" gorm:"primaryKey"`
	Name      string    `json:"name"`
	Secret    string    `json:"-"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (vaultCredential) TableName() string { return "plugin_credentials" }

type credentialBinding struct {
	PluginID     string `gorm:"primaryKey"`
	Slot         string `gorm:"primaryKey"`
	CredentialID string
}

func (credentialBinding) TableName() string { return "plugin_credential_bindings" }

func (m *Module) listCredentials(c *gin.Context) {
	rows := []vaultCredential{}
	if err := m.db.WithContext(c.Request.Context()).Select("id, name, updated_at").Order("name, id").Find(&rows).Error; err != nil {
		response.Fail(c, response.System, "无法读取凭据列表")
		return
	}
	response.OK(c, rows)
}

func (m *Module) saveCredential(c *gin.Context) {
	var in struct{ Name, Value string }
	if !response.Bind(c, &in) {
		return
	}
	name := strings.TrimSpace(in.Name)
	id := c.Param("credential")
	if name == "" || len([]rune(name)) > 64 || strings.IndexFunc(name, unicode.IsControl) >= 0 || len(in.Value) > 8192 || strings.ContainsRune(in.Value, 0) || (id == "" && in.Value == "") {
		response.Fail(c, response.Invalid, "凭据名称或内容无效")
		return
	}
	row := vaultCredential{ID: id, Name: name}
	if in.Value != "" {
		secret, err := store.Encrypt(m.cfg.AESKey, in.Value)
		if err != nil {
			response.Fail(c, response.System, "无法加密凭据")
			return
		}
		row.Secret = secret
	}
	db := m.db.WithContext(c.Request.Context())
	var err error
	if id == "" {
		row.ID = store.ID()
		err = db.Create(&row).Error
	} else {
		result := db.Model(&row).Updates(row)
		err = result.Error
		if err == nil && result.RowsAffected == 0 {
			response.Fail(c, response.NotFound, "凭据不存在")
			return
		}
	}
	if err != nil {
		response.Fail(c, response.System, "无法保存凭据")
		return
	}
	c.Set("panel.auditName", name)
	response.OK(c, row)
}

func (m *Module) deleteCredential(c *gin.Context) {
	result := m.db.WithContext(c.Request.Context()).Delete(&vaultCredential{}, "id = ?", c.Param("credential"))
	if result.Error != nil {
		if strings.Contains(result.Error.Error(), "FOREIGN KEY constraint") {
			response.Fail(c, response.Conflict, "凭据仍被插件绑定，请先解除绑定")
		} else {
			response.Fail(c, response.System, "删除凭据失败")
		}
		return
	}
	if result.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "凭据不存在")
		return
	}
	response.OK(c, nil)
}

func (m *Module) saveGrants(id string, manifest Manifest, grant capabilityGrant) error {
	bindings := []credentialBinding{}
	for slot, credentialID := range grant.Credentials {
		if credentialID == "" {
			continue
		}
		if !slices.ContainsFunc(manifest.CredentialSlots, func(item CredentialSlot) bool { return item.ID == slot }) {
			return errors.New("凭据绑定包含未知位置")
		}
		bindings = append(bindings, credentialBinding{PluginID: id, Slot: slot, CredentialID: credentialID})
	}
	grant.Credentials = nil
	data, _ := json.Marshal(grant)
	return m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&store.Setting{Key: grantPrefix + id, Value: string(data)}).Error; err != nil {
			return errors.New("保存插件授权失败")
		}
		if err := tx.Delete(&credentialBinding{}, "plugin_id = ?", id).Error; err != nil {
			return errors.New("无法更新插件凭据绑定")
		}
		if len(bindings) > 0 && tx.Create(&bindings).Error != nil {
			return errors.New("无法绑定凭据，请刷新并重新选择")
		}
		return nil
	})
}

func removeRetiredCredentialSlots(tx *gorm.DB, id string, manifest Manifest) error {
	query := tx.Where("plugin_id = ?", id)
	if len(manifest.CredentialSlots) > 0 {
		slots := make([]string, 0, len(manifest.CredentialSlots))
		for _, slot := range manifest.CredentialSlots {
			slots = append(slots, slot.ID)
		}
		query = query.Where("slot NOT IN ?", slots)
	}
	return query.Delete(&credentialBinding{}).Error
}

func (m *Module) hostCredential(c *gin.Context) {
	i := identity(c)
	slot := c.Param("slot")
	defer func() { m.hostAudit(c, "credentials.read", slot, c.Writer.Status() < 400) }()
	var row vaultCredential
	if !slices.ContainsFunc(i.manifest.CredentialSlots, func(item CredentialSlot) bool { return item.ID == slot }) ||
		m.db.WithContext(c.Request.Context()).Model(&row).
			Joins("JOIN plugin_credential_bindings AS b ON b.credential_id = plugin_credentials.id").
			Where("b.plugin_id = ? AND b.slot = ?", i.id, slot).Take(&row).Error != nil {
		response.Fail(c, response.Forbidden, "此插件凭据尚未授权")
		return
	}
	value, err := store.Decrypt(m.cfg.AESKey, row.Secret)
	if err != nil {
		response.Fail(c, response.System, "无法读取此插件凭据")
		return
	}
	i.run.log.addSecret(value)
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"value": value})
}
