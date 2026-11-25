package security

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/response"
	"github.com/gin-gonic/gin"
)

var usernamePattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (m *Module) sshUsers(c *gin.Context) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		fail(c, err)
		return
	}
	rows := []gin.H{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) != 7 {
			continue
		}
		uid, _ := strconv.Atoi(fields[2])
		if uid != 0 && (strings.HasSuffix(fields[6], "nologin") || strings.HasSuffix(fields[6], "false")) {
			continue
		}
		rows = append(rows, gin.H{"name": fields[0], "uid": uid, "home": fields[5], "shell": fields[6], "system": uid < 1000})
	}
	response.OK(c, gin.H{"list": rows})
}
func passwordValid(password string) bool {
	return len(password) >= 8 && len(password) <= 1024 && !strings.ContainsAny(password, "\x00\r\n:")
}
func setPassword(c *gin.Context, name, password string) error {
	bin, err := executor.LookPath("chpasswd")
	if err != nil {
		return err
	}
	result, err := executor.Pipe(c.Request.Context(), 15*time.Second, nil, strings.NewReader(name+":"+password+"\n"), nil, bin)
	if err != nil {
		return fmt.Errorf("密码修改失败：%s", strings.TrimSpace(result.Stderr))
	}
	return nil
}
func (m *Module) sshPassword(c *gin.Context) {
	var in struct {
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	name := c.Param("name")
	if !usernamePattern.MatchString(name) || !passwordValid(in.Password) {
		response.Fail(c, response.Invalid, "账号无效，密码需8至1024字节且不能含冒号、换行")
		return
	}
	if _, err := userHome(name); err != nil {
		fail(c, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := setPassword(c, name, in.Password); err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"saved": true})
}
func (m *Module) createSSHUser(c *gin.Context) {
	var in struct {
		Name     string `json:"name"`
		Password string `json:"password"`
		Shell    string `json:"shell"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !usernamePattern.MatchString(in.Name) || !passwordValid(in.Password) || in.Shell != "/bin/bash" && in.Shell != "/bin/sh" {
		response.Fail(c, response.Invalid, "账号、密码或Shell无效，密码至少8字节")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := run(c.Request.Context(), "useradd", "--create-home", "--shell", in.Shell, in.Name); err != nil {
		fail(c, err)
		return
	}
	err := setPassword(c, in.Name, in.Password)
	if err == nil {
		_, _, err = m.ensureSSHKey(in.Name, false)
	}
	if err != nil {
		_, cleanup := run(c.Request.Context(), "userdel", "--remove", in.Name)
		fail(c, errors.Join(err, cleanup))
		return
	}
	response.OK(c, gin.H{"created": true})
}
func (m *Module) deleteSSHUser(c *gin.Context) {
	var in struct {
		ConfirmName string `json:"confirmName"`
		RemoveHome  bool   `json:"removeHome"`
	}
	if !response.Bind(c, &in) {
		return
	}
	name := c.Param("name")
	if !usernamePattern.MatchString(name) || name != in.ConfirmName {
		response.Fail(c, response.Invalid, "请输入完整账号名确认删除")
		return
	}
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		fail(c, err)
		return
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) >= 7 && fields[0] == name {
			found = true
			uid, _ := strconv.Atoi(fields[2])
			if uid < 1000 {
				response.Fail(c, response.Forbidden, "不能删除root或系统账号")
				return
			}
		}
	}
	if !found {
		response.Fail(c, response.NotFound, "账号不存在")
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	args := []string{}
	if in.RemoveHome {
		args = append(args, "--remove")
	}
	args = append(args, name)
	if _, err = run(c.Request.Context(), "userdel", args...); err != nil {
		fail(c, err)
		return
	}
	if err := os.Remove(m.sshKeyPath(name)); err != nil && !os.IsNotExist(err) {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"deleted": true})
}
func readUserKeys(name string) (string, error) {
	home, err := userHome(name)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}
