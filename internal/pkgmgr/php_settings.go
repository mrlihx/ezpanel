package pkgmgr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func phpSection(line string, ini bool) string {
	name := strings.TrimSpace(line[1 : len(line)-1])
	if ini && !strings.HasPrefix(name, "PATH=") && !strings.HasPrefix(name, "HOST=") {
		return ""
	}
	return name
}
func phpValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 0 && (value[0] == '"' || value[0] == '\'') {
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			return value[1 : end+1]
		}
	}
	value, _, _ = strings.Cut(value, ";")
	return strings.TrimSpace(value)
}

// PHP主配置只修改全局段；FPM结构化设置只管理主文件中的www池。
func PHPDirectives(content, section string) map[string]string {
	values := map[string]string{}
	current := ""
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = phpSection(line, section == "")
			continue
		}
		if current != section || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = phpValue(value)
		}
	}
	return values
}
func SetPHPDirectives(content, section string, values map[string]string) (string, error) {
	lines := strings.Split(content, "\n")
	current, found, end := "", section == "", len(lines)
	remaining := make(map[string]string, len(values))
	for key, value := range values {
		remaining[key] = value
	}
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			next := phpSection(trimmed, section == "")
			if current == section && next != section {
				end = i
				break
			}
			current = next
			found = found || current == section
			continue
		}
		if current != section || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, _, ok := strings.Cut(trimmed, "=")
		key = strings.TrimSpace(key)
		if value, exists := values[key]; ok && exists {
			lines[i] = key + " = " + value
			delete(remaining, key)
		}
	}
	if !found {
		return "", errors.New("主配置中没有www池，请在FPM配置文件中编辑实际池配置")
	}
	additions := []string{}
	// 固定排序让相同修改产生稳定配置。
	for _, key := range phpSettingKeys {
		if value, ok := remaining[key]; ok {
			additions = append(additions, key+" = "+value)
		}
	}
	lines = append(lines[:end], append(additions, lines[end:]...)...)
	return strings.Join(lines, "\n"), nil
}

var phpSettingKeys = []string{"upload_max_filesize", "post_max_size", "max_execution_time", "max_input_time", "memory_limit", "disable_functions", "pm", "pm.max_children", "pm.start_servers", "pm.min_spare_servers", "pm.max_spare_servers", "pm.max_requests", "pm.process_idle_timeout", "pm.status_path"}
var phpFunctionName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var phpMemory = regexp.MustCompile(`^(?:-1|[1-9][0-9]{0,8}[KMG]?)$`)

func (m *Manager) applyPHPConfig(ctx context.Context, id string, reload bool) error {
	name := "ezpanel-" + id
	check := append([]string{"exec", name}, m.serviceCommand(id)...)
	if _, err := m.docker(ctx, 30*time.Second, append(check, "-t")...); err != nil {
		return fmt.Errorf("PHP-FPM 配置检查失败：%w", err)
	}
	if reload {
		// 官方 PHP 镜像以 FPM 为容器主进程，USR2 使其重新读取配置。
		if _, err := m.docker(ctx, 15*time.Second, "kill", "--signal=USR2", name); err != nil {
			return fmt.Errorf("PHP-FPM 重载信号发送失败：%w", err)
		}
	}
	return nil
}

func ValidatePHPValues(kind string, values map[string]string, current map[string]string) error {
	if len(values) == 0 {
		return errors.New("没有需要保存的设置")
	}
	for key, value := range values {
		valid := false
		if kind == "ini" {
			switch key {
			case "upload_max_filesize", "post_max_size", "memory_limit":
				valid = phpMemory.MatchString(value) && (value != "-1" || key == "memory_limit")
			case "max_execution_time", "max_input_time":
				n, e := strconv.Atoi(value)
				valid = e == nil && n <= 86400 && (n >= 0 || key == "max_input_time" && n == -1)
			case "disable_functions":
				valid = len(value) <= 16384
				if value != "" {
					for _, name := range strings.Split(value, ",") {
						valid = valid && phpFunctionName.MatchString(name)
					}
				}
			}
		} else {
			switch key {
			case "pm":
				valid = value == "dynamic" || value == "static" || value == "ondemand"
			case "pm.max_children", "pm.start_servers", "pm.min_spare_servers", "pm.max_spare_servers":
				n, e := strconv.Atoi(value)
				valid = e == nil && n >= 1 && n <= 5000
			case "pm.max_requests":
				n, e := strconv.Atoi(value)
				valid = e == nil && n >= 0 && n <= 10000000
			case "pm.process_idle_timeout":
				n, e := strconv.Atoi(strings.TrimSuffix(value, "s"))
				valid = e == nil && n >= 1 && n <= 86400
			case "pm.status_path":
				valid = value == "/__ezpanel_fpm_status" || value == "/ezpanel-fpm-status"
			}
		}
		if !valid {
			return fmt.Errorf("%s设置无效", key)
		}
	}
	if kind == "fpm" {
		merged := map[string]string{}
		for k, v := range current {
			merged[k] = v
		}
		for k, v := range values {
			merged[k] = v
		}
		if merged["pm"] == "dynamic" {
			children, _ := strconv.Atoi(merged["pm.max_children"])
			start, _ := strconv.Atoi(merged["pm.start_servers"])
			low, _ := strconv.Atoi(merged["pm.min_spare_servers"])
			high, _ := strconv.Atoi(merged["pm.max_spare_servers"])
			if low < 1 || low > start || start > high || high > children {
				return errors.New("动态模式需满足：1 ≤ 最小空闲进程 ≤ 初始进程 ≤ 最大空闲进程 ≤ 最大子进程")
			}
		}
	}
	return nil
}

