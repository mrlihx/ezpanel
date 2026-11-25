package waf

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"ezpanel/internal/geoip"
	"golang.org/x/sys/unix"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type rawEvent struct {
	TS        int64  `json:"ts"`
	SiteID    string `json:"siteId"`
	Domain    string `json:"domain"`
	IP        string `json:"ip"`
	Method    string `json:"method"`
	URI       string `json:"uri"`
	Query     string `json:"query"`
	UA        string `json:"ua"`
	RuleGroup string `json:"ruleGroup"`
	RuleID    string `json:"ruleId"`
	RuleName  string `json:"ruleName"`
	Action    string `json:"action"`
	Matched   string `json:"matched"`
	Status    int    `json:"status"`
}

// Run 启动后台增量日志采集与过期封禁/日志清理协程。
func (m *Module) Run(ctx context.Context) {
	m.restoreFirewallBans(ctx)
	// 启动时尝试同步一次引擎配置（开发模式下若无 /www 权限则静默跳过）
	if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
		slog.Warn("WAF 启动同步配置失败", "error", err)
	}

	tailTicker := time.NewTicker(3 * time.Second)
	cleanTicker := time.NewTicker(10 * time.Minute)
	rotateTicker := time.NewTicker(time.Minute)
	defer tailTicker.Stop()
	defer cleanTicker.Stop()
	defer rotateTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tailTicker.C:
			if err := m.collectOnce(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("WAF 日志采集失败", "error", err)
			}
		case <-rotateTicker.C:
			if err := m.rotateEventLog(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("轮转 WAF 日志失败", "error", err)
			}
		case <-cleanTicker.C:
			m.cleanup(ctx)
		}
	}
}

func (m *Module) rotateEventLog(ctx context.Context) error {
	eventLog := m.wafEventLog()
	marker := eventLog + ".reload"
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		info, err := os.Stat(eventLog)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Size() < 64<<20 {
			return nil
		}
		if err := os.WriteFile(marker, nil, 0600); err != nil {
			return err
		}
		rotated := eventLog + "." + time.Now().UTC().Format("20060102T150405.000000000")
		if err := os.Rename(eventLog, rotated); err != nil {
			return errors.Join(err, os.Remove(marker))
		}
	} else if err != nil {
		return err
	}

	// 工作进程继承旧日志句柄。平滑重载让新进程打开新文件，采集端继续读取旧文件。
	if err := m.SyncEngine(ctx, true); err != nil {
		return err
	}
	return os.Remove(marker)
}

func (m *Module) collectOnce(ctx context.Context) error {
	eventLog := m.wafEventLog()
	rotated, err := filepath.Glob(eventLog + ".[0-9]*")
	if err != nil {
		return err
	}
	for _, path := range rotated {
		if err := m.collectFile(ctx, path); err != nil {
			return err
		}
	}
	return m.collectFile(ctx, eventLog)
}

func (m *Module) collectFile(ctx context.Context, path string) error {
	eventLog := m.wafEventLog()
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	inode := info.Sys().(*syscall.Stat_t).Ino

	var state logOffset
	db := m.wafDB.WithContext(ctx)
	err = db.First(&state, "file_path = ?", path).Error
	if errors.Is(err, gorm.ErrRecordNotFound) && path != eventLog {
		// 刚轮转时，旧文件最后一次确认的偏移仍记在原路径上。
		err = db.First(&state, "file_path = ? AND inode = ?", eventLog, inode).Error
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	offset := state.Offset
	if state.Inode != inode || info.Size() < offset {
		offset = 0
	}
	if info.Size() == offset && (path == eventLog || state.FilePath == path) {
		if path != eventLog && time.Since(info.ModTime()) > 24*time.Hour {
			if _, err := os.Stat(eventLog + ".reload"); !errors.Is(err, os.ErrNotExist) {
				return err
			}
			if err := os.Remove(path); err != nil {
				return err
			}
			return db.Delete(&logOffset{FilePath: path}).Error
		}
		return nil
	}

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return err
	}

	reader := bufio.NewReaderSize(file, 64<<10)
	var batch []Event
	newOffset := offset
	const (
		maxBatch     = 500
		maxLinesTick = 2000
		maxLineBytes = 64 << 10
	)
	for lines := 0; len(batch) < maxBatch && lines < maxLinesTick; lines++ {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			// 仅处理完整行，避免半行截断丢失
			if line[len(line)-1] != '\n' && readErr == io.EOF {
				break
			}
			newOffset += int64(len(line))
			if len(line) <= maxLineBytes {
				var raw rawEvent
				if json.Unmarshal(line, &raw) == nil && raw.IP != "" {
					ts := raw.TS
					if ts <= 0 {
						ts = time.Now().Unix()
					}
					batch = append(batch, Event{
						OccurredAt:  ts,
						SiteID:      raw.SiteID,
						Domain:      raw.Domain,
						ClientIP:    raw.IP,
						Location:    geoip.Location(raw.IP),
						Method:      raw.Method,
						URI:         raw.URI,
						QueryString: raw.Query,
						UserAgent:   raw.UA,
						RuleGroup:   raw.RuleGroup,
						RuleID:      raw.RuleID,
						RuleName:    raw.RuleName,
						Action:      raw.Action,
						MatchedData: raw.Matched,
						StatusCode:  raw.Status,
					})
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	if len(batch) == 0 && newOffset == offset && (path == eventLog || state.FilePath == path) {
		return nil
	}

	blockedIPs := map[string]struct{}{}
	err = m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(batch) > 0 {
			if err := tx.CreateInBatches(batch, 100).Error; err != nil {
				return err
			}
			type statKey struct {
				hour  int64
				site  string
				group string
			}
			agg := map[statKey][2]int64{}
			for _, ev := range batch {
				k := statKey{hour: (ev.OccurredAt / 3600) * 3600, site: ev.SiteID, group: ev.RuleGroup}
				cur := agg[k]
				if ev.Action == "observe" {
					cur[1]++
				} else {
					cur[0]++
					if ev.RuleGroup != "ban" {
						blockedIPs[ev.ClientIP] = struct{}{}
					}
				}
				agg[k] = cur
			}
			for k, counts := range agg {
				err := tx.Exec(
					`INSERT INTO waf_hourly_stats (hour_ts, site_id, rule_group, blocked_count, observed_count)
					 VALUES (?, ?, ?, ?, ?)
					 ON CONFLICT(hour_ts, site_id, rule_group) DO UPDATE SET
					 blocked_count = blocked_count + excluded.blocked_count,
					 observed_count = observed_count + excluded.observed_count`,
					k.hour, k.site, k.group, counts[0], counts[1],
				).Error
				if err != nil {
					return err
				}
			}
		}
		return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&logOffset{
			FilePath: path,
			Inode:    inode,
			Offset:   newOffset,
		}).Error
	})
	if err != nil {
		return err
	}

	if len(blockedIPs) > 0 {
		m.evaluateAutoBan(ctx, blockedIPs)
	}
	return nil
}

