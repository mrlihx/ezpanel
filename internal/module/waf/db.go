package waf

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const wafDBSchema = `
CREATE TABLE IF NOT EXISTS waf_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	occurred_at INTEGER NOT NULL,
	site_id TEXT NOT NULL,
	domain TEXT NOT NULL,
	client_ip TEXT NOT NULL,
	location TEXT NOT NULL DEFAULT '',
	method TEXT NOT NULL,
	uri TEXT NOT NULL,
	query_string TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT '',
	rule_group TEXT NOT NULL,
	rule_id TEXT NOT NULL,
	rule_name TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	matched_data TEXT NOT NULL DEFAULT '',
	status_code INTEGER NOT NULL DEFAULT 403
);
CREATE INDEX IF NOT EXISTS idx_waf_events_time ON waf_events(occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_waf_events_site_time ON waf_events(site_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_waf_events_ip ON waf_events(client_ip, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_waf_events_group ON waf_events(rule_group, occurred_at DESC);

CREATE TABLE IF NOT EXISTS waf_bans (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	ip TEXT NOT NULL,
	site_id TEXT NOT NULL DEFAULT '',
	location TEXT NOT NULL DEFAULT '',
	reason TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT 'manual',
	nft_synced BOOLEAN NOT NULL DEFAULT 0,
	expires_at INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_waf_bans_ip_site ON waf_bans(ip, site_id);
CREATE INDEX IF NOT EXISTS idx_waf_bans_expires ON waf_bans(expires_at);

CREATE TABLE IF NOT EXISTS waf_hourly_stats (
	hour_ts INTEGER NOT NULL,
	site_id TEXT NOT NULL,
	rule_group TEXT NOT NULL,
	blocked_count INTEGER NOT NULL DEFAULT 0,
	observed_count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (hour_ts, site_id, rule_group)
);

CREATE TABLE IF NOT EXISTS waf_log_offsets (
	file_path TEXT PRIMARY KEY,
	inode INTEGER NOT NULL,
	offset INTEGER NOT NULL
);
`

// openWAFDB 打开独立的 waf.db（WAL 模式），避免高频攻击日志阻塞 panel.db。
func openWAFDB(path string) (*gorm.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return nil, err
		}
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger:  logger.Default.LogMode(logger.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(1)
	if path != ":memory:" {
		if err = os.Chmod(path, 0600); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}
	if err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000;").Error; err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("初始化 waf.db PRAGMA 失败: %w", err)
	}
	if err = db.Exec(wafDBSchema).Error; err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("初始化 waf.db 表结构失败: %w", err)
	}
	return db, nil
}
