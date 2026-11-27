package website

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	certificates "ezpanel/internal/module/certificate"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

// BindStoredCertificate 只绑定证书夹中覆盖站点全部域名的证书，调用方持有网站任务锁。
func (m *Module) BindStoredCertificate(ctx context.Context, siteID, certificateID string, forceHTTPS bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var row store.Website
	if err := m.db.WithContext(ctx).Take(&row, "id = ?", siteID).Error; err != nil {
		return err
	}
	cert, key, err := certificates.ReadPEM(m.db, m.cfg, certificateID)
	if err != nil {
		return err
	}
	if _, err := certificate(cert, key, websiteDomains(row)); err != nil {
		return err
	}
	row.CertificateID, row.SSLEnabled, row.ForceHTTPS, row.SSLAllowMismatch = certificateID, true, forceHTTPS, false
	if err := m.validateSites(row); err != nil {
		return err
	}
	return m.change(ctx, row, false, &cert, &key, func() error { return m.db.Save(&row).Error })
}

func (m *Module) checkSSL(c *gin.Context) {
	var in struct {
		CertificateID string `json:"certificateId"`
	}
	if !response.Bind(c, &in) {
		return
	}
	row, ok := m.row(c)
	if !ok {
		return
	}
	cert, key, err := certificates.ReadPEM(m.db, m.cfg, in.CertificateID)
	if err != nil {
		failure(c, err)
		return
	}
	leaf, err := certificate(cert, key, nil)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	response.OK(c, gin.H{"uncoveredDomains": uncoveredDomains(leaf, websiteDomains(row))})
}

// 仅替换当前仍绑定此证书的站点，沿用站点配置检查与失败回滚。
func (m *Module) ApplyRenewedCertificate(ctx context.Context, id string) error {
	var sites []store.Website
	if err := m.db.Where("certificate_id = ? AND ssl_enabled = ?", id, true).Order("id").Find(&sites).Error; err != nil {
		return err
	}
	cert, key, err := certificates.ReadPEM(m.db, m.cfg, id)
	if err != nil {
		return err
	}
	var failures []error
	for _, site := range sites {
		err := func() error {
			release, err := m.tasks.Acquire("website:" + site.ID)
			if err != nil {
				return err
			}
			defer release()
			m.mu.Lock()
			defer m.mu.Unlock()
			if err = m.db.First(&site, "id = ?", site.ID).Error; err != nil {
				return err
			}
			if !site.SSLEnabled || site.CertificateID != id {
				return nil
			}
			domains := websiteDomains(site)
			if site.SSLAllowMismatch {
				domains = nil
			}
			if _, err = certificate(cert, key, domains); err != nil {
				return err
			}
			err = m.change(ctx, site, false, &cert, &key, func() error { return nil })
			entry := store.OpLog{
				Module: "website", Action: "部署续签证书", ResourceID: site.ID, Phase: "completed",
				Target:  "「" + site.Name + "」\n绑定域名 " + strings.Join(websiteDomains(site), "、") + "\n证书 ID " + id,
				Success: err == nil, CreatedAt: time.Now().UTC(),
			}
			if err != nil {
				entry.ErrorCode = response.System
			}
			if auditErr := m.db.Create(&entry).Error; auditErr != nil {
				slog.Warn("续签证书操作日志写入失败", "site", site.ID, "error", auditErr)
			}
			return err
		}()
		if err != nil {
			failures = append(failures, fmt.Errorf("站点%s：%w", site.Name, err))
		}
	}
	return errors.Join(failures...)
}
