package database

import (
	"context"
	"encoding/json"
	"fmt"

	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
	"gorm.io/gorm/clause"
)

func (m *Module) localInstances(ctx context.Context, engine string) map[string]pkgmgr.DatabaseInstance {
	rows, _ := m.packages.DatabaseInstances(ctx, engine)
	out := make(map[string]pkgmgr.DatabaseInstance, len(rows))
	for _, row := range rows {
		if row.ID != "default" && row.Port > 0 {
			out[row.ID] = row
		}
	}
	return out
}

func (m *Module) instanceCredential(engine, id string) (string, error) {
	password, err := m.packages.DatabaseInstancePassword(engine, id)
	if err != nil {
		return "", fmt.Errorf("无法读取 %s 实例的连接凭据: %w", pkgmgr.Name(engine), err)
	}
	return store.Encrypt(m.cfg.AESKey, password)
}

func (m *Module) mysqlServers(ctx context.Context) ([]store.DBServer, map[string]pkgmgr.DatabaseInstance, error) {
	installed, err := m.ensureLocal(ctx)
	if err != nil {
		return nil, nil, err
	}
	instances := m.localInstances(ctx, "mysql")
	db := m.db.WithContext(ctx)
	var rows []store.DBServer
	if err := db.Order("created_at,id").Find(&rows).Error; err != nil {
		return nil, nil, err
	}
	out := make([]store.DBServer, 0, len(rows)+len(instances))
	known := map[string]bool{}
	for _, row := range rows {
		if row.InstanceID != "" {
			instance, ok := instances[row.InstanceID]
			if !ok {
				continue
			}
			known[row.InstanceID] = true
			if row.Name != instance.Name || row.Port != instance.Port {
				row.Name, row.Port = instance.Name, instance.Port
				if err := db.Model(&row).Select("name", "port").Updates(&row).Error; err != nil {
					return nil, nil, err
				}
			}
		} else if row.IsLocal && !installed {
			continue
		}
		out = append(out, row)
	}
	for id, instance := range instances {
		if known[id] {
			continue
		}
		secret, err := m.instanceCredential("mysql", id)
		if err != nil {
			return nil, nil, err
		}
		row := store.DBServer{ID: "mysql-" + id, InstanceID: id, Name: instance.Name, IsLocal: true, Type: "mysql", Network: "tcp", Host: "127.0.0.1", Port: instance.Port, Username: "root", PasswordEnc: secret, TLSMode: "disabled"}
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return nil, nil, err
		}
		if err := db.First(&row, "id = ?", row.ID).Error; err != nil {
			return nil, nil, err
		}
		out = append(out, row)
	}
	return out, instances, nil
}

func (m *Module) syncNoSQLInstances(ctx context.Context, engine string, instances map[string]pkgmgr.DatabaseInstance) error {
	db := m.db.WithContext(ctx)
	var rows []store.Setting
	prefix := "database." + engine + "."
	if err := db.Where("key LIKE ?", prefix+"%").Find(&rows).Error; err != nil {
		return err
	}
	known := map[string]bool{}
	for _, row := range rows {
		var saved noSQLConnection
		if err := json.Unmarshal([]byte(row.Value), &saved); err != nil {
			return err
		}
		instance, ok := instances[saved.InstanceID]
		if !ok {
			continue
		}
		known[saved.InstanceID] = true
		if saved.Name == instance.Name && saved.Port == instance.Port {
			continue
		}
		saved.Name, saved.Port = instance.Name, instance.Port
		raw, _ := json.Marshal(saved)
		if err := db.Model(&row).Update("value", string(raw)).Error; err != nil {
			return err
		}
	}
	for id, instance := range instances {
		if known[id] {
			continue
		}
		secret, err := m.instanceCredential(engine, id)
		if err != nil {
			return err
		}
		v := noSQLConnection{ID: engine + "-" + id, InstanceID: id, IsLocal: true, Name: instance.Name, Host: "127.0.0.1", Port: instance.Port, PasswordEnc: secret, AuthSource: "admin"}
		if engine == "mongodb" {
			v.Username = "panelAdmin"
		}
		raw, _ := json.Marshal(v)
		if err := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&store.Setting{Key: prefix + v.ID, Value: string(raw)}).Error; err != nil {
			return err
		}
	}
	return nil
}
