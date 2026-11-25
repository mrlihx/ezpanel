package database

import (
	"context"
	"errors"
	"os"
	"strings"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 安装器生成的凭据仅在首次接入时读取，不重置数据库账号或覆盖人工维护的密码。
func (m *Module) ensureLocal(ctx context.Context) (bool, error) {
	m.localMu.Lock()
	defer m.localMu.Unlock()
	state, err := m.packages.Status(ctx, "mysql")
	if err != nil {
		return false, err
	}
	if !state.Installed {
		state, err = m.packages.Status(ctx, "mariadb")
		if err != nil {
			return false, err
		}
	}
	if !state.Installed {
		return false, nil
	}
	socket := m.packages.MySQLSocket()
	if state.PkgManager != "Docker" {
		socket = "/run/mysqld/mysqld.sock"
		for _, path := range []string{socket, "/var/lib/mysql/mysql.sock", "/tmp/mysql.sock"} {
			if st, err := os.Stat(path); err == nil && st.Mode()&os.ModeSocket != 0 {
				socket = path
				break
			}
		}
		paths := []string{"/etc/mysql/mysql.conf.d/mysqld.cnf", "/etc/mysql/mariadb.conf.d/50-server.cnf", "/etc/my.cnf.d/server.cnf", "/etc/my.cnf"}
		configured := false
		for _, path := range paths {
			if data, err := os.ReadFile(path); err == nil {
				section := ""
				for _, line := range strings.Split(string(data), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "[") {
						section = line
						continue
					}
					if key, value, ok := strings.Cut(line, "="); ok && section == "[mysqld]" && strings.TrimSpace(key) == "socket" {
						socket = strings.Trim(strings.TrimSpace(value), `"'`)
						configured = true
					}
				}
				if configured {
					break
				}
			}
		}
	}
	var row store.DBServer
	err = m.db.Where("is_local = ? AND instance_id = ''", true).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// 兼容此前手动添加的同一socket实例，保留ID及备份/计划任务关联。
		err = m.db.Where("network = ? AND socket_path = ?", "unix", socket).Order("created_at,id").First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = store.DBServer{ID: "local-mysql", Name: "MySQL", Username: "root"}
		} else if err != nil {
			return true, err
		}
		row.IsLocal = true
		row.Type = state.ID
		row.Network = "unix"
		row.SocketPath = socket
		row.Host = ""
		row.Port = 0
		row.TLSMode = "disabled"
		// 旧版一键添加本机留下空root凭据时补齐；自定义账号或已存密码保持原值。
		if row.Username == "root" && row.PasswordEnc == "" && state.InitialPasswordPath != "" {
			password, err := os.ReadFile(state.InitialPasswordPath)
			if err != nil && !os.IsNotExist(err) {
				return true, errors.New("本机MySQL初始化凭据无法读取")
			}
			if len(password) > 0 {
				row.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, string(password))
				if err != nil {
					return true, err
				}
			}
		}
		if err = m.db.Save(&row).Error; err != nil {
			return true, err
		}
	} else if err != nil {
		return true, err
	} else if row.SocketPath != socket || row.Type != state.ID {
		if err = m.db.Model(&row).Updates(map[string]any{"socket_path": socket, "type": state.ID}).Error; err != nil {
			return true, err
		}
	}
	return true, nil
}

func (m *Module) localCredentials(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !row.IsLocal {
		response.Fail(c, response.Invalid, "此接口仅维护本机连接凭据")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Username == "" || len(in.Username) > 128 || len(in.Password) > 1024 || strings.ContainsRune(in.Username+in.Password, 0) {
		response.Fail(c, response.Invalid, "账号或密码格式无效")
		return
	}
	release, err := m.tasks.Acquire(LockKey(row.ID))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	row.Username = in.Username
	row.PasswordEnc = ""
	if in.Password != "" {
		row.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, in.Password)
		if err != nil {
			failure(c, err)
			return
		}
	}
	if err = m.db.Model(&row).Select("username", "password_enc").Updates(&row).Error; err != nil {
		failure(c, err)
		return
	}
	response.OK(c, nil)
}
