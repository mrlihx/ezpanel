package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

//go:embed schema.sql
var schema string

func ID() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

func Open(path string) (*gorm.DB, error) {
	db, e := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().UTC() }})
	if e != nil {
		return nil, e
	}
	sql, e := db.DB()
	if e != nil {
		return nil, e
	}
	sql.SetMaxOpenConns(1)
	fail := func(e error) (*gorm.DB, error) { sql.Close(); return nil, e }
	if path != ":memory:" {
		if e = os.Chmod(path, 0600); e != nil {
			return fail(e)
		}
	}
	if e = db.Exec("PRAGMA foreign_keys=ON").Error; e != nil {
		return fail(e)
	}
	if e = db.Exec(schema).Error; e != nil {
		return fail(e)
	}
	registerOpLogCallbacks(db, path)
	return db, nil
}

func aead(key string) (cipher.AEAD, error) {
	b, e := base64.StdEncoding.DecodeString(key)
	if e != nil || len(b) != 32 {
		return nil, errors.New("AES密钥无效")
	}
	c, e := aes.NewCipher(b)
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(c)
}

func Encrypt(key, value string) (string, error) {
	a, e := aead(key)
	if e != nil {
		return "", e
	}
	n := make([]byte, a.NonceSize())
	if _, e = rand.Read(n); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(a.Seal(n, n, []byte(value), nil)), nil
}

func Decrypt(key, value string) (string, error) {
	a, e := aead(key)
	if e != nil {
		return "", e
	}
	b, e := base64.StdEncoding.DecodeString(value)
	if e != nil || len(b) < a.NonceSize() {
		return "", errors.New("密文无效")
	}
	out, e := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], nil)
	if e != nil {
		return "", errors.New("无法解密，请检查配置密钥")
	}
	return string(out), nil
}

// DisableTargetJobs 保留已生成备份，仅停用指向已删除目标的单目标计划。
func DisableTargetJobs(tx *gorm.DB, kind, id, targetName, reason string) error {
	var jobs []CronJob
	if e := tx.Where("type = ?", kind).Find(&jobs).Error; e != nil {
		return e
	}
	for _, job := range jobs {
		var cfg struct {
			TargetID   string `json:"targetId"`
			AllSites   bool   `json:"allSites"`
			TargetName string `json:"targetName"`
		}
		if e := json.Unmarshal([]byte(job.Config), &cfg); e != nil {
			return errors.New("计划任务配置损坏，未删除目标")
		}
		if cfg.TargetID == id && !cfg.AllSites && (targetName == "" || cfg.TargetName == targetName) {
			if e := tx.Model(&job).Updates(map[string]any{"enabled": false, "disabled_reason": reason}).Error; e != nil {
				return e
			}
		}
	}
	return nil
}
