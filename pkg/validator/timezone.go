package validator

import (
	"errors"
	"time"

	// The zone database is compiled in, so a zone name validates the same
	// on every machine, with or without tzdata installed.
	_ "time/tzdata"
)

// TimezoneMaxLength bounds a zone name; users.timezone is VARCHAR(64).
const TimezoneMaxLength = 64

var ErrInvalidTimezone = errors.New("timezone must be an IANA time zone name, e.g. Asia/Jakarta")

// ValidateTimezone accepts IANA time zone names such as "Asia/Jakarta",
// "America/New_York" or "UTC". "Local" is refused: it means whatever zone
// the server runs in.
func ValidateTimezone(name string) error {
	if name == "" || name == "Local" || len(name) > TimezoneMaxLength {
		return ErrInvalidTimezone
	}
	if _, err := time.LoadLocation(name); err != nil {
		return ErrInvalidTimezone
	}
	return nil
}
