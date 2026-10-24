package database

import (
	"testing"

	"timebox-be/internal/config"
)

func TestParseConfig_SessionSettings(t *testing.T) {
	poolConfig, err := ParseConfig(config.DatabaseConfig{
		Host: "db.internal", Port: "5432", User: "app", Password: "secret",
		DBName: "timebox", SSLMode: "disable", MaxConns: 25,
	})
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}

	want := map[string]string{
		"application_name":                    "timebox-api",
		"statement_timeout":                   "30000",
		"idle_in_transaction_session_timeout": "60000",
		"timezone":                            "UTC", // from the DSN, kept
	}
	params := poolConfig.ConnConfig.RuntimeParams
	for key, value := range want {
		if params[key] != value {
			t.Errorf("RuntimeParams[%q] = %q, want %q", key, params[key], value)
		}
	}
	if poolConfig.MaxConns != 25 {
		t.Errorf("MaxConns = %d, want 25", poolConfig.MaxConns)
	}
}
