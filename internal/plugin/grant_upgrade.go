package plugin

import (
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sort"
	"time"

	"ezpanel/internal/store"
	"gorm.io/gorm"
)

func grantChanges(grant capabilityGrant, before, after Manifest) (capabilityGrant, []string) {
	changes := []string{}
	grant.AllResources = slices.Clone(grant.AllResources)
	for _, scope := range resourceScopes {
		ids := grant.resourceIDs(scope.Kind)
		if len(*ids) == 0 && !slices.Contains(grant.AllResources, scope.Kind) {
			continue
		}
		changed := (before.ResourceActions[scope.Kind] != "") != (after.ResourceActions[scope.Kind] != "")
		for _, permission := range scope.Permissions {
			changed = changed || slices.Contains(before.Permissions, permission) != slices.Contains(after.Permissions, permission)
		}
		if changed {
			*ids = nil
			grant.AllResources = slices.DeleteFunc(grant.AllResources, func(kind string) bool { return kind == scope.Kind })
			changes = append(changes, scope.Label+"授权")
		}
	}
	if grant.ChannelID != "" && !slices.Contains(after.Permissions, "notifications.send") {
		grant.ChannelID = ""
		changes = append(changes, "通知渠道授权")
	}
	grant.Events = slices.DeleteFunc(slices.Clone(grant.Events), func(name string) bool {
		keep := slices.Contains(after.Events, name)
		if !keep {
			changes = append(changes, "事件订阅 "+name)
		}
		return !keep
	})
	plans := make(map[string]string, len(grant.Schedules))
	for id, expression := range grant.Schedules {
		oldIndex := slices.IndexFunc(before.Jobs, func(job Job) bool { return job.ID == id })
		newIndex := slices.IndexFunc(after.Jobs, func(job Job) bool { return job.ID == id })
		if oldIndex >= 0 && newIndex >= 0 && reflect.DeepEqual(before.Jobs[oldIndex].Params, after.Jobs[newIndex].Params) && slices.Equal(before.Jobs[oldIndex].Steps, after.Jobs[newIndex].Steps) {
			plans[id] = expression
		} else {
			changes = append(changes, "计划任务 "+id)
		}
	}
	grant.Schedules = plans
	bindings := make(map[string]string, len(grant.Credentials))
	for slot, id := range grant.Credentials {
		if slices.ContainsFunc(after.CredentialSlots, func(item CredentialSlot) bool { return item.ID == slot }) {
			bindings[slot] = id
		} else {
			changes = append(changes, "凭据绑定 "+slot)
		}
	}
	grant.Credentials = bindings
	disabledSet := map[string]bool{}
	for _, code := range grant.DisabledPermissions {
		if slices.Contains(after.Permissions, code) {
			disabledSet[code] = true
		}
	}
	for _, code := range after.Permissions {
		if !slices.Contains(before.Permissions, code) {
			if spec, ok := pluginPermissions[code]; ok && spec.Risk == highRisk {
				disabledSet[code] = true
			}
		}
	}
	disabled := make([]string, 0, len(disabledSet))
	for code := range disabledSet {
		disabled = append(disabled, code)
	}
	sort.Strings(disabled)
	grant.DisabledPermissions = disabled
	slices.Sort(changes)
	return grant, changes
}

func loadTransitionGrant(db *gorm.DB, id string) (capabilityGrant, error) {
	var grant capabilityGrant
	var row store.Setting
	err := db.Take(&row, "key = ?", grantPrefix+id).Error
	if err == nil {
		err = json.Unmarshal([]byte(row.Value), &grant)
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return grant, err
	}
	var rows []credentialBinding
	if err := db.Where("plugin_id = ?", id).Find(&rows).Error; err != nil {
		return grant, err
	}
	grant.Credentials = make(map[string]string, len(rows))
	for _, row := range rows {
		grant.Credentials[row.Slot] = row.CredentialID
	}
	return grant, nil
}

func (m *Module) reconcileGrants(tx *gorm.DB, id string, before, after Manifest) error {
	grant, err := loadTransitionGrant(tx, id)
	if err != nil {
		return err
	}
	next, changes := grantChanges(grant, before, after)
	disabledChanged := !slices.Equal(grant.DisabledPermissions, next.DisabledPermissions)
	if len(changes) == 0 && !disabledChanged {
		return nil
	}
	next.Credentials = nil
	data, _ := json.Marshal(next)
	if err := tx.Save(&store.Setting{Key: grantPrefix + id, Value: string(data)}).Error; err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	if err := removeRetiredCredentialSlots(tx, id, after); err != nil {
		return err
	}
	if slices.Equal(grant.Events, next.Events) && slices.Equal(grant.AllResources, next.AllResources) &&
		slices.Equal(grant.SiteIDs, next.SiteIDs) && slices.Equal(grant.DatabaseIDs, next.DatabaseIDs) &&
		slices.Equal(grant.CertificateIDs, next.CertificateIDs) && slices.Equal(grant.ComposeNames, next.ComposeNames) {
		return nil
	}
	var pending []eventDelivery
	if err := tx.Select("id, payload").Where("plugin_id = ? AND status = ?", id, "pending").Find(&pending).Error; err != nil {
		return err
	}
	var revoked []string
	for _, row := range pending {
		var event Event
		if json.Unmarshal([]byte(row.Payload), &event) != nil || !acceptsEvent(next, event) {
			revoked = append(revoked, row.ID)
		}
	}
	for ids := range slices.Chunk(revoked, 500) {
		if err := tx.Model(&eventDelivery{}).Where("id IN ?", ids).Updates(map[string]any{"status": "skipped", "last_error": "插件授权已变化", "updated_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) grantPreview(id string, before, after Manifest) ([]string, error) {
	grant, err := loadTransitionGrant(m.db, id)
	if err != nil {
		return nil, err
	}
	_, changes := grantChanges(grant, before, after)
	return changes, nil
}
