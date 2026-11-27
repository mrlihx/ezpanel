package plugin

import (
	"context"
	"errors"
	"time"

	"ezpanel/internal/module/certificate"
)

const dnsCallback = "_panel/dns"

func (m *Module) DNSProviderReady(id string) bool {
	_, manifest, err := m.readyPlugin(id)
	return err == nil && manifest.DNSProvider != nil && m.checkPermission(id, manifest, "acme.dns") == nil
}

func (m *Module) ChangeDNSRecord(ctx context.Context, id, action string, record certificate.DNSRecord) error {
	run, manifest, err := m.readyPlugin(id)
	if err != nil || manifest.DNSProvider == nil {
		return errors.New("DNS 验证插件未运行")
	}
	if err := m.checkPermission(id, manifest, "acme.dns"); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	_, err = invokePlugin(ctx, run, id, dnsCallback, map[string]string{"action": action, "name": record.Name, "value": record.Value})
	return err
}
