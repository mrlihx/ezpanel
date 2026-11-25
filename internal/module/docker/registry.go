package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

type registry struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Username    string `json:"username"`
	Remark      string `json:"remark"`
	PasswordEnc string `json:"passwordEnc,omitempty"`
}

func (m *Module) registries(c *gin.Context) {
	var rows []store.Setting
	if err := m.db.Where("key LIKE ?", "docker.registry.%").Order("key").Find(&rows).Error; err != nil {
		fail(c, err)
		return
	}
	out := []gin.H{{"id": "dockerhub", "name": "Docker Hub", "address": "docker.io", "username": "", "remark": "", "hasPassword": false, "builtin": true}}
	for _, row := range rows {
		var r registry
		if err := json.Unmarshal([]byte(row.Value), &r); err != nil {
			fail(c, errors.New("仓库配置无法读取"))
			return
		}
		out = append(out, gin.H{"id": r.ID, "name": r.Name, "address": r.Address, "username": r.Username, "remark": r.Remark, "hasPassword": r.PasswordEnc != "", "builtin": false})
	}
	response.OK(c, out)
}
func (m *Module) saveRegistry(c *gin.Context) {
	var in struct {
		ID       string  `json:"id"`
		Name     string  `json:"name"`
		Address  string  `json:"address"`
		Username string  `json:"username"`
		Remark   string  `json:"remark"`
		Password *string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	u, err := url.Parse(in.Address)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimSpace(in.Name) == "" || len(in.Name) > 64 {
		response.Fail(c, response.Invalid, "请输入名称和HTTP或HTTPS仓库地址")
		return
	}
	if utf8.RuneCountInString(in.Remark) > 512 {
		response.Fail(c, response.Invalid, "备注最多512字")
		return
	}
	r := registry{ID: in.ID, Name: in.Name, Address: strings.TrimRight(in.Address, "/"), Username: in.Username, Remark: strings.TrimSpace(in.Remark)}
	if r.ID == "" {
		r.ID = store.ID()
	} else {
		if !resourceName.MatchString(r.ID) || r.ID == "dockerhub" {
			response.Fail(c, response.Invalid, "仓库ID无效")
			return
		}
		var row store.Setting
		if err = m.db.First(&row, "key = ?", "docker.registry."+r.ID).Error; err != nil {
			fail(c, err)
			return
		}
		var old registry
		if err = json.Unmarshal([]byte(row.Value), &old); err != nil {
			fail(c, err)
			return
		}
		r.PasswordEnc = old.PasswordEnc
	}
	if in.Password != nil {
		r.PasswordEnc = ""
		if *in.Password != "" {
			r.PasswordEnc, err = store.Encrypt(m.cfg.AESKey, *in.Password)
			if err != nil {
				fail(c, err)
				return
			}
		}
	}
	b, err := json.Marshal(r)
	if err != nil {
		fail(c, err)
		return
	}
	if err = m.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&store.Setting{Key: "docker.registry." + r.ID, Value: string(b)}).Error; err != nil {
		fail(c, err)
		return
	}
	response.OK(c, gin.H{"id": r.ID})
}
func (m *Module) deleteRegistry(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	if id == "dockerhub" {
		response.Fail(c, response.Invalid, "内置仓库不能删除")
		return
	}
	res := m.db.Delete(&store.Setting{}, "key = ?", "docker.registry."+id)
	if res.Error != nil {
		fail(c, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		response.Fail(c, response.NotFound, "镜像仓库不存在")
		return
	}
	response.OK(c, nil)
}
func (m *Module) registryCredentials(id string) (map[string]string, error) {
	if id == "" || id == "dockerhub" {
		return map[string]string{}, nil
	}
	var row store.Setting
	if err := m.db.First(&row, "key = ?", "docker.registry."+id).Error; err != nil {
		return nil, err
	}
	var r registry
	if err := json.Unmarshal([]byte(row.Value), &r); err != nil {
		return nil, err
	}
	password := ""
	if r.PasswordEnc != "" {
		var err error
		password, err = store.Decrypt(m.cfg.AESKey, r.PasswordEnc)
		if err != nil {
			return nil, err
		}
	}
	return map[string]string{"serveraddress": r.Address, "username": r.Username, "password": password}, nil
}
func (m *Module) registryAuth(id string) (string, error) {
	credentials, err := m.registryCredentials(id)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(credentials)
	return base64.URLEncoding.EncodeToString(b), err
}
func (m *Module) testRegistry(c *gin.Context) {
	id, ok := resource(c)
	if !ok {
		return
	}
	credentials, err := m.registryCredentials(id)
	if err != nil {
		fail(c, err)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	if id == "dockerhub" {
		var result any
		if err = m.json(ctx, "GET", "/images/search?term=hello-world&limit=1", nil, &result); err != nil {
			fail(c, err)
			return
		}
		response.OK(c, gin.H{"status": "Docker Hub可访问"})
		return
	}
	var out struct{ Status string }
	if err = m.json(ctx, "POST", "/auth", credentials, &out); err != nil {
		fail(c, errors.New("仓库认证失败，请检查账号、密码和网络"))
		return
	}
	response.OK(c, gin.H{"status": out.Status})
}
