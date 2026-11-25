package pkgmgr

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	"gopkg.in/yaml.v3"
)

const databaseLogRotateSize = 10 << 20
const databaseLogCopies = 3

var databaseLogTimestamp = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}-[0-9]{2}-[0-9]{2}(\.[0-9]+)?Z?$`)

func (m *Manager) RunDatabaseLogRotation(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, app := range []string{"mongodb", "redis", "memcached"} {
				root := m.layout().AppLogDir(app)
				entries, err := os.ReadDir(root)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					slog.Warn("读取数据库日志目录失败", "app", app, "error", err)
					continue
				}
				for _, entry := range entries {
					instance := entry.Name()
					if !entry.IsDir() || (instance != "default" && !databaseInstanceID.MatchString(instance)) {
						continue
					}
					if err := m.rotateDatabaseLog(ctx, app, instance, now.UTC()); err != nil && ctx.Err() == nil {
						slog.Warn("轮转数据库日志失败", "app", app, "instance", instance, "error", err)
					}
				}
			}
		}
	}
}

func (m *Manager) rotateDatabaseLog(ctx context.Context, app, instance string, now time.Time) error {
	dir := m.layout().AppInstanceLogDir(app, instance)
	name := app + ".log"
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.Mode().IsRegular() && info.Size() >= databaseLogRotateSize {
		rotated := path + "." + now.Format("2006-01-02T15-04-05.000000000")
		err = m.ApplicationChange(app, func() error {
			switch app {
			case "mongodb":
				return m.rotateMongoLog(ctx, instance)
			case "memcached":
				return snapshotMemcachedLog(path, rotated)
			default:
				// Redis 每次写入时重新打开日志，改名后会使用原路径创建新文件。
				return os.Rename(path, rotated)
			}
		})
		if err != nil {
			if errors.Is(err, ErrBusy) || errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var archives []string
	for _, entry := range entries {
		stamp, matches := strings.CutPrefix(entry.Name(), name+".")
		if matches && entry.Type().IsRegular() && databaseLogTimestamp.MatchString(stamp) {
			archives = append(archives, entry.Name())
		}
	}
	for _, file := range archives[:max(0, len(archives)-databaseLogCopies)] {
		if err := os.Remove(filepath.Join(dir, file)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (m *Manager) rotateMongoLog(ctx context.Context, instance string) error {
	row, err := m.DatabaseInstance(ctx, "mongodb", instance)
	if err != nil || !row.Running {
		return err
	}
	result, err := m.docker(ctx, 5*time.Second, "exec", row.ContainerID, "cat", databaseConfigurations["mongodb"].mount)
	if err != nil {
		return err
	}
	var settings struct {
		SystemLog struct {
			Path      string `yaml:"path"`
			LogRotate string `yaml:"logRotate"`
		} `yaml:"systemLog"`
	}
	if err := yaml.Unmarshal([]byte(result.Stdout), &settings); err != nil {
		return err
	}
	// 自定义路径和 reopen 模式交给用户配置的轮转器处理。
	if settings.SystemLog.Path != containerMongoLog || settings.SystemLog.LogRotate == "reopen" {
		return nil
	}
	_, err = m.docker(ctx, 5*time.Second, "exec", row.ContainerID, "sh", "-c", `[ "$(cat /proc/1/comm)" != mongod ] || kill -USR1 1`)
	return err
}

func snapshotMemcachedLog(path, archive string) error {
	// Memcached 持有启动时重定向的文件句柄，轮转时保留活动文件的 inode。
	src, err := os.OpenFile(path, os.O_RDWR|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return err
	}
	defer src.Close()
	info, err := src.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < databaseLogRotateSize {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".memcached-log-")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	_, copyErr := io.CopyN(temp, src, info.Size())
	if err := errors.Join(copyErr, temp.Sync(), temp.Close()); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), archive); err != nil {
		return err
	}
	return src.Truncate(0)
}
