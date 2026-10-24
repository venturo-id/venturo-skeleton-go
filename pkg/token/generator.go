package token

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
)

// GenerateSecureToken returns length random bytes, hex encoded.
func GenerateSecureToken(length int) (string, error) {
	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate secure token: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// Hash returns the hex SHA-256 of a high-entropy secret (refresh token,
// API key secret, email link token) for storage and lookup. Never use it
// for passwords: those are guessable and need bcrypt.
func Hash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// GenerateCode returns digits random decimal digits, for a one-time code
// a person reads from an email and types in. Leading zeros are kept.
func GenerateCode(digits int) (string, error) {
	limit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(digits)), nil)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return "", fmt.Errorf("failed to generate code: %w", err)
	}
	return fmt.Sprintf("%0*d", digits, n), nil
}

// HashCode returns the hex HMAC-SHA256 of a one-time code for storage.
// A code has too few digits for a plain hash — every value can be tried
// offline — so it is keyed with a server secret, and bound to id (the row
// it is stored in) so equal codes of different rows never hash alike.
func HashCode(key []byte, id, code string) string {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id))
	mac.Write([]byte{0})
	mac.Write([]byte(code))
	return hex.EncodeToString(mac.Sum(nil))
}

// EqualHash compares two hashes in constant time.
func EqualHash(a, b string) bool {
	return hmac.Equal([]byte(a), []byte(b))
}
