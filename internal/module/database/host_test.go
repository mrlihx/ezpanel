package database

import "testing"

func TestNormalizeAccessHost(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "localhost", false},
		{"localhost", "localhost", false},
		{"local", "localhost", false},
		{"%", "%", false},
		{"1.2.3.4", "1.2.3.4", false},
		{" 192.168.10.25 ", "192.168.10.25", false},
		{"192.168.1.55/24", "192.168.1.0/255.255.255.0", false},
		{"172.18.0.0/16", "172.18.0.0/255.255.0.0", false},
		{"172.18.12.34/255.255.0.0", "172.18.0.0/255.255.0.0", false},
		{"2001:db8::1", "2001:db8::1", false},
		{"172.18.0.0/0", "", true},
		{"172.18.0.0/0.0.0.0", "", true},
		{"172.18.0.0/255.0.255.0", "", true},
		{"not-an-ip", "", true},
		{"foo.example.com", "", true},
	}
	for _, tc := range cases {
		got, err := normalizeAccessHost(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("normalizeAccessHost(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Fatalf("normalizeAccessHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAccountAcceptsNetmaskHost(t *testing.T) {
	if !account("app_user", "172.18.0.0/255.255.0.0") {
		t.Fatal("expected account() to accept IPv4/netmask host")
	}
	if account("app_user", "172.18.0.0/0.0.0.0") {
		t.Fatal("expected account() to reject zero netmask")
	}
}
