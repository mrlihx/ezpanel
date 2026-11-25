package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) rootPassword(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.Username != "root" {
		response.Fail(c, response.Invalid, "当前连接未使用root账号，无法读取root密码")
		return
	}
	password := ""
	var err error
	if row.PasswordEnc != "" {
		password, err = store.Decrypt(m.cfg.AESKey, row.PasswordEnc)
		if err != nil {
			failure(c, err)
			return
		}
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": password})
}
func (m *Module) setRootPassword(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if row.Username != "root" {
		response.Fail(c, response.Invalid, "请使用root连接修改该账号密码")
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Password == "" || len(in.Password) > 1024 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "请输入密码，最多1024字节且不能含空字符")
		return
	}
	encrypted, err := store.Encrypt(m.cfg.AESKey, in.Password)
	if err != nil {
		failure(c, err)
		return
	}
	release, err := m.tasks.Acquire(LockKey(row.ID))
	if err != nil {
		failure(c, err)
		return
	}
	defer release()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	db, err := m.connect(ctx, row, true)
	if err != nil {
		failure(c, err)
		return
	}
	defer db.Close()
	if _, err = db.ExecContext(ctx, "ALTER USER CURRENT_USER() IDENTIFIED BY ?", in.Password); err != nil {
		failure(c, errors.New("修改ROOT密码失败，请检查密码策略与权限"))
		return
	}
	if err = m.db.Model(&row).Update("password_enc", encrypted).Error; err != nil {
		failure(c, errors.New("ROOT密码已修改，但面板凭据更新失败，请维护面板连接凭据后重试"))
		return
	}
	if row.IsLocal && row.InstanceID == "" {
		state, err := m.packages.Status(ctx, row.Type)
		if err == nil && state.InitialPasswordPath != "" {
			path := state.InitialPasswordPath
			file, err := os.CreateTemp(filepath.Dir(path), ".root-password-")
			if err == nil {
				defer os.Remove(file.Name())
				err = file.Chmod(0600)
				if err == nil {
					_, err = file.WriteString(in.Password)
				}
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
				if err == nil {
					err = os.Rename(file.Name(), path)
				}
			}
			if err != nil {
				failure(c, errors.New("ROOT及面板密码已更新，但初始化凭据文件更新失败"))
				return
			}
		}
	}
	response.OK(c, nil)
}

func (m *Module) resetRootPassword(c *gin.Context) {
	row, ok := m.row(c)
	if !ok {
		return
	}
	if !row.IsLocal || row.Type != "mysql" || row.InstanceID != "" {
		response.Fail(c, response.Invalid, "强制恢复仅支持默认本机 MySQL；其他实例需使用其管理员连接修改密码")
		return
	}
	var in struct {
		Password     string `json:"password"`
		Confirmation string `json:"confirmation"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Confirmation != "确认重置ROOT密码" || in.Password == "" || len(in.Password) > 1024 || strings.ContainsAny(in.Password, "\x00\r\n") {
		response.Fail(c, response.Invalid, "请输入确认文案和有效的新密码，密码不能包含换行或空字符")
		return
	}
	encrypted, err := store.Encrypt(m.cfg.AESKey, in.Password)
	if err != nil {
		failure(c, err)
		return
	}
	id, err := m.tasks.Submit(task.Meta{Module: "database", Action: "reset_root", Target: "本机MySQL ROOT", ResourceID: row.ID, Username: auth.Current(c).User.Username, IP: m.auth.ClientIP(c), LockKey: LockKey(row.ID)}, func(ctx context.Context, log *task.Log) (any, error) {
		log.Secrets(in.Password)
		row.Username = "root"
		row.PasswordEnc = encrypted
		err := m.packages.ResetMySQLRoot(ctx, in.Password, func(ctx context.Context) error {
			db, err := m.connect(ctx, row, false)
			if err != nil {
				return errors.New("无法使用新密码连接ROOT账号")
			}
			db.Close()
			if err = m.db.Model(&row).Select("username", "password_enc").Updates(&row).Error; err != nil {
				return errors.New("ROOT已更新，但面板凭据同步失败")
			}
			state, err := m.packages.Status(ctx, "mysql")
			if err != nil {
				return err
			}
			if state.InitialPasswordPath != "" {
				if err = os.WriteFile(state.InitialPasswordPath, []byte(in.Password), 0600); err != nil {
					return errors.New("ROOT与面板密码已更新，私有凭据文件同步失败")
				}
			}
			return nil
		}, log.Line)
		return nil, err
	})
	if err != nil {
		failure(c, err)
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}
