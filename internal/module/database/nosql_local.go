package database

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strconv"
	"strings"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (m *Module) noSQLEnvironment(c *gin.Context) {
	state, err := m.packages.Status(c.Request.Context(), c.Param("engine"))
	if err != nil {
		failure(c, err)
		return
	}
	response.OK(c, gin.H{"application": state, "recycleSupported": false, "recycleNotice": "此数据库暂不支持数据库回收站，删除操作永久生效"})
}

// 本机连接由实际软件和配置生成，首次接入时复用已有的本机认证，不要求手动添加。
func (m *Module) ensureNoSQLLocal(ctx context.Context, engine string) (bool, error) {
	m.localMu.Lock()
	defer m.localMu.Unlock()
	state, err := m.packages.Status(ctx, engine)
	if err != nil || !state.Installed {
		return false, err
	}
	port := 6379
	if engine == "mongodb" {
		port = 27017
	}
	v := noSQLConnection{ID: "local", IsLocal: true, Name: state.Name, Host: "127.0.0.1", Port: port, AuthSource: "admin"}
	var saved store.Setting
	key := "database." + engine + ".local"
	err = m.db.First(&saved, "key = ?", key).Error
	if err == nil {
		if err = json.Unmarshal([]byte(saved.Value), &v); err != nil {
			return true, err
		}
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return true, err
	} else {
		var rows []store.Setting
		if err = m.db.Where("key LIKE ?", "database."+engine+".%").Find(&rows).Error; err != nil {
			return true, err
		}
		for _, row := range rows {
			var old noSQLConnection
			if json.Unmarshal([]byte(row.Value), &old) == nil && old.InstanceID == "" && (old.IsLocal || ((old.Host == "localhost" || net.ParseIP(old.Host).IsLoopback()) && old.Port == port)) {
				v = old
				v.IsLocal = true
				key = row.Key
				saved = row
				break
			}
		}
	}
	if state.PkgManager == "Docker" {
		instance, err := m.packages.DatabaseInstance(ctx, engine, "default")
		if err != nil {
			return true, err
		}
		if instance.Port > 0 {
			v.Port = instance.Port
		}
		if saved.Key == "" && v.PasswordEnc == "" {
			password, readErr := os.ReadFile(state.InitialPasswordPath)
			if readErr != nil {
				return true, errors.New("本机数据库初始化凭据无法读取")
			}
			if engine == "mongodb" {
				v.Username = "panelAdmin"
			}
			v.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, string(password))
			if err != nil {
				return true, err
			}
		}
	} else {
		paths := []string{"/etc/redis/redis.conf", "/etc/redis.conf"}
		if engine == "mongodb" {
			paths = []string{"/etc/mongod.conf"}
		}
		for _, path := range paths {
			data, readErr := os.ReadFile(path)
			if os.IsNotExist(readErr) {
				continue
			}
			if readErr != nil {
				return true, errors.New("本机数据库配置读取失败")
			}
			if engine == "mongodb" {
				var cfg struct {
					Net struct {
						Port   int    `yaml:"port"`
						BindIP string `yaml:"bindIp"`
					} `yaml:"net"`
				}
				if err = yaml.Unmarshal(data, &cfg); err != nil {
					return true, errors.New("MongoDB配置解析失败")
				}
				if cfg.Net.Port > 0 {
					v.Port = cfg.Net.Port
				}
				if cfg.Net.BindIP != "" {
					for _, host := range strings.Split(cfg.Net.BindIP, ",") {
						host = strings.TrimSpace(host)
						if host != "0.0.0.0" && host != "::" {
							v.Host = host
							break
						}
					}
				}
			} else {
				for _, line := range strings.Split(string(data), "\n") {
					fields := strings.Fields(line)
					if len(fields) < 2 {
						continue
					}
					switch fields[0] {
					case "port":
						value, e := strconv.Atoi(fields[1])
						if e != nil || value < 1 || value > 65535 {
							return true, errors.New("Redis TCP端口无效或已禁用")
						}
						v.Port = value
					case "bind":
						v.Host = strings.TrimPrefix(fields[1], "-")
						if v.Host == "0.0.0.0" || v.Host == "::" {
							v.Host = "127.0.0.1"
						}
					case "requirepass":
						password := strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "requirepass")), `"'`)
						currentPassword := ""
						if v.PasswordEnc != "" {
							currentPassword, err = store.Decrypt(m.cfg.AESKey, v.PasswordEnc)
							if err != nil {
								return true, err
							}
						}
						if currentPassword == password {
							continue
						}
						v.PasswordEnc = ""
						if password != "" {
							v.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, password)
							if err != nil {
								return true, err
							}
						}
					}
				}
			}
			break
		}
	}
	v.IsLocal = true
	v.Name = state.Name
	raw, err := json.Marshal(v)
	if err != nil {
		return true, err
	}
	if saved.Value == string(raw) {
		return true, nil
	}
	err = m.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&store.Setting{Key: key, Value: string(raw)}).Error
	return true, err
}
