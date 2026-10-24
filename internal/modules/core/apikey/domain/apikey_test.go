package domain

import "testing"

func TestIsIPAllowed(t *testing.T) {
	tests := []struct {
		name      string
		whitelist []string
		ip        string
		want      bool
	}{
		{"no whitelist allows everyone", nil, "198.51.100.1", true},
		{"exact IP", []string{"198.51.100.1"}, "198.51.100.1", true},
		{"other IP", []string{"198.51.100.1"}, "198.51.100.2", false},
		{"inside CIDR", []string{"10.0.0.0/8"}, "10.20.30.40", true},
		{"outside CIDR", []string{"10.0.0.0/8"}, "11.0.0.1", false},
		{"IPv6 CIDR", []string{"2001:db8::/32"}, "2001:db8::1", true},
		{"unparseable client IP", []string{"10.0.0.0/8"}, "not-an-ip", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := APIKey{IPWhitelist: tt.whitelist}
			if got := key.IsIPAllowed(tt.ip); got != tt.want {
				t.Errorf("IsIPAllowed(%q) = %v, want %v", tt.ip, got, tt.want)
			}
		})
	}
}
