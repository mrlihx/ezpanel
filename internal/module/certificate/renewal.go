package certificate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) reportRenewal(row store.CertificateOrder, taskID string, issueErr error) {
	if row.RenewalID == "" || m.renewalFinished == nil {
		return
	}
	status := "failed"
	if issueErr == nil {
		var latest store.CertificateOrder
		if err := m.db.Select("status").First(&latest, "id = ?", row.ID).Error; err != nil || latest.Status != "valid" {
			return
		}
		status = "success"
	}
	m.renewalFinished(row.RenewalID, row.SiteID, taskID, status)
}

func (m *Module) reconcileOrders() error {
	return m.db.Model(&store.CertificateOrder{}).Where("status IN ? AND task_id IN (SELECT id FROM tasks WHERE status = ?)", []string{"creating", "issuing"}, "interrupted").Updates(map[string]any{"status": "failed", "error": "面板重启，证书任务已中断，请重试", "updated_at": time.Now().UTC()}).Error
}

func (m *Module) retryDeployment(c *gin.Context) {
	var row store.CertificateOrder
	if err := m.db.First(&row, "id = ?", c.Param("id")).Error; err != nil {
		fail(c, err)
		return
	}
	if !row.AutoDeploy || row.CertificateID == "" || row.Status != "failed" {
		response.Fail(c, response.Invalid, "仅已签发且部署失败的自动续签订单可以重试部署")
		return
	}
	m.submitOrder(c, row, true)
}

func (m *Module) deployIssued(ctx context.Context, row store.CertificateOrder, log *task.Log) error {
	if m.deployRenewal == nil {
		return errors.New("证书自动部署未接入")
	}
	if err := m.db.Model(&store.CertificateOrder{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "issuing", "task_id": log.TaskID(), "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	log.Line("证书已签发，开始部署至仍绑定此证书的站点和面板")
	if err := m.deployRenewal(ctx, row.CertificateID); err != nil {
		return fmt.Errorf("证书已保存，自动部署失败，可重试部署：%w", err)
	}
	if err := m.db.Model(&store.CertificateOrder{}).Where("id = ?", row.ID).Updates(map[string]any{"status": "valid", "error": "", "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	log.Line("证书自动部署完成")
	return nil
}

// 计划任务串行处理证书；待DNS验证的订单保留，避免周期触发重复申请。
func (m *Module) RenewDue(ctx context.Context, days int, log *task.Log) (any, error) {
	if err := m.reconcileOrders(); err != nil {
		return nil, err
	}
	var rows []store.Certificate
	if err := m.db.Where("source = ? AND (not_after <= ? OR id IN (SELECT renewal_id FROM certificate_orders WHERE auto_deploy = 1 AND status = 'failed' AND certificate_id != ''))", "letsencrypt", time.Now().UTC().Add(time.Duration(days)*24*time.Hour)).Order("not_after,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := task.BatchResult{Results: []task.ItemResult{}}
	for _, cert := range rows {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		message, err := func() (string, error) {
			release, err := m.tasks.Acquire("certificate:" + cert.ID)
			if err != nil {
				return "", err
			}
			defer release()
			return m.renewCertificate(ctx, cert, days, false, log)
		}()
		item := task.ItemResult{Target: cert.ID, Msg: message}
		if err != nil {
			item.Code = response.System
			item.Msg = response.Summary(err.Error())
			result.FailedCount++
		} else {
			result.SuccessCount++
		}
		log.Line(cert.ID + "：" + item.Msg)
		result.Results = append(result.Results, item)
	}
	if result.FailedCount > 0 {
		return result, errors.New("部分证书续签或部署失败，请查看逐项结果")
	}
	if len(rows) == 0 {
		log.Line("没有进入续签窗口的证书")
	}
	return result, nil
}

// RenewPluginCertificate 续签指定 ACME 证书。调用方持有证书任务锁。
func (m *Module) RenewPluginCertificate(ctx context.Context, id string, log *task.Log) (string, error) {
	if err := m.reconcileOrders(); err != nil {
		return "", err
	}
	var cert store.Certificate
	if err := m.db.WithContext(ctx).Take(&cert, "id = ? AND source = ?", id, "letsencrypt").Error; err != nil {
		return "", err
	}
	return m.renewCertificate(ctx, cert, 0, true, log)
}

func (m *Module) renewCertificate(ctx context.Context, cert store.Certificate, days int, force bool, log *task.Log) (string, error) {
	var latest store.CertificateOrder
	err := m.db.Where("renewal_id = ?", cert.ID).Order("created_at DESC,id DESC").First(&latest).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}
	if err == nil && (latest.Status == "creating" || latest.Status == "pending" || latest.Status == "issuing") {
		return "已有续签申请，等待任务或 DNS 验证完成", nil
	}
	var order store.CertificateOrder
	provider := ""
	if latest.AutoDeploy && latest.Status == "failed" && latest.CertificateID != "" {
		order = latest
	} else {
		if !force && !cert.NotAfter.Before(time.Now().Add(time.Duration(days)*24*time.Hour)) {
			return "证书尚未进入续签窗口", nil
		}
		var prior store.CertificateOrder
		if err = m.db.Where("certificate_id = ?", cert.ID).Order("created_at DESC,id DESC").First(&prior).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return "", err
		}
		if prior.ID != "" {
			provider, err = m.dnsProvider(ctx, prior.ID)
			if err != nil {
				return "", err
			}
		}
		if prior.SiteID == "" {
			if err = m.db.Model(&store.CertificateOrder{}).Select("site_id").Where("certificate_id = ? AND site_id != ''", cert.ID).Order("created_at DESC,id DESC").Limit(1).Scan(&prior.SiteID).Error; err != nil {
				return "", err
			}
		}
		now := time.Now().UTC()
		order = store.CertificateOrder{ID: store.ID(), Domains: cert.Domains, Email: cert.Email, Method: cert.ValidationMethod, Algorithm: certificateAlgorithm(cert), SiteID: prior.SiteID, RenewalID: cert.ID, AutoDeploy: true, Status: "creating", Challenges: "[]", TaskID: log.TaskID(), CreatedAt: now, UpdatedAt: now}
		if err = m.createOrderRecord(order, provider); err != nil {
			return "", err
		}
	}
	issueCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	if order.CertificateID != "" {
		err = m.deployIssued(issueCtx, order, log)
	} else {
		err = m.issue(issueCtx, order.ID, false, log)
	}
	m.reportRenewal(order, log.TaskID(), err)
	if err != nil {
		_ = m.db.Model(&store.CertificateOrder{}).Where("id = ?", order.ID).Updates(map[string]any{"status": "failed", "error": response.Summary(err.Error()), "updated_at": time.Now().UTC()}).Error
		return "", err
	}
	var latestOrder store.CertificateOrder
	if err := m.db.WithContext(ctx).Select("status").Take(&latestOrder, "id = ?", order.ID).Error; err != nil {
		return "", err
	}
	if latestOrder.Status == "pending" {
		return "续签申请已生成，请到免费证书页完成 DNS 验证", nil
	}
	return "续签并部署完成", nil
}
