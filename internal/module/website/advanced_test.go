package website

import (
	"slices"
	"testing"
)

func TestNormalizeTLSProtocols(t *testing.T) {
	got, err := normalizeTLSProtocols([]string{"TLSv1.3", "TLSv1.2", "TLSv1.3"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"TLSv1.2", "TLSv1.3"}) {
		t.Fatalf("got %v, want [TLSv1.2 TLSv1.3]", got)
	}
	got, err = normalizeTLSProtocols([]string{"TLSv1", "TLSv1.1", "TLSv1.2", "TLSv1.3"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 {
		t.Fatalf("got %v", got)
	}
	if _, err := normalizeTLSProtocols(nil); err == nil {
		t.Fatal("expected error for empty protocols")
	}
	if _, err := normalizeTLSProtocols([]string{"SSLv3"}); err == nil {
		t.Fatal("expected error for unsupported protocol SSLv3")
	}
}
