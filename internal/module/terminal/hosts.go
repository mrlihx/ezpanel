package terminal

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"ezpanel/internal/auth"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
	"gorm.io/gorm"
)

type hostDetails struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Username    string `json:"username"`
	AuthType    string `json:"authType"`
	Fingerprint string `json:"fingerprint"`
}

type savedHost struct {
	hostDetails
	Secret string `json:"secret"`
}

type hostCredential struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"privateKey,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
}

var dnsAddress = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*\.?$`)

func hostPrefix(userID uint) string {
	return fmt.Sprintf("terminal.host.%d.", userID)
}

func (m *Module) listHosts(c *gin.Context) {
	var settings []store.Setting
	if err := m.db.WithContext(c.Request.Context()).Where("key LIKE ?", hostPrefix(auth.Current(c).User.ID)+"%").Find(&settings).Error; err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	hosts := make([]hostDetails, 0, len(settings))
	for _, row := range settings {
		var host savedHost
		if err := json.Unmarshal([]byte(row.Value), &host); err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
		hosts = append(hosts, host.hostDetails)
	}
	sort.SliceStable(hosts, func(i, j int) bool { return hosts[i].Name < hosts[j].Name })
	response.OK(c, hosts)
}

func loadHost(db *gorm.DB, key string) (savedHost, error) {
	var row store.Setting
	var host savedHost
	if err := db.First(&row, "key = ?", key).Error; err != nil {
		return host, err
	}
	err := json.Unmarshal([]byte(row.Value), &host)
	return host, err
}

func (m *Module) saveHost(c *gin.Context) {
	var in struct {
		Name        string `json:"name"`
		Address     string `json:"address"`
		Port        int    `json:"port"`
		Username    string `json:"username"`
		AuthType    string `json:"authType"`
		Fingerprint string `json:"fingerprint"`
		hostCredential
	}
	if !response.Bind(c, &in) {
		return
	}
	details := hostDetails{
		ID: c.Param("id"), Name: strings.TrimSpace(in.Name),
		Address: strings.ToLower(strings.TrimSpace(in.Address)), Port: in.Port,
		Username: strings.TrimSpace(in.Username), AuthType: in.AuthType,
		Fingerprint: strings.TrimSpace(in.Fingerprint),
	}
	if len(details.Name) == 0 || len(details.Name) > 128 || len(details.Username) == 0 || len(details.Username) > 256 ||
		strings.ContainsFunc(details.Name+details.Username, unicode.IsControl) {
		response.Fail(c, response.Invalid, "请输入有效的主机名称和用户名")
		return
	}
	if addr, ok := strings.CutPrefix(details.Address, "["); ok && strings.HasSuffix(addr, "]") {
		details.Address = strings.TrimSuffix(addr, "]")
	}
	if details.Port < 1 || details.Port > 65535 || len(details.Address) > 253 || (net.ParseIP(details.Address) == nil && !dnsAddress.MatchString(details.Address)) {
		response.Fail(c, response.Invalid, "请输入有效的主机地址和端口")
		return
	}

	if details.Fingerprint != "" {
		encoded, ok := strings.CutPrefix(details.Fingerprint, "SHA256:")
		sum, err := base64.RawStdEncoding.Strict().DecodeString(encoded)
		if !ok || err != nil || len(sum) != sha256.Size {
			response.Fail(c, response.Invalid, "SSH 主机公钥指纹格式无效，留空将在首次连接时自动记录")
			return
		}
	}

	db := m.db.WithContext(c.Request.Context())
	prefix := hostPrefix(auth.Current(c).User.ID)
	var previous savedHost
	var err error
	if details.ID != "" {
		previous, err = loadHost(db, prefix+details.ID)
		if err != nil {
			hostFailure(c, err)
			return
		}
		if details.Fingerprint == "" && details.Address == previous.Address && details.Port == previous.Port {
			details.Fingerprint = previous.Fingerprint
		}
	} else {
		details.ID = store.ID()
	}
	credentials := in.hostCredential
	credentials.PrivateKey = strings.TrimSpace(credentials.PrivateKey)
	host := savedHost{hostDetails: details}
	switch {
	case details.AuthType == "password" && credentials.Password != "" && len(credentials.Password) <= 4096:
		credentials.PrivateKey, credentials.Passphrase = "", ""
	case details.AuthType == "privateKey" && credentials.PrivateKey != "" && len(credentials.PrivateKey) <= 65536 && len(credentials.Passphrase) <= 4096:
		credentials.Password = ""
		if _, err := credentials.signer(); err != nil {
			response.Fail(c, response.Invalid, "SSH 私钥格式无效或私钥口令错误")
			return
		}
	case credentials == (hostCredential{}) && previous.ID != "":
		if details.Address != previous.Address || details.Port != previous.Port ||
			details.Username != previous.Username || details.AuthType != previous.AuthType {
			response.Fail(c, response.Invalid, "更改连接信息后，请重新填写 SSH 凭据")
			return
		}
		host.Secret = previous.Secret
	default:
		response.Fail(c, response.Invalid, "请输入有效的 SSH 密码或私钥")
		return
	}
	if host.Secret == "" {
		plain, _ := json.Marshal(credentials)
		host.Secret, err = store.Encrypt(m.key, string(plain))
		if err != nil {
			response.Fail(c, response.Internal, "")
			return
		}
	}

	value, _ := json.Marshal(host)
	row := store.Setting{Key: prefix + details.ID, Value: string(value)}
	if previous.ID == "" {
		err = db.Create(&row).Error
	} else {
		result := db.Model(&row).Update("value", row.Value)
		err = result.Error
		if err == nil && result.RowsAffected == 0 {
			err = gorm.ErrRecordNotFound
		}
	}
	if err != nil {
		hostFailure(c, err)
		return
	}
	c.Set("panel.auditName", details.Name)
	c.Set("panel.auditResource", details.ID)
	response.OK(c, details)
}

func (m *Module) deleteHost(c *gin.Context) {
	id := c.Param("id")
	result := m.db.WithContext(c.Request.Context()).Delete(&store.Setting{Key: hostPrefix(auth.Current(c).User.ID) + id})
	if result.Error != nil {
		hostFailure(c, result.Error)
		return
	}
	if result.RowsAffected == 0 {
		hostFailure(c, gorm.ErrRecordNotFound)
		return
	}
	c.Set("panel.auditResource", id)
	response.OK(c, nil)
}

func hostFailure(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "SSH 主机不存在")
		return
	}
	response.Fail(c, response.Internal, "")
}

func (c hostCredential) signer() (ssh.Signer, error) {
	if c.Passphrase != "" {
		return ssh.ParsePrivateKeyWithPassphrase([]byte(c.PrivateKey), []byte(c.Passphrase))
	}
	return ssh.ParsePrivateKey([]byte(c.PrivateKey))
}
