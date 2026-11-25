package pkgmgr

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var databaseNumberOptions = map[string]map[string]int64{
	"mysql": {
		"table_open_cache": 1, "thread_cache_size": 0, "key_buffer_size": 8,
		"tmp_table_size": 1024, "innodb_buffer_pool_size": 5 << 20, "innodb_log_buffer_size": 256 << 10,
		"max_connections": 1, "sort_buffer_size": 32 << 10, "read_buffer_size": 8192,
		"read_rnd_buffer_size": 1, "join_buffer_size": 128, "thread_stack": 128 << 10, "binlog_cache_size": 4096,
	},
	"redis":      {"timeout": 0, "maxclients": 1, "databases": 1, "maxmemory": 0, "tcp-keepalive": 0},
	"memcached":  {"m": 1, "c": 1, "t": 1},
	"mongodb":    {"maxIncomingConnections": 1, "verbosity": 0, "slowOpThresholdMs": 0},
	"postgresql": {
		"max_connections":           1,
		"max_prepared_transactions": 0,
		"bgwriter_lru_maxpages":     0,
		"max_worker_processes":      1,
		"log_min_duration_statement": -1,
	},
}

func (m *Manager) DatabaseInstanceOptions(ctx context.Context, id, instance string) (Settings, map[string]string, error) {
	config, err := m.DatabaseInstanceSettings(ctx, id, instance)
	if err != nil {
		return config, nil, err
	}
	if id == "mongodb" {
		return config, mongoOptionValues(config.Content), nil
	}
	values := map[string]string{}
	if id == "memcached" {
		lines := strings.Split(strings.TrimSpace(config.Content), "\n")
		for index := 0; index < len(lines); index++ {
			trimmed := strings.TrimSpace(lines[index])
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			fields := strings.Fields(trimmed)
			key := strings.TrimPrefix(fields[0], "-")
			if _, ok := databaseNumberOptions[id][key]; !ok {
				continue
			}
			if len(fields) >= 2 {
				values[key] = fields[1]
			} else if index+1 < len(lines) {
				values[key] = strings.TrimSpace(lines[index+1])
				index++
			}
		}
		return config, values, nil
	}
	server := id == "redis" || id == "postgresql"
	for line := range strings.SplitSeq(config.Content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			server = line == "[mysqld]" || line == "[server]"
		} else if server {
			key, value := databaseDirective(id, line)
			if _, ok := databaseNumberOptions[id][key]; ok || key == "slow_query_log" || key == "long_query_time" || key == "maxmemory-policy" || (id == "postgresql" && (key == "shared_buffers" || key == "work_mem" || key == "effective_cache_size" || key == "temp_buffers" || key == "max_stack_depth" || key == "listen_addresses")) {
				if id == "postgresql" && key == "listen_addresses" {
					value = strings.Trim(value, "'\"")
				}
				values[key] = value
			}
			if key == "log_bin" {
				if value == "OFF" || value == "0" {
					values["binlog"] = "0"
				} else {
					values["binlog"] = "1"
				}
			}
			if key == "skip_log_bin" || key == "disable_log_bin" {
				values["binlog"] = "0"
			}
		}
	}
	return config, values, nil
}

func databaseDirective(id, line string) (string, string) {
	line = strings.TrimSpace(line)
	pos := strings.IndexAny(line, "= \t")
	key, value := line, ""
	if pos >= 0 {
		key, value = line[:pos], strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line[pos:]), "="))
	}
	if id == "mysql" {
		key = strings.ReplaceAll(key, "-", "_")
	}
	return key, value
}

func (m *Manager) SaveDatabaseInstanceOptions(ctx context.Context, id, instance, revision string, values map[string]string) error {
	if len(values) == 0 {
		return errors.New("请填写要保存的配置项")
	}
	for key, value := range values {
		if min, ok := databaseNumberOptions[id][key]; ok {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil || n < min || (key == "verbosity" && n > 5) || (key == "binlog_cache_size" && n%4096 != 0) {
				return fmt.Errorf("%s 的数值无效", key)
			}
			continue
		}
		switch {
		case id == "mysql" && (key == "slow_query_log" || key == "binlog"):
			if value == "0" || value == "1" {
				continue
			}
		case id == "mysql" && key == "long_query_time":
			n, err := strconv.ParseFloat(value, 64)
			if err == nil && n >= 0 && n <= 31536000 {
				continue
			}
		case id == "redis" && key == "maxmemory-policy":
			if slices.Contains([]string{"noeviction", "allkeys-lru", "allkeys-lfu", "allkeys-random", "volatile-lru", "volatile-lfu", "volatile-random", "volatile-ttl"}, value) {
				continue
			}
		case id == "postgresql" && (key == "shared_buffers" || key == "work_mem" || key == "effective_cache_size" || key == "temp_buffers" || key == "max_stack_depth"):
			if regexpPGSize(value) {
				continue
			}
		case id == "postgresql" && key == "listen_addresses":
			if value == "localhost" || value == "*" {
				continue
			}
		case id == "mongodb" && key == "authorization":
			if value == "enabled" || value == "disabled" {
				continue
			}
		case id == "mongodb" && key == "profilingMode":
			if value == "off" || value == "slowOp" || value == "all" {
				continue
			}
		case id == "mongodb" && key == "cacheSizeGB":
			if value == "" {
				continue
			}
			n, err := strconv.ParseFloat(value, 64)
			if err == nil && n >= 0.25 && n <= 1024 {
				continue
			}
		}
		return fmt.Errorf("配置项 %s 的值无效", key)
	}
	return m.changeDatabaseInstanceSettings(ctx, id, instance, revision, func(content string) (string, error) {
		return editDatabaseOptions(id, content, values)
	})
}