func (m *Manager) PHPInfo(ctx context.Context, id string) (string, error) {
	result, err := m.docker(ctx, 15*time.Second, "exec", "ezpanel-"+id, "php", "-c", m.phpDirectory(id)+"/php.ini", "-r", "phpinfo(INFO_GENERAL | INFO_CONFIGURATION | INFO_MODULES);")
	if err != nil {
		return "", err
	}
	if result.Truncated {
		return "", errors.New("PHP信息超过1MiB限制")
	}
	return result.Stdout, nil
}
func (m *Manager) PHPExtensions(ctx context.Context, id string) ([]PHPExtension, error) {
	result, err := m.docker(ctx, 15*time.Second, "exec", "ezpanel-"+id, "php", "-c", m.phpDirectory(id)+"/php.ini", "-r", `$r=[];$seen=[];foreach(array_merge(get_loaded_extensions(),get_loaded_extensions(true)) as $n){if(isset($seen[$n]))continue;$seen[$n]=true;$r[]=array('name'=>$n,'version'=>phpversion($n)?:'');}echo json_encode($r);`)
	if err != nil {
		return nil, err
	}
	var rows []PHPExtension
	if err = json.Unmarshal([]byte(result.Stdout), &rows); err != nil {
		return nil, errors.New("读取PHP扩展失败，请检查php.ini和服务日志")
	}
	base, err := m.docker(ctx, 15*time.Second, "exec", "ezpanel-"+id, "php", "-n", "-r", `echo json_encode(array_values(array_unique(array_merge(get_loaded_extensions(),get_loaded_extensions(true)))));`)
	if err != nil {
		return nil, err
	}
	var builtin []string
	if err = json.Unmarshal([]byte(base.Stdout), &builtin); err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Builtin = contains(builtin, rows[i].Name)
		name := PHPExtensionModule(rows[i].Name)
		rows[i].Module = name
		rows[i].Source = phpExtensionSource(id, name)
		_, e := os.Stat(m.phpExtensionINI(id, name))
		rows[i].Managed = e == nil
	}
	return rows, nil
}

type PHPExtension struct {
	Module  string `json:"module"`
	Source  string `json:"source"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Builtin bool   `json:"builtin"`
	Managed bool   `json:"managed"`
}

func (m *Manager) PHPFPMStatus(ctx context.Context, id string) (map[string]any, error) {
	settings, err := m.Settings(ctx, id)
	if err != nil {
		return nil, err
	}
	values := PHPDirectives(settings.Content, "www")
	path := values["pm.status_path"]
	if path == "" {
		return map[string]any{"enabled": false}, nil
	}
	endpoint := values["listen"]
	if endpoint == filepath.Join(m.defaultInstallRoot(id), "run", "fpm.sock") {
		endpoint = m.PHPSocket(phpBranch(id))
	}
	network := "tcp"
	if strings.HasPrefix(endpoint, "/") {
		network = "unix"
	} else {
		host, port, e := net.SplitHostPort(endpoint)
		if e != nil {
			return nil, errors.New("无法识别FPM本机监听地址")
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()) {
			return nil, errors.New("负载状态只支持本机FPM端点")
		}
		if ip.IsUnspecified() {
			host = "127.0.0.1"
		}
		endpoint = net.JoinHostPort(host, port)
	}
	data, err := readFPMStatus(ctx, network, endpoint, path)
	if err != nil {
		return nil, err
	}
	return map[string]any{"enabled": true, "values": data}, nil
}
