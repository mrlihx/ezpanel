package sysinfo

import "testing"

func TestCompareSemverAndUpdateCheck(t *testing.T) {
	if compareSemver("0.1.3", "0.1.2") <= 0 {
		t.Fatal("expected 0.1.3 > 0.1.2")
	}
	if compareSemver("0.1.2", "0.1.2") != 0 {
		t.Fatal("expected 0.1.2 == 0.1.2")
	}
	if compareSemver("0.1.1", "0.1.2") >= 0 {
		t.Fatal("expected 0.1.1 < 0.1.2")
	}
	if !isNewerPanelRelease("0.1.1", "0.1.2") {
		t.Fatal("expected 0.1.2 to be newer than 0.1.1")
	}
	if isNewerPanelRelease("0.1.2", "0.1.2") {
		t.Fatal("expected same version not to report update")
	}
	if isNewerPanelRelease("0.0.0-dev", "0.1.2") {
		t.Fatal("expected dev build not to report update")
	}
	if got := stripReleaseVersion("v0.1.2-r2"); got != "0.1.2" {
		t.Fatalf("stripReleaseVersion = %q, want 0.1.2", got)
	}
}

func TestExtractExpectedSHA256(t *testing.T) {
	sums := []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  ezpanel-linux-amd64\nbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  ezpanel-linux-arm64\n")
	got, err := extractExpectedSHA256(sums, "ezpanel-linux-arm64")
	if err != nil || got != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("got (%q, %v)", got, err)
	}
	if _, err := extractExpectedSHA256(sums, "missing"); err == nil {
		t.Fatal("expected error for missing artifact")
	}
}

func TestPanelRevisionOrdering(t *testing.T) {
	for _, tc := range []struct {
		current, latest string
		newer           bool
	}{
		{"0.1.2", "v0.1.2-r1", true},
		{"v0.1.2-r9", "v0.1.2-r10", true},
		{"v0.1.2-r10", "v0.1.2-r9", false},
		{"v0.1.2-r10", "v0.1.2", false},
		{"v0.1.2-r10", "v0.1.3", true},
	} {
		if got := isNewerPanelRelease(tc.current, tc.latest); got != tc.newer {
			t.Errorf("%s -> %s: got %v, want %v", tc.current, tc.latest, got, tc.newer)
		}
	}
}
