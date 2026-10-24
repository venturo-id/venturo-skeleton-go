package token

import (
	"strings"
	"testing"
)

func TestGenerateCode(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		code, err := GenerateCode(6)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatalf("code = %q, want 6 digits", code)
		}
		seen[code] = true
	}
	if len(seen) < 190 {
		t.Errorf("only %d distinct codes in 200", len(seen))
	}
}

func TestHashCode(t *testing.T) {
	key := []byte("server-secret")
	a := HashCode(key, "row-1", "123456")
	if a != HashCode(key, "row-1", "123456") {
		t.Error("hash is not deterministic")
	}
	if a == HashCode(key, "row-2", "123456") {
		t.Error("the same code in another row hashes alike")
	}
	if a == HashCode([]byte("other-secret"), "row-1", "123456") {
		t.Error("the key does not change the hash")
	}
	if !EqualHash(a, HashCode(key, "row-1", "123456")) || EqualHash(a, HashCode(key, "row-1", "123457")) {
		t.Error("EqualHash disagrees with the hashes")
	}
}
