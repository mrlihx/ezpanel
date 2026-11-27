package plugin

import (
	"context"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func (m *Module) restoreSiteBackup(c *gin.Context) {
	site, backup := c.Param("site"), c.Param("id")
	if !m.authorizeResource(c, "sites", site) {
		return
	}
	var in struct{ ConfirmName string }
	if !response.Bind(c, &in) {
		return
	}
	if in.ConfirmName == "" {
		response.Fail(c, response.Invalid, "请精确确认站点名称")
		return
	}
	m.submitManagedAction(c, "site-backup-restore", backup, site, "website:"+site, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		src, size, err := m.services.Backups.OpenSiteArchive(ctx, site, backup)
		if err != nil {
			log.Line("无法读取站点归档")
			return nil, err
		}
		defer src.Close()
		previous, err := m.services.Websites.RestoreSiteFiles(ctx, site, in.ConfirmName, src, size, m.services.Files.ExtractSiteBackup)
		if previous != "" {
			log.Line("原站点目录已保留在 " + previous)
		}
		if err != nil {
			log.Line(response.Summary(err.Error()))
			return nil, err
		}
		return gin.H{"backupId": backup, "siteId": site}, nil
	})
}

func (m *Module) bindSiteCertificate(c *gin.Context) {
	site := c.Param("site")
	if !m.authorizeResource(c, "sites", site) {
		return
	}
	var in struct {
		CertificateID string
		ForceHTTPS    bool
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.CertificateID == "" {
		response.Fail(c, response.Invalid, "请选择已有证书")
		return
	}
	if !m.authorizeResource(c, "certificates", in.CertificateID) {
		return
	}
	m.submitManagedAction(c, "site-certificate-bind", in.CertificateID, site, "website:"+site, func(ctx context.Context, log *task.Log) (any, error) {
		err := m.services.Websites.BindStoredCertificate(ctx, site, in.CertificateID, in.ForceHTTPS)
		if err != nil {
			log.Line(response.Summary(err.Error()))
		}
		return nil, err
	})
}

func (m *Module) renewCertificate(c *gin.Context) {
	id := c.Param("id")
	if !m.authorizeResource(c, "certificates", id) {
		return
	}
	m.submitManagedAction(c, "certificate-renew", id, id, "certificate:"+id, func(ctx context.Context, log *task.Log) (any, error) {
		message, err := m.services.Certificates.RenewPluginCertificate(ctx, id, log)
		if err != nil {
			log.Line(response.Summary(err.Error()))
		} else {
			log.Line(message)
		}
		return nil, err
	})
}
