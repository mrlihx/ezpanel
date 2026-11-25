package website

import (
	"strings"
	"testing"
)

func TestValidateRewriteBaotaStyle(t *testing.T) {
	epayRule := `location / {
  if (!-e $request_filename) {
    rewrite ^/([a-zA-Z0-9\-\_]+).html$ /index.php?mod=$1 last;
  }
  rewrite ^/pay/(.*)$ /pay.php?s=$1 last;
  rewrite ^/api/v1(?!/index\.php)(.*)$ /api/v1/index.php last;
  rewrite ^/api/(.*)$ /api.php?s=$1 last;
  rewrite ^/doc/([a-zA-Z0-9\-\_]+).html$ /index.php?doc=$1 last;
}
location ^~ /plugins {
  deny all;
}
location ^~ /includes {
  deny all;
}
location ^~ /log/ {
  deny all;
}`
	if err := validateRewrite(epayRule); err != nil {
		t.Fatalf("expected Baota multi-location rewrite to pass, got error: %v", err)
	}
	if !hasRootLocation(epayRule) || !hasAnyLocation(epayRule) {
		t.Fatal("expected epayRule to detect root and any location")
	}

	subOnly := `location ^~ /plugins {
  deny all;
}`
	if err := validateRewrite(subOnly); err != nil {
		t.Fatalf("expected sub-location rule to pass, got error: %v", err)
	}
	if hasRootLocation(subOnly) || !hasAnyLocation(subOnly) {
		t.Fatal("expected subOnly to have hasRootLocation=false, hasAnyLocation=true")
	}

	bare := `if (!-e $request_filename) {
    rewrite ^(.*)$ /index.php?s=$1 last;
}`
	if err := validateRewrite(bare); err != nil {
		t.Fatalf("expected bare rewrite to pass, got error: %v", err)
	}
	if hasRootLocation(bare) || hasAnyLocation(bare) {
		t.Fatal("expected bare rule to have no location")
	}

	for _, bad := range []string{
		"} server { listen 80; }",
		"include /etc/passwd;",
		"server { listen 80; }",
		"location / { deny all;",
	} {
		if err := validateRewrite(bad); err == nil {
			t.Fatalf("expected invalid rule %q to fail", bad)
		}
	}

	if err := validateRewrite(strings.Repeat(" ", 10)); err != nil {
		t.Fatalf("expected empty rule to pass, got %v", err)
	}
}
