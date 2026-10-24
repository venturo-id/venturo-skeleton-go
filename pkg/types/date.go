package types

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// Date represents a date without time information (YYYY-MM-DD format)
// It's designed to work with PostgreSQL DATE columns
type Date struct {
	time.Time
}

const DateFormat = "2006-01-02"

// UnmarshalJSON implements json.Unmarshaler to parse date strings in YYYY-MM-DD format.
// null and "" give the zero Date, matching what MarshalJSON writes for it.
func (d *Date) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		d.Time = time.Time{}
		return nil
	}

	// Remove quotes from JSON string
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return fmt.Errorf("invalid date format: expected quoted string")
	}
	s = s[1 : len(s)-1]

	// Handle empty string
	if s == "" {
		d.Time = time.Time{}
		return nil
	}

	// Parse date in YYYY-MM-DD format
	parsed, err := time.Parse(DateFormat, s)
	if err != nil {
		return fmt.Errorf("invalid date format: expected YYYY-MM-DD, got %s", s)
	}

	d.Time = parsed
	return nil
}

// MarshalJSON implements json.Marshaler to output date strings in YYYY-MM-DD format
func (d Date) MarshalJSON() ([]byte, error) {
	if d.Time.IsZero() {
		return []byte("null"), nil
	}
	return []byte(fmt.Sprintf(`"%s"`, d.Time.Format(DateFormat))), nil
}

// Scan implements sql.Scanner to read DATE values from the database
func (d *Date) Scan(value interface{}) error {
	if value == nil {
		d.Time = time.Time{}
		return nil
	}

	switch v := value.(type) {
	case time.Time:
		d.Time = v
		return nil
	case string:
		parsed, err := time.Parse(DateFormat, v)
		if err != nil {
			return fmt.Errorf("failed to scan date: %w", err)
		}
		d.Time = parsed
		return nil
	default:
		return fmt.Errorf("unsupported scan type for Date: %T", value)
	}
}

// Value implements driver.Valuer to write DATE values to the database
func (d Date) Value() (driver.Value, error) {
	if d.Time.IsZero() {
		return nil, nil
	}
	// Return only the date part for PostgreSQL DATE columns
	return d.Time.Format(DateFormat), nil
}

// NewDate creates a new Date from year, month, day
func NewDate(year int, month time.Month, day int) Date {
	return Date{Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC)}
}

// ParseDate parses a date string in YYYY-MM-DD format
func ParseDate(s string) (Date, error) {
	parsed, err := time.Parse(DateFormat, s)
	if err != nil {
		return Date{}, fmt.Errorf("invalid date format: expected YYYY-MM-DD, got %s", s)
	}
	return Date{Time: parsed}, nil
}

// String returns the date in YYYY-MM-DD format
func (d Date) String() string {
	if d.Time.IsZero() {
		return ""
	}
	return d.Time.Format(DateFormat)
}
