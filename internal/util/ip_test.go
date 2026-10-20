package util

import "testing"

func TestTruncateIP(t *testing.T) {
	tests := []struct {
		ip   string
		want string
	}{
		{"203.0.113.77", "203.0.113.0"},
		{"203.0.113.0", "203.0.113.0"},
		{"8.8.8.8", "8.8.8.0"},
		{"255.255.255.255", "255.255.255.0"},
		// IPv4-mapped IPv6 is treated as IPv4
		{"::ffff:203.0.113.77", "203.0.113.0"},
		{"2001:db8:1234:5678:9abc:def0:1234:5678", "2001:db8:1234::"},
		{"2001:db8:12ff:ffff::1", "2001:db8:12ff::"},
		{"2001:db8::1", "2001:db8::"},
		{"::1", "::"},
		{"fe80::1%eth0", "fe80::"},
		// not an IP: never echo the input back
		{"", ""},
		{"203.0.113.77:443", ""},
		{"203.0.113.77, 10.0.0.1", ""},
		{"not-an-ip", ""},
	}
	for _, tt := range tests {
		if got := TruncateIP(tt.ip); got != tt.want {
			t.Errorf("TruncateIP(%q) = %q, want %q", tt.ip, got, tt.want)
		}
	}
}
