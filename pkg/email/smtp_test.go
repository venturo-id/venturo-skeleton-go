package email

import (
	"strings"
	"testing"
)

const testPassword = "smtp-test-password"

func validConfig() Config {
	return Config{
		Host:         "smtp.example.com",
		Port:         587,
		Username:     "smtp-user",
		Password:     testPassword,
		FromEmail:    "noreply@example.com",
		FrontendURL:  "https://app.example.com",
		SupportEmail: "help@example.com",
	}
}

// mailpitConfig is a local Mailpit: no credentials, no TLS
func mailpitConfig() Config {
	return Config{
		Host:        "localhost",
		Port:        1025,
		FromEmail:   "noreply@example.com",
		FrontendURL: "http://localhost:3000",
	}
}

func TestConfigEnabled(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
		want   bool
	}{
		{name: "complete", mutate: func(*Config) {}, want: true},
		{name: "without credentials", mutate: func(c *Config) { c.Username, c.Password = "", "" }, want: true},
		{name: "without host", mutate: func(c *Config) { c.Host = "" }},
		{name: "without from email", mutate: func(c *Config) { c.FromEmail = "" }},
		{name: "empty", mutate: func(c *Config) { *c = Config{} }},
	}
	for _, tt := range tests {
		cfg := validConfig()
		tt.mutate(&cfg)
		if got := cfg.Enabled(); got != tt.want {
			t.Errorf("%s: Enabled() = %v, want %v", tt.name, got, tt.want)
		}
	}
	if !mailpitConfig().Enabled() {
		t.Error("Enabled() = false for a local Mailpit")
	}
}

func TestNewSMTPEmailService_Validation(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string // substring; "" means success
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "port 0 uses default", mutate: func(c *Config) { c.Port = 0 }},
		{name: "implicit TLS port", mutate: func(c *Config) { c.Port = 465 }},
		{name: "no credentials", mutate: func(c *Config) { c.Username, c.Password = "", "" }},
		{name: "local mailpit", mutate: func(c *Config) { *c = mailpitConfig() }},
		{name: "missing host", mutate: func(c *Config) { c.Host = "" }, wantErr: "Host is required"},
		{name: "username without password", mutate: func(c *Config) { c.Password = "" }, wantErr: "Password is required when Username is set"},
		{name: "password without username", mutate: func(c *Config) { c.Username = "" }, wantErr: "Username is required when Password is set"},
		{name: "missing from email", mutate: func(c *Config) { c.FromEmail = "" }, wantErr: "FromEmail is required"},
		{name: "missing frontend url", mutate: func(c *Config) { c.FrontendURL = "" }, wantErr: "FrontendURL is required"},
		{name: "negative port", mutate: func(c *Config) { c.Port = -1 }, wantErr: "Port must be between"},
		{name: "port too large", mutate: func(c *Config) { c.Port = 70000 }, wantErr: "Port must be between"},
		{name: "invalid from email", mutate: func(c *Config) { c.FromEmail = "not-an-email" }, wantErr: "FromEmail must be a plain email address"},
		{name: "from email with name", mutate: func(c *Config) { c.FromEmail = "App <noreply@example.com>" }, wantErr: "FromEmail must be a plain email address"},
		{name: "notifications from email", mutate: func(c *Config) { c.NotificationsFromEmail = "notifications@example.com" }},
		{name: "invalid notifications from email", mutate: func(c *Config) { c.NotificationsFromEmail = "not-an-email" }, wantErr: "NotificationsFromEmail must be a plain email address"},
		{name: "notifications from email with name", mutate: func(c *Config) { c.NotificationsFromEmail = "App <notifications@example.com>" }, wantErr: "NotificationsFromEmail must be a plain email address"},
		{name: "relative frontend url", mutate: func(c *Config) { c.FrontendURL = "app.example.com" }, wantErr: "FrontendURL must be an absolute http(s) URL"},
		{name: "script frontend url", mutate: func(c *Config) { c.FrontendURL = "javascript:alert(1)" }, wantErr: "FrontendURL must be an absolute http(s) URL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			service, err := NewSMTPEmailService(cfg)
			if tt.wantErr == "" {
				if err != nil || service == nil {
					t.Fatalf("NewSMTPEmailService() = %v, %v; want a service", service, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("NewSMTPEmailService() error = nil, want %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("NewSMTPEmailService() error = %q, want it to contain %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), testPassword) {
				t.Errorf("NewSMTPEmailService() error leaks the password: %q", err)
			}
		})
	}
}

func TestNewSMTPEmailService_Defaults(t *testing.T) {
	cfg := validConfig()
	cfg.Port = 0

	service, err := NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"app name", service.cfg.AppName, "Timebox"},
		{"from name follows app name", service.cfg.FromName, "Timebox"},
		{"notifications sender follows from email", service.cfg.NotificationsFromEmail, "noreply@example.com"},
		{"port", service.cfg.Port, 587},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestNewSMTPEmailService_KeepsExplicitValues(t *testing.T) {
	cfg := validConfig()
	cfg.AppName = "Acme Planner"
	cfg.FromName = "Acme Notifications"
	cfg.NotificationsFromEmail = "notifications@example.com"
	cfg.Port = 2525

	service, err := NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}
	if service.cfg != cfg {
		t.Errorf("config = %+v, want it unchanged: %+v", service.cfg, cfg)
	}

	// FromName defaults to a custom AppName, not to "Timebox"
	cfg.FromName = ""
	service, err = NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}
	if service.cfg.FromName != "Acme Planner" {
		t.Errorf("FromName = %q, want %q", service.cfg.FromName, "Acme Planner")
	}
}
