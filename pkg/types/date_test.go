package types

import (
	"encoding/json"
	"testing"
	"time"
)

func TestDate_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string
	}{
		{
			name:    "valid date",
			input:   `"2025-10-31"`,
			wantErr: false,
			want:    "2025-10-31",
		},
		{
			name:    "invalid format - with time",
			input:   `"2025-10-31T12:00:00Z"`,
			wantErr: true,
		},
		{
			name:    "invalid format - wrong separator",
			input:   `"2025/10/31"`,
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   `""`,
			wantErr: false,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var d Date
			err := json.Unmarshal([]byte(tt.input), &d)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && d.String() != tt.want {
				t.Errorf("UnmarshalJSON() got = %v, want %v", d.String(), tt.want)
			}
		})
	}
}

func TestDate_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		date    Date
		want    string
		wantErr bool
	}{
		{
			name: "valid date",
			date: Date{Time: time.Date(2025, 10, 31, 0, 0, 0, 0, time.UTC)},
			want: `"2025-10-31"`,
		},
		{
			name: "zero date",
			date: Date{Time: time.Time{}},
			want: `null`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.date)
			if (err != nil) != tt.wantErr {
				t.Errorf("MarshalJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if string(got) != tt.want {
				t.Errorf("MarshalJSON() got = %v, want %v", string(got), tt.want)
			}
		})
	}
}

func TestDate_RoundTrip(t *testing.T) {
	// Test that we can marshal and unmarshal correctly
	type TestStruct struct {
		CloseDate Date `json:"close_date"`
	}

	input := `{"close_date":"2025-10-31"}`
	var ts TestStruct
	err := json.Unmarshal([]byte(input), &ts)
	if err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if ts.CloseDate.String() != "2025-10-31" {
		t.Errorf("Expected date 2025-10-31, got %v", ts.CloseDate.String())
	}

	// Marshal it back
	output, err := json.Marshal(ts)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}

	if string(output) != input {
		t.Errorf("Round trip failed: got %v, want %v", string(output), input)
	}
}

func TestDate_RoundTripZero(t *testing.T) {
	// A zero Date marshals to null; null must read back as a zero Date,
	// or a client cannot send back what the API gave it.
	type TestStruct struct {
		DueDate Date `json:"due_date"`
	}

	output, err := json.Marshal(TestStruct{})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if string(output) != `{"due_date":null}` {
		t.Fatalf("Marshal = %s, want {\"due_date\":null}", output)
	}

	ts := TestStruct{DueDate: NewDate(2026, time.January, 2)}
	if err := json.Unmarshal(output, &ts); err != nil {
		t.Fatalf("Unmarshal(null) failed: %v", err)
	}
	if !ts.DueDate.IsZero() {
		t.Errorf("Unmarshal(null) = %v, want the zero Date", ts.DueDate)
	}
}

func TestNewDate(t *testing.T) {
	d := NewDate(2025, time.October, 31)
	if d.String() != "2025-10-31" {
		t.Errorf("NewDate() got = %v, want 2025-10-31", d.String())
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "valid date",
			input:   "2025-10-31",
			want:    "2025-10-31",
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "31-10-2025",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseDate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got.String() != tt.want {
				t.Errorf("ParseDate() got = %v, want %v", got.String(), tt.want)
			}
		})
	}
}
