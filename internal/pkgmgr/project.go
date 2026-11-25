package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

const projectStartupTimeout = 2 * time.Minute

func (m *Manager) StartProject(ctx context.Context, id, kind string, runtime RuntimeEnvironment, root, command, environment, registry string, port int) (string, error) {
	if err := m.ensureManagedNetwork(ctx); err != nil {
		return "", err
	}
	name := "ezpanel-site-" + id
	args := []string{"create", "--pull=never", "--name", name, "--label", "ezpanel.managed=true", "--label", "ezpanel.project=true", "--restart=no",
		"--network", ManagedNetwork,
		"--security-opt", "no-new-privileges=true", "--cap-drop", "ALL", "--log-opt", "max-size=10m", "--log-opt", "max-file=3",
		"--workdir", "/app", "--mount", "type=bind,src=" + root + ",dst=/app"}
	if port > 0 {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
		hostPort := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
		_ = listener.Close()
		args = append(args, "-p", "127.0.0.1:"+hostPort+":"+strconv.Itoa(port)+"/tcp", "-e", "PORT="+strconv.Itoa(port))
	}
	for line := range strings.SplitSeq(environment, "\n") {
		if line != "" {
			args = append(args, "-e", line)
		}
	}
	args = appendProjectRegistry(args, kind, registry)
	if kind == "python" {
		mount, err := m.pythonProjectMount(id, runtime.ID)
		if err != nil {
			return "", err
		}
		args = append(args, "--mount", mount, "--label", "ezpanel.python-venv=true")
		command = activateProjectVenv + command
	}
	if _, err := m.docker(ctx, time.Minute, append(args, runtime.Image, "sh", "-c", command)...); err != nil {
		return "", err
	}
	_, err := m.ProjectAction(ctx, id, "start")
	var target string
	if err == nil {
		target, err = m.waitProject(ctx, name, port)
	}
	if err == nil {
		return target, nil
	}

	// 新容器未通过启动检查时，将它移出正式名称，保留输出供排查和重试。
	recovery := context.WithoutCancel(ctx)
	_, _ = m.ProjectAction(recovery, id, "stop")
	_, _ = m.ProjectAction(recovery, id+"-failed", "remove")
	if archiveErr := m.RenameProject(recovery, id, id+"-failed"); archiveErr != nil {
		_, _ = m.ProjectAction(recovery, id, "remove")
		return "", errors.Join(err, archiveErr)
	}
	return "", err
}

func (m *Manager) ResumeProject(ctx context.Context, id, action string, port int) (string, error) {
	name := "ezpanel-site-" + id
	info, exists, err := m.inspectContainer(ctx, name)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", errors.New("项目容器不存在")
	}
	if err := m.connectManagedNetwork(ctx, info); err != nil {
		return "", err
	}
	if _, err := m.docker(ctx, 10*time.Second, "update", "--restart=no", name); err != nil {
		return "", err
	}
	_, _ = m.ProjectAction(ctx, id+"-failed", "remove")
	if _, err := m.ProjectAction(ctx, id, action); err != nil {
		return "", err
	}
	target, err := m.waitProject(ctx, name, port)
	if err != nil {
		_, _ = m.ProjectAction(context.WithoutCancel(ctx), id, "stop")
	}
	return target, err
}

func (m *Manager) waitProject(ctx context.Context, name string, port int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, projectStartupTimeout)
	defer cancel()
	client := &http.Client{
		Timeout:       time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	timedOut := func() error {
		if port > 0 {
			return fmt.Errorf("项目在 120 秒内未响应 HTTP 请求，请检查是否监听 0.0.0.0:%d，并查看运行日志", port)
		}
		return errors.New("120 秒内未检测到 HTTP 服务，请检查启动命令与监听地址，并查看运行日志")
	}
	for {
		info, exists, err := m.inspectContainer(ctx, name)
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", timedOut()
			}
			return "", ctx.Err()
		}
		if err != nil {
			return "", err
		}
		if !exists || !info.State.Running || info.State.Status == "restarting" {
			return "", fmt.Errorf("项目进程已退出（退出码 %d），请查看运行日志", info.State.ExitCode)
		}
		host := "127.0.0.1"
		var ports []string
		if port > 0 {
			for _, binding := range info.NetworkSettings.Ports[strconv.Itoa(port)+"/tcp"] {
				if binding.HostPort != "" {
					ports = append(ports, binding.HostPort)
				}
			}
		} else if host = info.managedAddress(); host != "" {
			result, _ := m.docker(ctx, 5*time.Second, "exec", name, "cat", "/proc/net/tcp", "/proc/net/tcp6")
			ports = listeningProjectPorts(result.Stdout)
		}
		var target string
		for _, candidate := range ports {
			address := "http://" + net.JoinHostPort(host, candidate)
			request, err := http.NewRequestWithContext(ctx, http.MethodHead, address, nil)
			if err != nil {
				continue
			}
			res, err := client.Do(request)
			if err != nil {
				continue
			}
			res.Body.Close()
			if target != "" && target != address {
				return "", errors.New("检测到多个 HTTP 服务，请让命令行启动命令只启动一个 HTTP 服务")
			}
			target = address
		}
		if target != "" {
			return target, nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return "", timedOut()
			}
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func listeningProjectPorts(table string) []string {
	var ports []string
	for line := range strings.SplitSeq(table, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[0] == "sl" || fields[3] != "0A" {
			continue
		}
		_, value, _ := strings.Cut(fields[1], ":")
		if port, err := strconv.ParseUint(value, 16, 16); err == nil && port > 0 {
			value = strconv.FormatUint(port, 10)
			if !slices.Contains(ports, value) {
				ports = append(ports, value)
			}
		}
	}
	return ports
}

