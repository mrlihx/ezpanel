package pkgmgr

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"ezpanel/internal/executor"
)

//go:embed supervisorbridge/manage.py
var supervisorBridge string

type SupervisorProcess struct {
	Name        string `json:"name"`
	PID         int    `json:"pid"`
	State       string `json:"state"`
	Description string `json:"description"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

type SupervisorProgram struct {
	Name        string              `json:"name"`
	Group       string              `json:"group"`
	Command     string              `json:"command"`
	User        string              `json:"user"`
	Directory   string              `json:"directory"`
	Numprocs    int                 `json:"numprocs"`
	Priority    int                 `json:"priority"`
	Autostart   bool                `json:"autostart"`
	Autorestart string              `json:"autorestart"`
	Remark      string              `json:"remark"`
	Path        string              `json:"path"`
	Content     string              `json:"content"`
	Revision    string              `json:"revision"`
	Editable    bool                `json:"editable"`
	Processes   []SupervisorProcess `json:"processes"`
}

type SupervisorSnapshot struct {
	List    []SupervisorProgram `json:"list"`
	Users   []string            `json:"users"`
	Warning string              `json:"warning"`
	MainLog string              `json:"mainLog"`
}

func (m *Manager) supervisorCall(ctx context.Context, main, action string, input any) (json.RawMessage, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	// 使用 Supervisor 安装时的 Python，复用其解析器与 Unix socket 控制接口。
	binary, err := m.LookPath("supervisord")
	if err != nil {
		return nil, err
	}
	python := "/usr/bin/python3"
	if content, err := m.ReadFile(binary); err == nil {
		line, _, _ := strings.Cut(string(content), "\n")
		if strings.HasPrefix(line, "#!/") && !strings.ContainsAny(line[2:], " \t\r") {
			python = line[2:]
		}
	}
	result, err := executor.Pipe(ctx, 2*time.Minute, nil, strings.NewReader(string(data)), nil,
		python, "-c", supervisorBridge, main, m.layout().Root(), action)
	if err != nil {
		return nil, fmt.Errorf("Supervisor 操作失败：%s", strings.TrimSpace(result.Stderr))
	}
	if result.Truncated || !json.Valid([]byte(result.Stdout)) {
		return nil, errors.New("Supervisor 返回的数据过大或格式无效")
	}
	return json.RawMessage(result.Stdout), nil
}

func (m *Manager) Supervisor(ctx context.Context, action string, input any) (json.RawMessage, error) {
	settings, err := m.Settings(ctx, "supervisor")
	if err != nil {
		return nil, err
	}
	if action == "list" {
		return m.supervisorCall(ctx, settings.Path, action, input)
	}
	var out json.RawMessage
	err = m.ApplicationChange("supervisor", func() error {
		var err error
		out, err = m.supervisorCall(ctx, settings.Path, action, input)
		return err
	})
	return out, err
}

func (m *Manager) SupervisorSnapshot(ctx context.Context) (SupervisorSnapshot, error) {
	var snapshot SupervisorSnapshot
	raw, err := m.Supervisor(ctx, "list", nil)
	if err == nil {
		err = json.Unmarshal(raw, &snapshot)
	}
	return snapshot, err
}

func supervisorLogSources(snapshot SupervisorSnapshot) []LogSource {
	out := []LogSource{}
	if filepath.IsAbs(snapshot.MainLog) {
		out = append(out, LogSource{ID: "main", Name: "Supervisor 服务日志", Path: snapshot.MainLog, Clearable: true})
	}
	for _, program := range snapshot.List {
		for _, process := range program.Processes {
			for _, kind := range []struct{ label, key, path string }{
				{"运行日志", "stdout", process.Stdout}, {"错误日志", "stderr", process.Stderr},
			} {
				if !filepath.IsAbs(kind.path) {
					continue
				}
				sum := sha256.Sum256([]byte(program.Name + "\x00" + process.Name + "\x00" + kind.key))
				out = append(out, LogSource{ID: hex.EncodeToString(sum[:8]), Name: program.Name + " / " + process.Name + " / " + kind.label, Path: kind.path, Clearable: true})
			}
		}
	}
	return out
}
