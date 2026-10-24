package crypto

import "golang.org/x/crypto/bcrypt"

// HashPassword hashes a plain text password
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// ComparePassword compares a hashed password with a plain text password.
// An account without a password (external sign-in only) never matches,
// and takes as long to refuse as a wrong password.
func ComparePassword(hashedPassword, password string) bool {
	if hashedPassword == "" {
		SpendComparison(password)
		return false
	}
	err := bcrypt.CompareHashAndPassword([]byte(hashedPassword), []byte(password))
	return err == nil
}

// dummyHash is a bcrypt hash of a random value, at the default cost.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("timebox-dummy-password"), bcrypt.DefaultCost)

// SpendComparison does the work of one ComparePassword and discards the
// result. Call it when there is no account to check, so a sign-in for an
// unknown user takes as long as one with a wrong password and response
// times do not reveal which accounts exist.
func SpendComparison(password string) {
	_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}
