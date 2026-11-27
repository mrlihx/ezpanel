package certificate

import (
	"testing"
	"time"

	"ezpanel/internal/store"
)

func TestOrderDNSProviderTracksCurrentCertificate(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	m := &Module{db: db}
	const provider = "example-dns"
	cert := store.Certificate{ID: store.ID(), Fingerprint: store.ID(), Source: "letsencrypt"}
	if err := db.Create(&cert).Error; err != nil {
		t.Fatal(err)
	}
	order := store.CertificateOrder{ID: store.ID(), CertificateID: cert.ID, Status: "valid", Method: "dns-01", CreatedAt: time.Now().UTC().Add(-time.Hour)}
	if err := m.createOrderRecord(order, provider); err != nil {
		t.Fatal(err)
	}
	if id, err := m.dnsProvider(t.Context(), order.ID); err != nil || id != provider {
		t.Fatalf("DNS provider is %q, error %v", id, err)
	}
	if id, err := m.latestDNSProvider(t.Context(), cert.ID); err != nil || id != provider {
		t.Fatalf("latest DNS provider is %q, error %v", id, err)
	}
	if err := m.DNSProviderInUse(provider); err == nil {
		t.Fatal("provider for the current certificate must be protected")
	}
	renewal := store.CertificateOrder{ID: store.ID(), CertificateID: cert.ID, Status: "valid", Method: "dns-01"}
	if err := m.createOrderRecord(renewal, ""); err != nil {
		t.Fatal(err)
	}
	if id, err := m.dnsProvider(t.Context(), renewal.ID); err != nil || id != "" {
		t.Fatalf("manual DNS order resolved provider %q, error %v", id, err)
	}
	if id, err := m.latestDNSProvider(t.Context(), cert.ID); err != nil || id != "" {
		t.Fatalf("manual renewal must clear inherited provider, got %q, error %v", id, err)
	}
	if err := m.DNSProviderInUse(provider); err != nil {
		t.Fatalf("old providers must be removable after renewal changes provider: %v", err)
	}
}