func editDatabaseOptions(id, content string, values map[string]string) (string, error) {
	if id == "mongodb" {
		var document map[string]any
		if err := yaml.Unmarshal([]byte(content), &document); err != nil {
			return "", err
		}
		if document == nil {
			document = map[string]any{}
		}
		for key, value := range values {
			switch key {
			case "authorization":
				mongoSection(document, "security")[key] = value
			case "cacheSizeGB":
				storage := mongoSection(document, "storage")
				wt := mongoSection(storage, "wiredTiger")
				engine := mongoSection(wt, "engineConfig")
				if value == "" {
					delete(engine, "cacheSizeGB")
				} else {
					n, _ := strconv.ParseFloat(value, 64)
					engine["cacheSizeGB"] = n
				}
			case "profilingMode":
				mongoSection(document, "operationProfiling")["mode"] = value
			case "slowOpThresholdMs":
				n, _ := strconv.Atoi(value)
				mongoSection(document, "operationProfiling")["slowOpThresholdMs"] = n
			default:
				n, _ := strconv.Atoi(value)
				section := "net"
				if key == "verbosity" {
					section = "systemLog"
				}
				mongoSection(document, section)[key] = n
			}
		}
		result, err := yaml.Marshal(document)
		return string(result), err
	}
	var lines []string
	var binlogPath string
	server := id == "redis" || id == "postgresql"
	args := strings.Split(strings.TrimRight(content, "\n"), "\n")
	for index := 0; index < len(args); index++ {
		line := strings.TrimSpace(args[index])
		if id == "memcached" {
			fields := strings.Fields(line)
			if len(fields) > 0 && !strings.HasPrefix(fields[0], "#") {
				key := strings.TrimPrefix(fields[0], "-")
				if _, exists := values[key]; exists {
					if len(fields) == 1 && index+1 < len(args) {
						index++
					}
					continue
				}
			}
		} else if strings.HasPrefix(line, "[") {
			server = line == "[mysqld]" || line == "[server]"
		} else if server {
			key, value := databaseDirective(id, line)
			if id == "mysql" && key == "log_bin" {
				binlogPath = value
			}
			_, change := values[key]
			_, binlog := values["binlog"]
			if change || binlog && (key == "skip_log_bin" || key == "disable_log_bin") {
				continue
			}
			if binlog && key == "log_bin" {
				continue
			}
		}
		lines = append(lines, args[index])
	}
	if id == "mysql" && !server {
		lines = append(lines, "", "[mysqld]")
	}
	for _, key := range slices.Sorted(maps.Keys(values)) {
		value := values[key]
		switch id {
		case "mysql":
			if key == "binlog" {
				if binlogPath != "" && binlogPath != "0" && !strings.EqualFold(binlogPath, "off") {
					lines = append(lines, "log-bin = "+binlogPath)
				} else if value == "1" {
					lines = append(lines, "log-bin")
				}
				if value == "0" {
					lines = append(lines, "skip-log-bin")
				}
			} else {
				lines = append(lines, key+" = "+value)
			}
		case "postgresql":
			if key == "listen_addresses" {
				lines = append(lines, key+" = '"+strings.Trim(value, "'\"")+"'")
			} else {
				lines = append(lines, key+" = "+value)
			}
	case "redis":
			lines = append(lines, key+" "+value)
		case "memcached":
			lines = append(lines, "-"+key+" "+value)
		}
	}
	return strings.Join(lines, "\n") + "\n", nil
}

func mongoSection(document map[string]any, key string) map[string]any {
	if section, ok := document[key].(map[string]any); ok {
		return section
	}
	section := map[string]any{}
	document[key] = section
	return section
}

func mongoOptionValues(content string) map[string]string {
	var document map[string]any
	_ = yaml.Unmarshal([]byte(content), &document)
	values := map[string]string{"authorization": "enabled", "verbosity": "0", "bindIp": "0.0.0.0", "dbPath": "/data/db", "profilingMode": "off", "slowOpThresholdMs": "100"}
	for section, fields := range map[string][]string{
		"net": {"bindIp", "maxIncomingConnections"}, "security": {"authorization"},
		"systemLog": {"path", "verbosity"}, "operationProfiling": {"mode", "slowOpThresholdMs"},
	} {
		data, _ := document[section].(map[string]any)
		for _, field := range fields {
			if value, ok := data[field]; ok {
				switch field {
				case "path":
					field = "logPath"
				case "mode":
					field = "profilingMode"
				}
				values[field] = fmt.Sprint(value)
			}
		}
	}
	if storage, ok := document["storage"].(map[string]any); ok {
		if wt, ok := storage["wiredTiger"].(map[string]any); ok {
			if engine, ok := wt["engineConfig"].(map[string]any); ok {
				if size, ok := engine["cacheSizeGB"]; ok {
					values["cacheSizeGB"] = fmt.Sprint(size)
				}
			}
		}
	}
	return values
}

func regexpPGSize(value string) bool {
	if value == "" || len(value) > 16 {
		return false
	}
	i := 0
	for i < len(value) && value[i] >= '0' && value[i] <= '9' {
		i++
	}
	if i == 0 {
		return false
	}
	unit := value[i:]
	return unit == "" || unit == "kB" || unit == "MB" || unit == "GB" || unit == "TB" || unit == "B"
}
