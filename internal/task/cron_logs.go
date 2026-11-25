package task

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"ezpanel/internal/logview"
	"ezpanel/internal/store"
)

const logHeaderPrefix = "# log offset: "
const logHeaderSize = len(logHeaderPrefix) + 20 + 1

func (m *Manager) CronLogPath(id string) string {
	return filepath.Join(m.cfg.DataDir(), "task-logs", "cron-"+id+".log")
}

func sharedLog(row store.Task) bool {
	return strings.HasPrefix(filepath.Base(row.LogPath), "cron-")
}

func logBase(file *os.File) (int64, error) {
	var header [logHeaderSize]byte
	if _, err := file.ReadAt(header[:], 0); err != nil {
		return 0, err
	}
	return strconv.ParseInt(string(header[len(logHeaderPrefix):logHeaderSize-1]), 10, 64)
}

// 共享日志的字节位置从首次写入开始计数。清理旧记录时只更新文件头的起点。
func openCronLog(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, 0, err
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err == nil && end == 0 {
		if _, err = fmt.Fprintf(file, logHeaderPrefix+"%020d\n", 0); err == nil {
			return file, 0, nil
		}
	} else if err == nil {
		var base int64
		if base, err = logBase(file); err == nil {
			return file, base + end - int64(logHeaderSize), nil
		}
	}
	file.Close()
	return nil, 0, err
}

// 同一计划的执行记录共用日志文件，仍有执行记录引用时保留文件。
func (m *Manager) RemoveLogs(row store.Task) error {
	if sharedLog(row) {
		var count int64
		if err := m.db.Model(&store.Task{}).Where("log_path = ? AND id <> ?", row.LogPath, row.ID).Count(&count).Error; err != nil || count > 0 {
			return err
		}
	}
	if err := os.Remove(row.LogPath); !os.IsNotExist(err) {
		return err
	}
	return nil
}

// 调用方持有计划任务锁，确保整理期间不会开始新的执行。
func (m *Manager) CompactCronLog(path string) error {
	if !strings.HasPrefix(filepath.Base(path), "cron-") {
		return nil
	}
	var rows []store.Task
	err := m.db.Select("log_start", "status").Where("log_path = ?", path).Order("log_start").Find(&rows).Error
	if err != nil || len(rows) == 0 || rows[0].LogStart == 0 {
		return err
	}
	for _, row := range rows {
		if row.Status == "running" {
			return nil
		}
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close()
	base, err := logBase(src)
	if err != nil || rows[0].LogStart <= base {
		return err
	}
	if _, err = src.Seek(rows[0].LogStart-base+int64(logHeaderSize), io.SeekStart); err != nil {
		return err
	}

	next, err := os.CreateTemp(filepath.Dir(path), ".cron-log-")
	if err != nil {
		return err
	}
	defer os.Remove(next.Name())
	if _, err = fmt.Fprintf(next, logHeaderPrefix+"%020d\n", rows[0].LogStart); err == nil {
		_, err = io.Copy(next, src)
	}
	if err = errors.Join(err, next.Close()); err != nil {
		return err
	}
	return os.Rename(next.Name(), path)
}

// ReadLogTail 只读取本次执行的末尾，避免一个大任务占满历史日志响应。
func ReadLogTail(row store.Task, limit int64) (logview.Text, error) {
	text := logview.Text{Path: row.LogPath, Clearable: true}
	file, section, err := logSection(row)
	if err != nil {
		return text, err
	}
	defer file.Close()
	text.Size = section.Size()
	offset := max(0, text.Size-limit)
	data, err := io.ReadAll(io.NewSectionReader(section, offset, text.Size-offset))
	if err != nil {
		return text, err
	}
	content := string(data)
	text.Truncated = offset > 0
	if text.Truncated {
		var b [1]byte
		if _, err := section.ReadAt(b[:], offset-1); err != nil {
			return text, err
		}
		if b[0] != '\n' {
			_, content, _ = strings.Cut(content, "\n")
		}
	}
	if !strings.HasSuffix(content, "\n") {
		if pos := strings.LastIndexByte(content, '\n'); pos >= 0 {
			content = content[:pos+1]
		} else if row.Status == "running" {
			content = ""
		}
	}
	text.Content = strings.ToValidUTF8(content, "�")
	return text, nil
}
