package database

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"fmt"
	"github.com/gin-gonic/gin"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

type redisConnection struct {
	conn   net.Conn
	reader *bufio.Reader
}

func readRESP(r *bufio.Reader, depth int) (any, error) {
	if depth > 8 {
		return nil, errors.New("Redis响应嵌套过深")
	}
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if len(line) < 3 {
		return nil, errors.New("Redis响应格式无效")
	}
	value := strings.TrimSuffix(line[1:], "\r\n")
	switch line[0] {
	case '+':
		return value, nil
	case '-':
		return nil, redisReplyError(value)
	case ':':
		return strconv.ParseInt(value, 10, 64)
	case '$':
		n, err := strconv.Atoi(value)
		if err != nil || n < -1 || n > 1<<20 {
			return nil, errors.New("Redis值超过显示限制或格式无效")
		}
		if n == -1 {
			return nil, nil
		}
		b := make([]byte, n+2)
		if _, err = io.ReadFull(r, b); err != nil {
			return nil, err
		}
		return string(b[:n]), nil
	case '*':
		n, err := strconv.Atoi(value)
		if err != nil || n < -1 || n > 10000 {
			return nil, errors.New("Redis列表超过显示限制")
		}
		out := []any{}
		for i := 0; i < n; i++ {
			item, err := readRESP(r, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	}
	return nil, errors.New("Redis响应类型无效")
}

type redisReplyError string

func (e redisReplyError) Error() string { return string(e) }
func (r *redisConnection) pipeline(commands [][]string) ([]any, error) {
	var body strings.Builder
	for _, args := range commands {
		fmt.Fprintf(&body, "*%d\r\n", len(args))
		for _, arg := range args {
			fmt.Fprintf(&body, "$%d\r\n%s\r\n", len(arg), arg)
		}
	}
	if _, err := io.WriteString(r.conn, body.String()); err != nil {
		return nil, err
	}
	out := make([]any, len(commands))
	for i := range commands {
		value, err := readRESP(r.reader, 0)
		if err != nil {
			var reply redisReplyError
			if !errors.As(err, &reply) {
				return nil, err
			}
			value = err
		}
		out[i] = value
	}
	return out, nil
}
func (r *redisConnection) cmd(args ...string) (any, error) {
	out, err := r.pipeline([][]string{args})
	if err != nil {
		return nil, err
	}
	if err, ok := out[0].(error); ok {
		return nil, err
	}
	return out[0], nil
}
func (m *Module) withRedis(c *gin.Context, fn func(*redisConnection) error) {
	if c.Param("engine") != "redis" {
		response.Fail(c, response.Invalid, "仅用于Redis")
		return
	}
	v, password, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	database := c.DefaultQuery("database", "0")
	n, err := strconv.Atoi(database)
	if err != nil || n < 0 || n > 65535 {
		response.Fail(c, response.Invalid, "逻辑库编号无效")
		return
	}
	if err = withRedisConnection(c.Request.Context(), v, password, database, fn); err != nil {
		failure(c, errors.New("Redis操作失败："+response.Summary(err.Error(), password)))
	}
}
func withRedisConnection(parent context.Context, v noSQLConnection, password, database string, fn func(*redisConnection) error) error {
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	address := net.JoinHostPort(v.Host, strconv.Itoa(v.Port))
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var conn net.Conn
	var err error
	if v.TLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: v.Host}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return errors.New("Redis连接失败，请检查地址和TLS配置")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	r := &redisConnection{conn, bufio.NewReader(conn)}
	if password != "" || v.Username != "" {
		args := []string{"AUTH"}
		if v.Username != "" {
			args = append(args, v.Username)
		}
		args = append(args, password)
		if _, err = r.cmd(args...); err != nil {
			return errors.New("Redis认证失败")
		}
	}
	if _, err = r.cmd("SELECT", database); err != nil {
		return err
	}
	return fn(r)
}

func (m *Module) redisDatabases(c *gin.Context) {
	m.withRedis(c, func(r *redisConnection) error {
		value, err := r.cmd("INFO", "keyspace")
		if err != nil {
			return err
		}
		text, ok := value.(string)
		if !ok {
			return errors.New("Redis逻辑库响应无效")
		}
		counts := map[int]int64{}
		highest := 0
		for _, line := range strings.Split(text, "\r\n") {
			if !strings.HasPrefix(line, "db") {
				continue
			}
			name, details, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			index, err := strconv.Atoi(strings.TrimPrefix(name, "db"))
			if err != nil {
				continue
			}
			if index > highest {
				highest = index
			}
			for _, item := range strings.Split(details, ",") {
				if strings.HasPrefix(item, "keys=") {
					counts[index], _ = strconv.ParseInt(strings.TrimPrefix(item, "keys="), 10, 64)
				}
			}
		}
		total := 0
		configured, err := r.cmd("CONFIG", "GET", "databases")
		if err == nil {
			if values, ok := configured.([]any); ok && len(values) == 2 {
				if value, ok := values[1].(string); ok {
					total, _ = strconv.Atoi(value)
				}
			}
		} else {
			var reply redisReplyError
			if !errors.As(err, &reply) {
				return err
			}
		}
		out := []gin.H{}
		limit := total
		if limit == 0 {
			limit = highest + 1
		}
		for i := 0; i < limit; i++ {
			if total == 0 && i != 0 && counts[i] == 0 {
				continue
			}
			out = append(out, gin.H{"index": i, "name": strconv.Itoa(i), "keys": counts[i]})
		}
		response.OK(c, gin.H{"list": out})
		return nil
	})
}
func (m *Module) redisKeyList(c *gin.Context) {
	cursor := c.DefaultQuery("cursor", "0")
	if _, err := strconv.ParseUint(cursor, 10, 64); err != nil {
		response.Fail(c, response.Invalid, "Redis 扫描游标无效")
		return
	}
	pattern := c.DefaultQuery("pattern", "*")
	m.withRedis(c, func(r *redisConnection) error {
		names := make(map[string]bool)
		pageNames := []string{}
		// 稀疏匹配时只补扫少量批次，保留游标供下一次加载。
		for round := 0; round < 4; round++ {
			result, err := r.cmd("SCAN", cursor, "MATCH", pattern, "COUNT", "100")
			if err != nil {
				return err
			}
			scan, ok := result.([]any)
			if !ok || len(scan) != 2 {
				return errors.New("Redis扫描结果无效")
			}
			cursor, ok = scan[0].(string)
			if !ok {
				return errors.New("Redis扫描游标无效")
			}
			keys, ok := scan[1].([]any)
			if !ok {
				return errors.New("Redis键列表无效")
			}
			for _, raw := range keys {
				name, ok := raw.(string)
				if !ok {
					return errors.New("Redis键名无效")
				}
				if !names[name] {
					names[name] = true
					pageNames = append(pageNames, name)
				}
			}
			if cursor == "0" || len(pageNames) >= 20 {
				break
			}
		}
		commands := make([][]string, 0, len(pageNames)*2)
		for _, name := range pageNames {
			commands = append(commands, []string{"TYPE", name}, []string{"TTL", name})
		}
		out := []gin.H{}
		if len(commands) > 0 {
			metadata, err := r.pipeline(commands)
			if err != nil {
				return err
			}
			commands = commands[:0]
			for i, name := range pageNames {
				kind, ok := metadata[i*2].(string)
				ttl, ttlOK := metadata[i*2+1].(int64)
				if !ok || !ttlOK || kind == "none" || ttl == -2 {
					continue
				}
				row := gin.H{"name": name, "type": kind, "ttl": ttl, "value": "", "length": nil}
				out = append(out, row)
				length := "STRLEN"
				switch kind {
				case "hash":
					length = "HLEN"
				case "list":
					length = "LLEN"
				case "set":
					length = "SCARD"
				case "zset":
					length = "ZCARD"
				case "stream":
					length = "XLEN"
				}
				commands = append(commands, []string{length, name})
				if kind == "string" {
					commands = append(commands, []string{"GETRANGE", name, "0", "255"})
				}
			}
			if len(commands) > 0 {
				values, err := r.pipeline(commands)
				if err != nil {
					return err
				}
				for _, row := range out {
					if length, ok := values[0].(int64); ok {
						row["length"] = length
					}
					values = values[1:]
					if row["type"] == "string" {
						if value, ok := values[0].(string); ok {
							row["value"] = value
						}
						values = values[1:]
					} else {
						row["value"] = fmt.Sprintf("%v · %v项", row["type"], row["length"])
					}
				}
			}
		}
		response.OK(c, gin.H{"nextCursor": cursor, "list": out})
		return nil
	})
}
func (m *Module) redisKeys(c *gin.Context) {
	pattern := c.DefaultQuery("pattern", "*")
	cursor := c.DefaultQuery("cursor", "0")
	if _, err := strconv.ParseUint(cursor, 10, 64); err != nil {
		response.Fail(c, response.Invalid, "游标无效")
		return
	}
	m.withRedis(c, func(r *redisConnection) error {
		out, err := r.cmd("SCAN", cursor, "MATCH", pattern, "COUNT", "100")
		if err != nil {
			return err
		}
		response.OK(c, out)
		return nil
	})
}
func (m *Module) redisValue(c *gin.Context) {
	key := c.Query("key")
	if key == "" || len(key) > 1024 {
		response.Fail(c, response.Invalid, "键名无效")
		return
	}
	m.withRedis(c, func(r *redisConnection) error {
		kind, err := r.cmd("TYPE", key)
		if err != nil {
			return err
		}
		var args []string
		switch kind {
		case "none":
			response.OK(c, gin.H{"type": "none", "value": nil})
			return nil
		case "string":
			args = []string{"GET", key}
		case "hash":
			args = []string{"HSCAN", key, "0", "COUNT", "100"}
		case "list":
			args = []string{"LRANGE", key, "0", "99"}
		case "set":
			args = []string{"SRANDMEMBER", key, "100"}
		case "zset":
			args = []string{"ZRANGE", key, "0", "99", "WITHSCORES"}
		default:
			response.OK(c, gin.H{"type": kind, "notice": "此类型请使用原生客户端查看"})
			return nil
		}
		value, err := r.cmd(args...)
		if err != nil {
			return err
		}
		ttl, err := r.cmd("TTL", key)
		if err != nil {
			return err
		}
		response.OK(c, gin.H{"type": kind, "value": value, "ttl": ttl})
		return nil
	})
}
func (m *Module) setRedisValue(c *gin.Context) {
	var in struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		TTL   int64  `json:"ttl"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Key == "" || len(in.Key) > 1024 || len(in.Value) > 1<<20 || in.TTL < 0 {
		response.Fail(c, response.Invalid, "键值或过期时间无效")
		return
	}
	m.withRedis(c, func(r *redisConnection) error {
		args := []string{"SET", in.Key, in.Value}
		if in.TTL > 0 {
			args = append(args, "EX", strconv.FormatInt(in.TTL, 10))
		}
		if _, err := r.cmd(args...); err != nil {
			return err
		}
		response.OK(c, nil)
		return nil
	})
}
func (m *Module) deleteRedisValue(c *gin.Context) {
	var in struct {
		Key string `json:"key"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Key == "" || len(in.Key) > 1024 {
		response.Fail(c, response.Invalid, "键名无效")
		return
	}
	m.withRedis(c, func(r *redisConnection) error {
		out, err := r.cmd("DEL", in.Key)
		if err != nil {
			return err
		}
		response.OK(c, gin.H{"deleted": out})
		return nil
	})
}

func (m *Module) deleteRedisKeys(c *gin.Context) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Keys) == 0 || len(in.Keys) > 500 {
		response.Fail(c, response.Invalid, "请选择1至500个键")
		return
	}
	for _, key := range in.Keys {
		if key == "" || len(key) > 1024 {
			response.Fail(c, response.Invalid, "键名无效")
			return
		}
	}
	v, password, err := m.noSQLConnection(c)
	if err != nil {
		failure(c, err)
		return
	}
	database := c.DefaultQuery("database", "0")
	n, err := strconv.Atoi(database)
	if err != nil || n < 0 || n > 65535 {
		response.Fail(c, response.Invalid, "逻辑库编号无效")
		return
	}
	session := auth.Current(c)
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: "delete", Target: v.Name + "/Redis DB" + database, ResourceID: v.ID, Username: session.User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey("redis:" + v.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		result := task.BatchResult{Results: []task.ItemResult{}}
		err := withRedisConnection(ctx, v, password, database, func(r *redisConnection) error {
			for _, key := range in.Keys {
				_, err := r.cmd("UNLINK", key)
				item := task.ItemResult{Target: key, Msg: "删除完成"}
				if err != nil {
					item.Code = response.System
					item.Msg = response.Summary(err.Error(), password)
					result.FailedCount++
					result.Results = append(result.Results, item)
					log.Line(key + "：" + item.Msg)
					var replyErr redisReplyError
					if !errors.As(err, &replyErr) {
						return err
					}
					continue
				}
				result.SuccessCount++
				result.Results = append(result.Results, item)
				log.Line(key + "：" + item.Msg)
			}
			return nil
		})
		if err == nil && result.FailedCount > 0 {
			err = errors.New("部分Redis键删除失败，请查看逐项结果")
		}
		return result, err
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
