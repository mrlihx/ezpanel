package database

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"ezpanel/internal/store"
)

type PluginResource struct {
	ID, Name, Kind, Status, Version string
}

// 与数据库页面共用实例、连接和 SQLite 文件的面板标识。
func (m *Module) PluginResources(ctx context.Context) ([]PluginResource, error) {
	db := m.db.WithContext(ctx)
	var servers []store.DBServer
	if err := db.Select("id, name, type, is_local").Find(&servers).Error; err != nil {
		return nil, err
	}
	rows := make([]PluginResource, 0, len(servers))
	for _, server := range servers {
		status := "remote"
		if server.IsLocal {
			status = "local"
		}
		rows = append(rows, PluginResource{ID: server.ID, Name: server.Name, Kind: server.Type, Status: status})
	}
	var connections []store.Setting
	if err := db.Where("key LIKE ? OR key LIKE ?", "database.mongodb.%", "database.redis.%").Find(&connections).Error; err != nil {
		return rows, err
	}
	for _, row := range connections {
		var connection noSQLConnection
		if json.Unmarshal([]byte(row.Value), &connection) != nil || connection.ID == "" {
			continue
		}
		engine, _, _ := strings.Cut(strings.TrimPrefix(row.Key, "database."), ".")
		id := connection.ID
		if engine == "redis" && id == "local" {
			// Redis 和 MongoDB 默认连接都叫 local，资源授权需要单独识别。
			id = "redis:local"
		}
		status := "remote"
		if connection.IsLocal {
			status = "local"
		}
		rows = append(rows, PluginResource{ID: id, Name: connection.Name, Kind: engine, Status: status})
	}
	files, err := m.readSQLiteEntries()
	if err != nil {
		return rows, err
	}
	for _, file := range files {
		if !file.Removed {
			rows = append(rows, PluginResource{ID: file.ID, Name: file.Name, Kind: "sqlite"})
		}
	}
	instances, err := m.packages.DatabaseInstanceSummaries(ctx, "postgresql")
	if err != nil {
		// Docker 不可用时仍提供远程连接和 SQLite 文件。
		slog.Warn("插件数据库清单暂时无法读取 PostgreSQL 实例", "error", err)
		return rows, nil
	}
	for _, instance := range instances {
		rows = append(rows, PluginResource{ID: instance.ID, Name: instance.Name, Kind: "postgresql", Status: instance.Status, Version: instance.Version})
	}
	return rows, nil
}
