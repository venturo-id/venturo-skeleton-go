package mailer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"timebox-be/pkg/email"
)

// slowService records sends after a delay, failing when fail is set.
type slowService struct {
	email.NoOpEmailService
	mu    sync.Mutex
	sent  []string
	delay time.Duration
	fail  bool
}

func (s *slowService) SendWelcomeEmail(to, _ string) error {
	time.Sleep(s.delay)
	if s.fail {
		return errors.New("smtp down")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, to)
	return nil
}

func TestAsync_ReturnsAtOnceAndWaitDrains(t *testing.T) {
	inner := &slowService{delay: 50 * time.Millisecond}
	a := NewAsync(inner)

	start := time.Now()
	for i := 0; i < 20; i++ {
		if err := a.SendWelcomeEmail("ada@example.com", "Ada"); err != nil {
			t.Fatalf("SendWelcomeEmail() error = %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("sending blocked the caller for %s", elapsed)
	}

	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if len(inner.sent) != 20 {
		t.Errorf("sent %d emails, want 20", len(inner.sent))
	}
}

func TestAsync_WaitHonoursContext(t *testing.T) {
	a := NewAsync(&slowService{delay: time.Second})
	_ = a.SendWelcomeEmail("ada@example.com", "Ada")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := a.Wait(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Wait() = %v, want DeadlineExceeded", err)
	}
}

func TestAsync_FailureIsNotReturned(t *testing.T) {
	a := NewAsync(&slowService{fail: true})
	if err := a.SendWelcomeEmail("ada@example.com", "Ada"); err != nil {
		t.Errorf("SendWelcomeEmail() = %v, want nil: failures are logged in the background", err)
	}
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

// blockedService holds every send until release is closed, like a stalled
// SMTP server.
type blockedService struct {
	email.NoOpEmailService
	release chan struct{}
	sent    atomic.Int64
}

func (s *blockedService) SendWelcomeEmail(string, string) error {
	<-s.release
	s.sent.Add(1)
	return nil
}

func TestAsync_DropsWhenFull(t *testing.T) {
	inner := &blockedService{release: make(chan struct{})}
	a := NewAsync(inner)

	start := time.Now()
	for i := 0; i < maxPending+50; i++ {
		_ = a.SendWelcomeEmail("ada@example.com", "Ada")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("a full queue blocked the caller for %s", elapsed)
	}
	if got := a.dropped.Load(); got != 50 {
		t.Errorf("dropped = %d, want 50", got)
	}

	close(inner.release)
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got := inner.sent.Load(); got != maxPending {
		t.Errorf("sent %d emails, want %d", got, maxPending)
	}

	// Room again once the queue drained
	_ = a.SendWelcomeEmail("ada@example.com", "Ada")
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
	if got := inner.sent.Load(); got != maxPending+1 {
		t.Errorf("sent %d emails, want %d", got, maxPending+1)
	}
}

// Sends may start while Wait runs (requests cut short at shutdown).
func TestAsync_SendDuringWait(t *testing.T) {
	a := NewAsync(&slowService{delay: time.Millisecond})

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Go(func() {
			for j := 0; j < 50; j++ {
				_ = a.SendWelcomeEmail("ada@example.com", "Ada")
				_ = a.Wait(context.Background())
			}
		})
	}
	wg.Wait()
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

// recorder records every email it is asked to send, with its arguments.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

var _ email.EmailService = (*recorder)(nil)

func (r *recorder) record(method string, args ...any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, fmt.Sprintf("%s%v", method, args))
	return nil
}

func (r *recorder) SendVerificationCode(to, name, code string, validFor time.Duration) error {
	return r.record("SendVerificationCode", to, name, code, validFor)
}

func (r *recorder) SendPasswordResetCode(to, name, code string, validFor time.Duration) error {
	return r.record("SendPasswordResetCode", to, name, code, validFor)
}

func (r *recorder) SendEmailChangeCode(to, name, code string, validFor time.Duration) error {
	return r.record("SendEmailChangeCode", to, name, code, validFor)
}

func (r *recorder) SendWelcomeEmail(to, name string) error {
	return r.record("SendWelcomeEmail", to, name)
}

func (r *recorder) SendAccountLockedEmail(to, name string, lockedFor time.Duration) error {
	return r.record("SendAccountLockedEmail", to, name, lockedFor)
}

func (r *recorder) SendPasswordChangedEmail(to, name string) error {
	return r.record("SendPasswordChangedEmail", to, name)
}

func (r *recorder) SendEmailChangedEmail(to, name, newEmail string) error {
	return r.record("SendEmailChangedEmail", to, name, newEmail)
}

func (r *recorder) SendAccountClosedEmail(to, name string) error {
	return r.record("SendAccountClosedEmail", to, name)
}

func (r *recorder) SendGoogleLinkedEmail(to, name, googleEmail string) error {
	return r.record("SendGoogleLinkedEmail", to, name, googleEmail)
}

func (r *recorder) SendGoogleUnlinkedEmail(to, name, googleEmail string) error {
	return r.record("SendGoogleUnlinkedEmail", to, name, googleEmail)
}

// Every email reaches the inner service's matching method, arguments intact.
func TestAsync_ForwardsEveryEmail(t *testing.T) {
	inner := &recorder{}
	a := NewAsync(inner)

	_ = a.SendVerificationCode("ada@example.com", "Ada", "482916", 15*time.Minute)
	_ = a.SendPasswordResetCode("ada@example.com", "Ada", "731045", 15*time.Minute)
	_ = a.SendEmailChangeCode("ada@new.example.com", "Ada", "905127", 15*time.Minute)
	_ = a.SendWelcomeEmail("ada@example.com", "Ada")
	_ = a.SendAccountLockedEmail("ada@example.com", "Ada", 30*time.Minute)
	_ = a.SendPasswordChangedEmail("ada@example.com", "Ada")
	_ = a.SendEmailChangedEmail("ada@example.com", "Ada", "ada@new.example.com")
	_ = a.SendAccountClosedEmail("ada@example.com", "Ada")
	_ = a.SendGoogleLinkedEmail("ada@example.com", "Ada", "ada.lovelace@gmail.com")
	_ = a.SendGoogleUnlinkedEmail("ada@example.com", "Ada", "ada.lovelace@gmail.com")
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	want := []string{
		"SendVerificationCode[ada@example.com Ada 482916 15m0s]",
		"SendPasswordResetCode[ada@example.com Ada 731045 15m0s]",
		"SendEmailChangeCode[ada@new.example.com Ada 905127 15m0s]",
		"SendWelcomeEmail[ada@example.com Ada]",
		"SendAccountLockedEmail[ada@example.com Ada 30m0s]",
		"SendPasswordChangedEmail[ada@example.com Ada]",
		"SendEmailChangedEmail[ada@example.com Ada ada@new.example.com]",
		"SendAccountClosedEmail[ada@example.com Ada]",
		"SendGoogleLinkedEmail[ada@example.com Ada ada.lovelace@gmail.com]",
		"SendGoogleUnlinkedEmail[ada@example.com Ada ada.lovelace@gmail.com]",
	}
	// Sends run concurrently, so they finish in any order
	got := slices.Sorted(slices.Values(inner.calls))
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("inner service got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
