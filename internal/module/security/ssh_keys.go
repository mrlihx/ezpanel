package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/ssh"
)

type managedSSHKey struct {
	Secret    string `json:"secret"`
	PublicKey string `json:"publicKey"`
}

func (m *Module) sshKeyPath(name string) string {
	return filepath.Join(m.cfg.DataDir(), "security", "ssh-keys", name+".json")
}
func (m *Module) readManagedSSHKey(name string) (managedSSHKey, error) {
	var key managedSSHKey
	data, err := os.ReadFile(m.sshKeyPath(name))
	if os.IsNotExist(err) {
		return key, nil
	}
	if err != nil {
		return key, err
	}
	err = json.Unmarshal(data, &key)
	return key, err
}

// 只轮换面板生成的登录密钥，保留账号原有的其他公钥。
func (m *Module) ensureSSHKey(name string, regenerate bool) (managedSSHKey, bool, error) {
	old, err := m.readManagedSSHKey(name)
	if err != nil || old.Secret != "" && !regenerate {
		return old, false, err
	}
	account, err := user.Lookup(name)
	if err != nil {
		return old, false, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return old, false, err
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		return old, false, err
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return old, false, err
	}
	block, err := ssh.MarshalPrivateKey(private, "ezpanel:"+name)
	if err != nil {
		return old, false, err
	}
	secret, err := store.Encrypt(m.cfg.AESKey, string(pem.EncodeToMemory(block)))
	if err != nil {
		return old, false, err
	}
	publicKey, err := ssh.NewPublicKey(public)
	if err != nil {
		return old, false, err
	}
	key := managedSSHKey{Secret: secret, PublicKey: string(ssh.MarshalAuthorizedKey(publicKey))}
	// 账号可修改自己的主目录，文件操作限制在该目录内，避免链接越界。
	home, err := os.OpenRoot(account.HomeDir)
	if err != nil {
		return old, false, err
	}
	defer home.Close()
	if err = home.MkdirAll(".ssh", 0700); err != nil {
		return old, false, err
	}
	dir, err := home.OpenRoot(".ssh")
	if err != nil {
		return old, false, err
	}
	defer dir.Close()
	directory, err := dir.Open(".")
	if err != nil {
		return old, false, err
	}
	err = errors.Join(directory.Chmod(0700), directory.Chown(uid, gid), directory.Close())
	if err != nil {
		return old, false, err
	}
	previous, err := dir.ReadFile("authorized_keys")
	if err != nil && !os.IsNotExist(err) {
		return old, false, err
	}
	saveAuthorized := func(content []byte) error {
		temp := ".ezpanel-" + store.ID()
		f, e := dir.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return e
		}
		defer dir.Remove(temp)
		_, e = f.Write(content)
		e = errors.Join(e, f.Chmod(0600), f.Chown(uid, gid), f.Close())
		if e != nil {
			return e
		}
		return dir.Rename(temp, "authorized_keys")
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimRight(string(previous), "\n"), "\n") {
		parsed, _, _, _, e := ssh.ParseAuthorizedKey([]byte(line))
		if e == nil && string(ssh.MarshalAuthorizedKey(parsed)) == old.PublicKey {
			continue
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	lines = append(lines, strings.TrimSpace(key.PublicKey)+" ezpanel:"+name)
	data, err := json.Marshal(key)
	if err != nil {
		return old, false, err
	}
	if err = write(m.sshKeyPath(name), data, 0600); err != nil {
		return old, false, err
	}
	err = saveAuthorized([]byte(strings.Join(lines, "\n") + "\n"))
	if err != nil {
		recovery := saveAuthorized(previous)
		if old.Secret == "" {
			recovery = errors.Join(recovery, os.Remove(m.sshKeyPath(name)))
		} else {
			oldData, _ := json.Marshal(old)
			recovery = errors.Join(recovery, write(m.sshKeyPath(name), oldData, 0600))
		}
		return old, false, errors.Join(err, recovery)
	}
	return key, true, nil
}
func (m *Module) sshKeys(c *gin.Context) {
	name := c.Param("name")
	if !usernamePattern.MatchString(name) {
		response.Fail(c, response.Invalid, "账号名无效")
		return
	}
	if _, err := userHome(name); err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	m.mu.Lock()
	defer m.mu.Unlock()
	var key managedSSHKey
	var err error
	if c.Request.Method == "POST" {
		var in struct {
			Regenerate bool `json:"regenerate"`
		}
		if !response.Bind(c, &in) {
			return
		}
		key, _, err = m.ensureSSHKey(name, in.Regenerate)
	} else {
		key, err = m.readManagedSSHKey(name)
	}
	if err != nil {
		fail(c, err)
		return
	}
	private := ""
	if key.Secret != "" {
		private, err = store.Decrypt(m.cfg.AESKey, key.Secret)
		if err != nil {
			fail(c, err)
			return
		}
	}
	response.OK(c, gin.H{"privateKey": private})
}
