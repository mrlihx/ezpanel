package certificate

import (
	"context"
	"errors"
	"net"
	"slices"
	"time"

	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"gorm.io/gorm"
)

type DNSRecord struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type DNSProviders interface {
	DNSProviderReady(id string) bool
	ChangeDNSRecord(ctx context.Context, pluginID, action string, record DNSRecord) error
}

func (m *Module) SetDNSProviders(providers DNSProviders) { m.dnsProviders = providers }

func (m *Module) dnsProvider(ctx context.Context, orderID string) (string, error) {
	var id string
	err := m.db.WithContext(ctx).Table("certificate_order_dns_providers").Select("plugin_id").Where("order_id = ?", orderID).Scan(&id).Error
	return id, err
}

func (m *Module) latestDNSProvider(ctx context.Context, certificateID string) (string, error) {
	var id string
	err := m.db.WithContext(ctx).Table("certificate_orders AS o").
		Select("COALESCE(p.plugin_id, '')").
		Joins("LEFT JOIN certificate_order_dns_providers AS p ON p.order_id = o.id").
		Where("o.certificate_id = ?", certificateID).
		Order("o.created_at DESC, o.id DESC").Limit(1).Scan(&id).Error
	return id, err
}

func (m *Module) createOrderRecord(row store.CertificateOrder, pluginID string) error {
	return m.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		if pluginID != "" {
			return tx.Exec("INSERT INTO certificate_order_dns_providers (order_id, plugin_id) VALUES (?, ?)", row.ID, pluginID).Error
		}
		return nil
	})
}

func (m *Module) DNSProviderInUse(id string) error {
	var count int64
	err := m.db.Table("certificate_order_dns_providers AS p").Joins("JOIN certificate_orders AS o ON o.id = p.order_id").
		Where(`p.plugin_id = ? AND (o.status IN ? OR EXISTS (
			SELECT 1 FROM certificates c WHERE c.id = o.certificate_id AND c.source = 'letsencrypt'
			AND o.id = (SELECT latest.id FROM certificate_orders latest WHERE latest.certificate_id = c.id ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)
		))`, id, []string{"creating", "pending", "issuing"}).Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return errors.New("仍有 ACME 证书或申请使用该 DNS 插件")
	}
	return nil
}

func (m *Module) publishDNS(ctx context.Context, pluginID string, challenges []challenge, log *task.Log) (func(), error) {
	records := make([]DNSRecord, 0, len(challenges))
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		for _, record := range records {
			if err := m.dnsProviders.ChangeDNSRecord(ctx, pluginID, "remove", record); err != nil {
				log.Line("无法清理 DNS 验证记录 " + record.Name)
			}
		}
	}
	for _, item := range challenges {
		record := DNSRecord{Name: item.RecordName, Value: item.Value}
		records = append(records, record)
		if err := m.dnsProviders.ChangeDNSRecord(ctx, pluginID, "present", record); err != nil {
			return cleanup, err
		}
	}
	if len(records) == 0 {
		return cleanup, nil
	}
	log.Line("DNS 插件已提交 TXT 记录，等待解析生效")
	wait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for _, record := range records {
		for {
			values, err := net.DefaultResolver.LookupTXT(wait, record.Name+".")
			if err == nil && slices.Contains(values, record.Value) {
				break
			}
			select {
			case <-wait.Done():
				return cleanup, errors.New("DNS 验证记录尚未生效：" + record.Name)
			case <-tick.C:
			}
		}
	}
	return cleanup, nil
}
