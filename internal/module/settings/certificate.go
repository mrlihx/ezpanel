package settings

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/module/certificate"
)

// 续签只更换证书，保持访问地址、账号会话及其它配置不变。
func (m *Module) ApplyRenewedCertificate(ctx context.Context, id string) error {
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	old := m.cfg.Current()
	if old.PanelCertificateID != id || old.TLSCert == "" {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	cert, key, err := certificate.ReadPEM(m.db, old, id)
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair([]byte(cert), []byte(key))
	if err != nil {
		return err
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	if time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return errors.New("续签证书尚未生效或已过期")
	}
	origin, err := url.Parse(old.PublicURL)
	if err != nil {
		return err
	}
	if err = leaf.VerifyHostname(origin.Hostname()); err != nil {
		return err
	}
	base := filepath.Join(old.DataDir(), "panel-ssl")
	if err = os.MkdirAll(base, 0700); err != nil {
		return err
	}
	staged, err := os.MkdirTemp(base, "renewal-")
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(staged)
		}
	}()
	certPath, keyPath := filepath.Join(staged, "fullchain.pem"), filepath.Join(staged, "private.key")
	if err = os.WriteFile(certPath, []byte(cert), 0600); err != nil {
		return err
	}
	if err = os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		return err
	}
	next, err := m.cfg.Apply(func(out *config.Config) error { out.TLSCert, out.TLSKey = certPath, keyPath; return nil })
	if err != nil {
		return err
	}
	if err = m.applyNetwork(next); err != nil {
		_, restore := m.cfg.Apply(func(out *config.Config) error { out.TLSCert, out.TLSKey = old.TLSCert, old.TLSKey; return nil })
		keep = restore != nil // 回滚失败时，当前配置仍可能引用新证书。
		return errors.Join(err, restore)
	}
	keep = true
	return nil
}