func (m *Module) evaluateAutoBan(ctx context.Context, ips map[string]struct{}) {
	cfg, err := m.loadGlobalConfig(ctx)
	if err != nil || !cfg.AutoBanEnabled || cfg.AutoBanThreshold <= 0 || cfg.AutoBanWindow <= 0 {
		return
	}
	now := time.Now().Unix()
	since := now - int64(cfg.AutoBanWindow)
	changed := false
	for ip := range ips {
		var count int64
		if err := m.wafDB.WithContext(ctx).Model(&Event{}).
			Where("client_ip = ? AND occurred_at >= ? AND action != 'observe'", ip, since).
			Count(&count).Error; err != nil || count < int64(cfg.AutoBanThreshold) {
			continue
		}
		var existing Ban
		if err := m.wafDB.WithContext(ctx).Where("ip = ? AND site_id = ''", ip).First(&existing).Error; err == nil {
			if existing.ExpiresAt == 0 || existing.ExpiresAt > now {
				continue
			}
		}
		expiresAt := now + int64(cfg.AutoBanDuration)
		if cfg.AutoBanDuration <= 0 {
			expiresAt = 0
		}
		ban := Ban{
			IP:        ip,
			SiteID:    "",
			Location:  geoip.Location(ip),
			Reason:    "高频触发 WAF 防护规则自动封禁",
			Source:    "auto",
			NFTSynced: cfg.SyncFirewall && m.firewall != nil,
			ExpiresAt: expiresAt,
			CreatedAt: now,
		}
		if err := m.saveBan(ctx, &ban); err != nil {
			slog.Warn("自动封禁保存失败", "ip", ip, "error", err)
			if !ban.NFTSynced {
				continue
			}
			ban.NFTSynced = false
			if err := m.saveBan(ctx, &ban); err != nil {
				slog.Warn("自动封禁降级到 WAF 失败", "ip", ip, "error", err)
				continue
			}
		}
		changed = true
	}
	if changed {
		if err := m.SyncEngine(ctx, false); err != nil && !isNonFatalSyncErr(err, m.cfg.Dev) {
			slog.Warn("自动封禁同步引擎失败", "error", err)
		}
	}
}

func (m *Module) restoreFirewallBans(ctx context.Context) {
	if m.firewall == nil {
		return
	}
	var ips []string
	if err := m.wafDB.WithContext(ctx).Model(&Ban{}).Where("nft_synced = ?", true).Distinct("ip").Pluck("ip", &ips).Error; err != nil {
		slog.Warn("读取 WAF 联动封禁失败", "error", err)
		return
	}
	for _, ip := range ips {
		if err := m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return m.reconcileFirewallBan(ctx, tx, ip)
		}); err != nil {
			slog.Warn("恢复 WAF 防火墙联动失败", "ip", ip, "error", err)
		}
	}
}

func (m *Module) cleanup(ctx context.Context) {
	now := time.Now().Unix()
	expired := m.wafDB.WithContext(ctx).Model(&Ban{}).Where("expires_at > 0 AND expires_at <= ?", now)
	var ips []string
	if err := expired.Distinct("ip").Pluck("ip", &ips).Error; err != nil {
		slog.Warn("查询到期 WAF 封禁失败", "error", err)
	}
	changed := false
	for _, ip := range ips {
		if err := m.wafDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var synced int64
			due := tx.Model(&Ban{}).Where("ip = ? AND expires_at > 0 AND expires_at <= ?", ip, now)
			if err := due.Session(&gorm.Session{}).Where("nft_synced = ?", true).Count(&synced).Error; err != nil {
				return err
			}
			if err := due.Delete(&Ban{}).Error; err != nil || synced == 0 {
				return err
			}
			return m.reconcileFirewallBan(ctx, tx, ip)
		}); err != nil {
			slog.Warn("清理到期 WAF 封禁失败", "ip", ip, "error", err)
		} else {
			changed = true
		}
	}
	if changed {
		_ = m.SyncEngine(ctx, false)
	}

	cfg, err := m.loadGlobalConfig(ctx)
	retention := 30
	if err == nil && cfg.RetentionDays > 0 {
		retention = cfg.RetentionDays
	}
	cutoff := now - int64(retention)*86400
	_ = m.wafDB.WithContext(ctx).Where("occurred_at < ?", cutoff).Delete(&Event{}).Error
	statCutoff := now - 180*86400
	_ = m.wafDB.WithContext(ctx).Where("hour_ts < ?", statCutoff).Delete(&HourlyStat{}).Error
}
