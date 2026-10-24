package logger

import (
	"bytes"
	"os"
	"testing"
)

// ginzap logs every request with its path as the message: the same
// message hundreds of times a second must all be written.
func TestInitialize_ProductionKeepsEveryEntry(t *testing.T) {
	t.Setenv("DISCORD_ALERT_WEBHOOK", "")

	// zap opens "stderr" when the logger is built.
	out, err := os.CreateTemp(t.TempDir(), "log")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	stderr := os.Stderr
	os.Stderr = out
	defer func() { os.Stderr = stderr }()

	previous := Log
	defer func() { Log = previous }()
	if err := Initialize("production"); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	const n = 500
	for range n {
		Info("/core/v1/users")
	}
	Sync()

	written, err := os.ReadFile(out.Name())
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got := bytes.Count(written, []byte("\n")); got != n {
		t.Errorf("wrote %d entries, want %d", got, n)
	}
}
