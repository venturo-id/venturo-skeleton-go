package validator

import "testing"

func TestValidateTimezone(t *testing.T) {
	valid := []string{"Asia/Jakarta", "Asia/Makassar", "Asia/Jayapura", "America/New_York", "Europe/London", "UTC"}
	for _, name := range valid {
		if err := ValidateTimezone(name); err != nil {
			t.Errorf("ValidateTimezone(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{"", "Local", "Asia/Bandung", "WIB", "+07:00", "../../etc/passwd", "Asia/Jakarta "}
	for _, name := range invalid {
		if err := ValidateTimezone(name); err == nil {
			t.Errorf("ValidateTimezone(%q) = nil, want an error", name)
		}
	}
}
