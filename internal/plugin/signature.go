package plugin

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
)

// 管理员单独部署可信公钥。ZIP 和签名均不能自行登记公钥。
func (m *Module) verifySignature(id string, digest, signature []byte) (bool, error) {
	data, err := os.ReadFile(filepath.Join(m.packages(), ".trusted-keys", id+".pub"))
	if os.IsNotExist(err) {
		if len(signature) != 0 {
			return false, errors.New("此插件尚未登记可信公钥")
		}
		return false, nil
	}
	if err != nil {
		return false, errors.New("读取插件可信公钥失败")
	}
	if len(signature) != ed25519.SignatureSize {
		return false, errors.New("此插件要求提供 Ed25519 签名文件")
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" {
		return false, errors.New("可信公钥须为 PEM 格式的 Ed25519 公钥")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	public, ok := key.(ed25519.PublicKey)
	if err != nil || !ok {
		return false, errors.New("可信公钥须为 PEM 格式的 Ed25519 公钥")
	}
	if !ed25519.Verify(public, digest, signature) {
		return false, errors.New("插件包签名不匹配，请核对发布包")
	}
	return true, nil
}
