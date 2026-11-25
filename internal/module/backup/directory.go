package backup

import (
	"context"
	"errors"
	"ezpanel/internal/executor"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
)

type ArchiveOptions struct {
	Directory string
	Exclude   []string
	Temporary bool
}

func (m *Module) CreateDirectory(ctx context.Context, source, cronID string, log *task.Log, options ArchiveOptions) (store.SiteBackup, error) {
	var backup store.SiteBackup
	path, size, err := m.archiveDirectory(ctx, source, filepath.Base(source), "directory", log, options)
	if err != nil {
		return backup, err
	}
	backup = store.SiteBackup{BackupFile: store.BackupFile{FileName: filepath.Base(path), StorageType: "local", Method: "automatic"}, ID: store.ID(), SiteName: source, FilePath: path, Size: size, CronJobID: &cronID, CreatedAt: time.Now().UTC()}
	if err = m.db.Create(&backup).Error; err != nil {
		os.Remove(path)
		return backup, errors.New("目录备份登记失败")
	}
	log.Line(source + "：备份已登记 " + backup.ID)
	return backup, nil
}

func (m *Module) archiveDirectory(ctx context.Context, source, label, kind string, log *task.Log, options ArchiveOptions) (string, int64, error) {
	policy, e := pathsafe.New(m.cfg.InstallDir)
	if e != nil {
		return "", 0, e
	}
	root, e := policy.Check(source, false)
	if e != nil {
		return "", 0, errors.New("备份来源不可读或指向受保护目录")
	}
	info, e := os.Stat(root)
	if e != nil || !info.IsDir() {
		return "", 0, errors.New("备份来源必须是目录")
	}
	if e := policy.CheckTree(root); e != nil {
		return "", 0, errors.New("备份来源包含受保护目录")
	}
	destination := options.Directory
	if destination == "" {
		destination = m.cfg.Current().BackupRoot
	}
	name := pathsafe.BackupName(label)
	dir := filepath.Join(destination, kind, name)
	if !options.Temporary {
		if _, err := policy.Check(dir, true); err != nil {
			return "", 0, errors.New("备份目录不可用或指向受保护目录")
		}
	}
	if pathsafe.Contains(root, dir) {
		return "", 0, errors.New("备份目录位于来源目录内，拒绝递归备份")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return "", 0, errors.New("备份目录不可写")
	}
	realDir, e := filepath.EvalSymlinks(dir)
	if e != nil || pathsafe.Contains(root, realDir) {
		return "", 0, errors.New("实际备份目录不可用或位于来源目录内")
	}
	if kind == "site" {
		log.Line("│ 网站根目录 " + root)
	} else {
		log.Line("│ 备份目录 " + root)
	}
	var sourceSize int64
	e = filepath.WalkDir(root, func(entryPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if entryPath == root {
			return nil
		}
		relative := strings.TrimPrefix(entryPath, root+string(filepath.Separator))
		for _, rule := range options.Exclude {
			target := d.Name()
			if strings.Contains(rule, "/") {
				target = filepath.ToSlash(relative)
			}
			if matched, _ := path.Match(rule, target); matched {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			sourceSize += info.Size()
		}
		return nil
	})
	if e != nil {
		return "", 0, fmt.Errorf("统计备份目录大小失败: %w", e)
	}
	log.Line(fmt.Sprintf("│ 纳入备份的目录大小 %d 字节（%.2f MiB）", sourceSize, float64(sourceSize)/(1<<20)))
	if len(options.Exclude) != 0 {
		log.Line("│ 排除规则 " + strings.Join(options.Exclude, "，"))
	} else {
		log.Line("│ 排除规则 未设置")
	}
	if usage, err := disk.Usage(realDir); err == nil {
		log.Line(fmt.Sprintf("│ 可用磁盘空间 %.2f GiB，可用 Inode %d", float64(usage.Free)/(1<<30), usage.InodesFree))
	}
	location, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		location = time.UTC
	}
	path := filepath.Join(dir, name+"_"+time.Now().In(location).Format("20060102")+"_"+strconv.Itoa(rand.IntN(900000)+100000)+".tar.gz")
	part := path + ".part"
	file, e := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return "", 0, errors.New("备份文件创建失败")
	}
	defer func() { file.Close(); os.Remove(part) }()
	tar, e := executor.LookPath("tar")
	if e != nil {
		return "", 0, e
	}
	args := []string{"-czf", "-", "-C", root}
	if len(options.Exclude) > 0 {
		args = append(args, "--wildcards", "--no-wildcards-match-slash")
	}
	for _, rule := range options.Exclude {
		if strings.Contains(rule, "/") {
			args = append(args, "--anchored", "--exclude=./"+rule)
		} else {
			args = append(args, "--no-anchored", "--exclude="+rule)
		}
	}
	started := time.Now()
	log.Line("│ 开始压缩文件 " + started.In(location).Format("2006-01-02 15:04:05 MST"))
	result, e := executor.Pipe(ctx, time.Hour, nil, nil, file, tar, append(args, "--", ".")...)
	log.ExitCode(result.ExitCode)
	if result.Stderr != "" {
		log.Line(result.Stderr)
	}
	if e != nil {
		return "", 0, errors.New("目录归档失败，已有成功备份保留")
	}
	if e = file.Sync(); e != nil {
		return "", 0, e
	}
	if e = file.Close(); e != nil {
		return "", 0, e
	}
	if e = os.Link(part, path); e != nil {
		return "", 0, e
	}
	st, e := os.Stat(path)
	if e != nil {
		os.Remove(path)
		return "", 0, e
	}
	finished := time.Now()
	log.Line(fmt.Sprintf("│ 压缩完成 %s，耗时 %.2f 秒，归档大小 %d 字节", finished.In(location).Format("2006-01-02 15:04:05 MST"), finished.Sub(started).Seconds(), st.Size()))
	if options.Temporary {
		log.Line("│ 远程上传归档已准备 " + filepath.Base(path))
	} else {
		log.Line("│ 本地备份 " + path)
	}
	return path, st.Size(), nil
}
