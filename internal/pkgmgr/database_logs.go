package pkgmgr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ezpanel/internal/logview"
)

func (m *Manager) DatabaseInstanceLog(ctx context.Context, id string, row DatabaseInstance, path string) (logview.Text, error) {
	if path == "" || path == "stderr" {
		result, err := m.ReadContainerLog(ctx, row.ContainerID)
		body := result.Stdout + result.Stderr
		return logview.Text{Path: "容器日志", Content: strings.ToValidUTF8(body, "�"), Size: int64(len(body)), Truncated: result.Truncated}, err
	}
	if local := m.mountedDatabaseLogPath(id, row, path); local != "" {
		out, err := logview.Read(local)
		out.Clearable = false
		return out, err
	}
	if !row.Running {
		return logview.Text{Path: path}, errors.New("实例已停止，启动后可读取容器内日志文件")
	}
	result, err := m.docker(ctx, 8*time.Second, "exec", row.ContainerID, "sh", "-c",
		`if [ -f "$1" ]; then tail -c 524288 -- "$1"; fi`, "log", path)
	return logview.Text{Path: path, Content: strings.ToValidUTF8(result.Stdout, "�"), Size: int64(len(result.Stdout)), Truncated: len(result.Stdout) == 524288}, err
}

func (m *Manager) MySQLErrorLog(ctx context.Context, row DatabaseInstance, clear bool) (logview.Text, error) {
	logPath := filepath.Join(m.layout().AppInstanceLogDir("mysql", row.ID), "error.log")
	if clear {
		return logview.Text{}, logview.Clear(logPath)
	}
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return m.DatabaseInstanceLog(ctx, "mysql", row, "")
	}
	return logview.Read(logPath)
}

func (m *Manager) MongoInstanceLogPath(ctx context.Context, row DatabaseInstance) string {
	result, err := m.docker(ctx, 5*time.Second, "exec", row.ContainerID, "cat", databaseConfigurations["mongodb"].mount)
	if err != nil {
		return ""
	}
	return mongoOptionValues(result.Stdout)["logPath"]
}