func (m *Manager) ProjectAction(ctx context.Context, id, action string) (string, error) {
	name := "ezpanel-site-" + id
	switch action {
	case "logs":
		result, err := m.docker(ctx, 10*time.Second, "logs", "--timestamps", "--tail", "300", name)
		if err != nil {
			return "", err
		}
		// Docker 时间戳使用补齐的 RFC3339Nano UTC 格式，可直接按字符串合并两路日志。
		type logLine struct{ timestamp, content string }
		var lines []logLine
		for _, stream := range []string{result.Stdout, result.Stderr} {
			if stream == "" {
				continue
			}
			for line := range strings.SplitSeq(strings.TrimSuffix(stream, "\n"), "\n") {
				stamp, body, ok := strings.Cut(line, " ")
				if !ok || len(stamp) != len("2006-01-02T15:04:05.000000000Z") || !strings.HasSuffix(stamp, "Z") {
					stamp, body = "", line
				}
				lines = append(lines, logLine{stamp, body})
			}
		}
		slices.SortStableFunc(lines, func(a, b logLine) int { return strings.Compare(a.timestamp, b.timestamp) })
		var out strings.Builder
		for _, line := range lines {
			out.WriteString(line.content)
			out.WriteByte('\n')
		}
		return out.String(), nil
	case "remove":
		if _, exists, err := m.inspectContainer(ctx, name); err != nil || !exists {
			return "", err
		}
		_, err := m.docker(ctx, 30*time.Second, "rm", "-f", name)
		return "", err
	default:
		_, err := m.docker(ctx, 30*time.Second, action, name)
		return "", err
	}
}

func (m *Manager) ProjectLogs(ctx context.Context, id string) (string, bool, bool, error) {
	failedID := id + "-failed"
	_, failed, err := m.inspectContainer(ctx, "ezpanel-site-"+failedID)
	if err != nil {
		return "", false, false, err
	}
	info, exists, err := m.inspectContainer(ctx, "ezpanel-site-"+id)
	if err != nil {
		return "", false, failed, err
	}
	running := exists && info.State.Running && info.State.Status == "running"
	logID := id
	if failed {
		logID = failedID
	} else if !exists {
		return "", false, false, nil
	}
	log, err := m.ProjectAction(ctx, logID, "logs")
	return log, running, failed, err
}

func (m *Manager) RenameProject(ctx context.Context, from, to string) error {
	_, err := m.docker(ctx, 15*time.Second, "rename", "ezpanel-site-"+from, "ezpanel-site-"+to)
	return err
}

func (m *Manager) ProjectRunning(ctx context.Context, id string) bool {
	info, exists, err := m.inspectContainer(ctx, "ezpanel-site-"+id)
	return err == nil && exists && info.State.Running && info.State.Status == "running"
}

type ProjectService struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	Status    string            `json:"status"`
	Running   bool              `json:"running"`
	PID       int32             `json:"pid"`
	StartedAt string            `json:"startedAt"`
	Ports     map[string]string `json:"ports"`
}

func (m *Manager) ProjectService(ctx context.Context, id string) (ProjectService, error) {
	name := "ezpanel-site-" + id
	info, exists, err := m.inspectContainer(ctx, name)
	if err != nil || !exists {
		return ProjectService{}, err
	}
	ports := map[string]string{}
	for port, bindings := range info.HostConfig.PortBindings {
		if len(bindings) > 0 {
			ports[port] = bindings[0].HostPort
		}
	}
	return ProjectService{ID: info.ID, Name: name, Image: info.Config.Image, Status: info.State.Status, Running: info.State.Status == "running", PID: info.State.Pid, StartedAt: info.State.StartedAt, Ports: ports}, nil
}

func (m *Manager) RunningProjects(ctx context.Context) map[string]bool {
	rows := map[string]bool{}
	result, err := m.docker(ctx, 5*time.Second, "ps", "--filter", "label=ezpanel.project=true", "--filter", "status=running", "--format", "{{.Names}}")
	if err == nil {
		for _, name := range strings.Fields(result.Stdout) {
			if id, ok := strings.CutPrefix(name, "ezpanel-site-"); ok {
				rows[id] = true
			}
		}
	}
	return rows
}
