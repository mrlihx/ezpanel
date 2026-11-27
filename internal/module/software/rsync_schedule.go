package software

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/task"
	"github.com/fsnotify/fsnotify"
)

// 每个发送任务维护独立的监听器，配置变动时替换，退出时统一关闭。
func (m *Module) RunRsyncSchedule(ctx context.Context) {
	type worker struct {
		config string
		cancel context.CancelFunc
		done   chan struct{}
	}
	workers := map[string]worker{}
	defer func() {
		for _, job := range workers {
			job.cancel()
		}
		for _, job := range workers {
			<-job.done
		}
	}()
	for {
		workspace, err := m.readRsyncWorkspace(ctx)
		if err != nil {
			slog.Error("读取 Rsync 自动同步配置失败", "error", err)
		} else {
			active := map[string]bool{}
			for _, row := range workspace.Senders {
				if row.Schedule == "interval" && row.CheckMinutes == 0 {
					continue
				}
				active[row.ID] = true
				var rule rsyncRule
				for _, candidate := range workspace.Rules {
					if candidate.ID == row.RuleID {
						rule = candidate
						break
					}
				}
				config, _ := json.Marshal(struct {
					Sender rsyncSender
					Rule   rsyncRule
					Secret string
				}{row, rule, workspace.Secrets[row.ID]})
				job, exists := workers[row.ID]
				if exists && job.config == string(config) {
					continue
				}
				if exists {
					job.cancel()
					<-job.done
				}
				workerCtx, cancel := context.WithCancel(ctx)
				job = worker{config: string(config), cancel: cancel, done: make(chan struct{})}
				workers[row.ID] = job
				go func(row rsyncSender, job worker) {
					defer close(job.done)
					for workerCtx.Err() == nil {
						m.runRsyncWatch(workerCtx, workspace, row)
						select {
						case <-workerCtx.Done():
							return
						case <-time.After(time.Minute):
						}
					}
				}(row, job)
			}
			for id, job := range workers {
				if !active[id] {
					job.cancel()
					<-job.done
					delete(workers, id)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-m.rsyncChanged:
		}
	}
}

func (m *Module) runRsyncWatch(ctx context.Context, workspace rsyncWorkspace, row rsyncSender) {
	var watcher *fsnotify.Watcher
	var events <-chan fsnotify.Event
	var watchErrors <-chan error
	var rescanAt time.Time
	addTree := func(root string) error {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if entry.IsDir() {
				return watcher.Add(path)
			}
			return nil
		})
	}
	if row.Schedule == "realtime" {
		var err error
		watcher, err = fsnotify.NewBufferedWatcher(256)
		if err != nil {
			slog.Error("创建 Rsync 实时监听失败", "task", row.Name, "error", err)
			return
		}
		defer watcher.Close()
		if err = watcher.Add(filepath.Dir(row.Source)); err != nil {
			slog.Error("监听 Rsync 数据源父目录失败", "task", row.Name, "error", err)
			return
		}
		if err := addTree(row.Source); err != nil {
			rescanAt = time.Now().Add(time.Minute)
			slog.Error("监听 Rsync 数据源目录失败", "task", row.Name, "error", err)
		}
		events, watchErrors = watcher.Events, watcher.Errors
	}
	var interval <-chan time.Time
	if row.CheckMinutes > 0 {
		timer := time.NewTicker(time.Duration(row.CheckMinutes) * time.Minute)
		defer timer.Stop()
		interval = timer.C
	}
	// 合并连续写入；已有同步占用资源时保留待同步标记。
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	completed := make(chan error, 1)
	dirty := row.Schedule == "realtime"
	trigger := "实时同步"
	var retryAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if !pathsafe.Contains(row.Source, event.Name) {
				continue
			}
			dirty, trigger = true, "文件变更触发同步"
			if event.Has(fsnotify.Create) {
				if info, err := os.Lstat(event.Name); err == nil && info.IsDir() {
					if err := addTree(event.Name); err != nil {
						rescanAt = time.Now().Add(time.Minute)
						slog.Error("添加 Rsync 子目录监听失败", "task", row.Name, "error", err)
					}
				}
			}
		case err, ok := <-watchErrors:
			if !ok {
				return
			}
			dirty = true
			rescanAt = time.Now()
			slog.Error("Rsync 目录监听异常", "task", row.Name, "error", err)
		case err := <-completed:
			if err != nil {
				dirty = true
				retryAt = time.Now().Add(time.Minute)
			}
		case <-interval:
			dirty, trigger = true, "定时同步检查"
		case now := <-tick.C:
			if !rescanAt.IsZero() && !now.Before(rescanAt) {
				if err := addTree(row.Source); err != nil {
					rescanAt = now.Add(time.Minute)
					slog.Warn("重新监听 Rsync 数据源目录失败", "task", row.Name, "error", err)
				} else {
					rescanAt = time.Time{}
					dirty, trigger = true, "重新监听后同步"
				}
			}
			if !dirty || now.Before(retryAt) {
				continue
			}
			_, err := m.submitRsyncSender(ctx, workspace, row, "system", "", trigger, completed)
			if errors.Is(err, task.ErrBusy) {
				continue
			}
			if err != nil {
				slog.Error("提交 Rsync 自动同步失败", "task", row.Name, "error", err)
				retryAt = now.Add(time.Minute)
				continue
			}
			dirty = false
		}
	}
}
