package service

import (
	"strings"
	"testing"

	userDomain "timebox-be/internal/modules/core/user/domain"
	"timebox-be/pkg/validator"
)

func TestDisplayName(t *testing.T) {
	full, blank := "Ada Lovelace", "  "
	tests := []struct {
		user userDomain.User
		want string
	}{
		{userDomain.User{Username: "ada", FullName: &full}, "Ada Lovelace"},
		{userDomain.User{Username: "ada", FullName: &blank}, "ada"},
		{userDomain.User{Username: "ada"}, "ada"},
	}
	for _, tt := range tests {
		if got := displayName(&tt.user); got != tt.want {
			t.Errorf("displayName() = %q, want %q", got, tt.want)
		}
	}
}

func TestGetDeviceInfoFromUserAgent(t *testing.T) {
	tests := []struct {
		name, ua, browser, os, kind string
	}{
		{"Chrome on Windows",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36",
			"Chrome", "Windows", "desktop"},
		{"Edge on Windows",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0",
			"Edge", "Windows", "desktop"},
		{"Safari on macOS",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Safari/605.1.15",
			"Safari", "macOS", "desktop"},
		{"Safari on iPhone",
			"Mozilla/5.0 (iPhone; CPU iPhone OS 17_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.6 Mobile/15E148 Safari/604.1",
			"Safari", "iOS", "mobile"},
		{"Chrome on Android",
			"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36",
			"Chrome", "Android", "mobile"},
		{"Firefox on Linux",
			"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
			"Firefox", "Linux", "desktop"},
		{"Timebox Desktop on macOS",
			"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) timebox/0.2.0 Chrome/140.0.0.0 Electron/43.7.3 Safari/537.36",
			"Timebox Desktop", "macOS", "desktop"},
		{"Timebox Desktop on Windows",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) timebox/0.2.0 Chrome/140.0.0.0 Electron/43.7.3 Safari/537.36",
			"Timebox Desktop", "Windows", "desktop"},
		{"empty", "", "Unknown", "Unknown", "desktop"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetDeviceInfoFromUserAgent(tt.ua)
			if got.Browser != tt.browser || got.OS != tt.os || got.Type != tt.kind {
				t.Errorf("got %s / %s / %s, want %s / %s / %s", got.Browser, got.OS, got.Type, tt.browser, tt.os, tt.kind)
			}
		})
	}
}

func TestSanitizeUsername_PassesValidator(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{"John.Doe", "john.doe"},
		{"_john", "john"},
		{"..a.b", "a.b"},
		{"jo", ""},
		{"x+tag", "xtag"},
		{strings.Repeat("a", 70), strings.Repeat("a", 50)},
	}

	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := sanitizeUsername(tt.raw)
			if got != tt.want {
				t.Fatalf("sanitizeUsername(%q) = %q, want %q", tt.raw, got, tt.want)
			}
			if got != "" {
				if err := validator.ValidateUsername(got); err != nil {
					t.Errorf("sanitizeUsername(%q) = %q, rejected by ValidateUsername: %v", tt.raw, got, err)
				}
			}
		})
	}
}
