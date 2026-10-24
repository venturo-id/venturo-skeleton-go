package crypto

import "testing"

func TestComparePassword(t *testing.T) {
	hash, err := HashPassword("Timebox2026")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	if !ComparePassword(hash, "Timebox2026") {
		t.Error("right password refused")
	}
	if ComparePassword(hash, "Timebox2027") {
		t.Error("wrong password accepted")
	}
	if ComparePassword("", "") || ComparePassword("", "Timebox2026") {
		t.Error("an account without a password matched")
	}
}
