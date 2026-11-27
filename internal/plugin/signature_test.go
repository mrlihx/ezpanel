package plugin

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/config"
)

func TestOfflinePluginSignature(t *testing.T) {
	m := Module{cfg: config.Config{InstallDir: t.TempDir()}}
	dir := filepath.Join(m.packages(), ".trusted-keys")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	const id = "demo-plugin"
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("original ZIP"))
	signature := ed25519.Sign(private, digest[:])
	if _, err := m.verifySignature(id, digest[:], signature); err == nil {
		t.Fatal("unknown publisher was accepted")
	}
	bytes, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".pub"), pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: bytes}), 0600); err != nil {
		t.Fatal(err)
	}
	if verified, err := m.verifySignature(id, digest[:], signature); !verified || err != nil {
		t.Fatalf("valid signature was rejected: %v", err)
	}
	if _, err := m.verifySignature(id, digest[:], nil); err == nil {
		t.Fatal("missing signature for registered key was accepted")
	}
	tampered := sha256.Sum256([]byte("different ZIP"))
	if _, err := m.verifySignature(id, tampered[:], signature); err == nil {
		t.Fatal("signature accepted for a changed ZIP")
	}
}
