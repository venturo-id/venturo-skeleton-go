package email

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
	"time"
)

// envelope is one captured delivery: the SMTP envelope and the message
type envelope struct {
	from, to string
	msg      io.WriterTo
}

// sentEmail is a delivered message decoded back into plain values
type sentEmail struct {
	From    *mail.Address
	To      string
	Subject string
	Body    string // HTML with entities unescaped, so links read as plain URLs
}

// newTestService returns a service whose messages are captured instead of
// sent over SMTP
func newTestService(t *testing.T, cfg Config) (*SMTPEmailService, *[]envelope) {
	t.Helper()
	service, err := NewSMTPEmailService(cfg)
	if err != nil {
		t.Fatalf("NewSMTPEmailService() error = %v", err)
	}
	sent := &[]envelope{}
	service.send = func(from, to string, msg io.WriterTo) error {
		*sent = append(*sent, envelope{from: from, to: to, msg: msg})
		return nil
	}
	return service, sent
}

func decode(t *testing.T, e envelope) sentEmail {
	t.Helper()
	var raw bytes.Buffer
	if _, err := e.msg.WriteTo(&raw); err != nil {
		t.Fatalf("message.WriteTo() error = %v", err)
	}
	msg, err := mail.ReadMessage(&raw)
	if err != nil {
		t.Fatalf("mail.ReadMessage() error = %v", err)
	}

	from, err := mail.ParseAddress(msg.Header.Get("From"))
	if err != nil {
		t.Fatalf("From header %q: %v", msg.Header.Get("From"), err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if err != nil {
		t.Fatalf("Subject header: %v", err)
	}
	if got := msg.Header.Get("Content-Transfer-Encoding"); got != "quoted-printable" {
		t.Fatalf("Content-Transfer-Encoding = %q, want quoted-printable", got)
	}
	body, err := io.ReadAll(quotedprintable.NewReader(msg.Body))
	if err != nil {
		t.Fatalf("decoding body: %v", err)
	}

	return sentEmail{
		From:    from,
		To:      msg.Header.Get("To"),
		Subject: subject,
		Body:    html.UnescapeString(string(body)),
	}
}

func TestSMTPEmailService_Send(t *testing.T) {
	cfg := validConfig()
	cfg.NotificationsFromEmail = "notifications@example.com"
	service, sent := newTestService(t, cfg)
	const to, name = "ada@example.com", "Ada Lovelace"
	// Codes come from FromEmail, every other email from NotificationsFromEmail
	const codes, notices = "noreply@example.com", "notifications@example.com"

	tests := []struct {
		name        string
		send        func() error
		wantFrom    string
		wantSubject string
		wantBody    []string
	}{
		{
			name:        "verification code",
			send:        func() error { return service.SendVerificationCode(to, name, "482916", 15*time.Minute) },
			wantFrom:    codes,
			wantSubject: "482916 is your Timebox verification code",
			wantBody:    []string{`<div class="code">482916</div>`, "Thank you for signing up for Timebox!", "This code expires in 15 minutes."},
		},
		{
			name:        "password reset code",
			send:        func() error { return service.SendPasswordResetCode(to, name, "731045", time.Hour) },
			wantFrom:    codes,
			wantSubject: "731045 is your Timebox password reset code",
			wantBody:    []string{`<div class="code">731045</div>`, "This code expires in 1 hour."},
		},
		{
			name:        "password changed",
			send:        func() error { return service.SendPasswordChangedEmail(to, name) },
			wantFrom:    notices,
			wantSubject: "Password Changed Successfully - Timebox",
			wantBody:    []string{"your Timebox account password was recently changed"},
		},
		{
			name:        "account locked",
			send:        func() error { return service.SendAccountLockedEmail(to, name, 30*time.Minute) },
			wantFrom:    notices,
			wantSubject: "Account Security Alert - Timebox",
			wantBody:    []string{"Your account has been temporarily locked", "unlocked in 30 minutes", "The Timebox Security Team"},
		},
		{
			name:        "welcome",
			send:        func() error { return service.SendWelcomeEmail(to, name) },
			wantFrom:    notices,
			wantSubject: "Welcome to Timebox!",
			wantBody:    []string{"Welcome to Timebox!", `href="https://app.example.com"`},
		},
		{
			name:        "email change code",
			send:        func() error { return service.SendEmailChangeCode(to, name, "905127", 15*time.Minute) },
			wantFrom:    codes,
			wantSubject: "905127 is your code to confirm your new Timebox email",
			wantBody:    []string{`<div class="code">905127</div>`, "This code expires in 15 minutes."},
		},
		{
			name:        "email changed",
			send:        func() error { return service.SendEmailChangedEmail(to, name, "ada@new.example.com") },
			wantFrom:    notices,
			wantSubject: "Your Email Was Changed - Timebox",
			wantBody:    []string{"now uses a new email address", "ada@new.example.com"},
		},
		{
			name:        "account closed",
			send:        func() error { return service.SendAccountClosedEmail(to, name) },
			wantFrom:    notices,
			wantSubject: "Your Account Was Closed - Timebox",
			wantBody:    []string{"Your Timebox account has been closed"},
		},
		{
			name:        "google linked",
			send:        func() error { return service.SendGoogleLinkedEmail(to, name, "ada.lovelace@gmail.com") },
			wantFrom:    notices,
			wantSubject: "Google Account Linked - Timebox",
			wantBody:    []string{"The Google account <strong>ada.lovelace@gmail.com</strong> can now sign in to your account."},
		},
		{
			name:        "google unlinked",
			send:        func() error { return service.SendGoogleUnlinkedEmail(to, name, "ada.lovelace@gmail.com") },
			wantFrom:    notices,
			wantSubject: "Google Account Unlinked - Timebox",
			wantBody:    []string{"The Google account <strong>ada.lovelace@gmail.com</strong> can no longer sign in to your account."},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*sent = nil
			if err := tt.send(); err != nil {
				t.Fatalf("send error = %v", err)
			}
			if len(*sent) != 1 {
				t.Fatalf("sent %d messages, want 1", len(*sent))
			}
			if env := (*sent)[0]; env.from != tt.wantFrom || env.to != to {
				t.Errorf("envelope = %s -> %s, want %s -> %s", env.from, env.to, tt.wantFrom, to)
			}
			got := decode(t, (*sent)[0])

			if got.Subject != tt.wantSubject {
				t.Errorf("Subject = %q, want %q", got.Subject, tt.wantSubject)
			}
			if got.From.Name != "Timebox" || got.From.Address != tt.wantFrom {
				t.Errorf("From = %q <%s>, want \"Timebox\" <%s>", got.From.Name, got.From.Address, tt.wantFrom)
			}
			if got.To != to {
				t.Errorf("To = %q, want %q", got.To, to)
			}

			want := append([]string{
				"Hi Ada Lovelace,",
				fmt.Sprintf("© %d Timebox. All rights reserved.", time.Now().Year()),
				`href="mailto:help@example.com"`,
			}, tt.wantBody...)
			for _, s := range want {
				if !strings.Contains(got.Body, s) {
					t.Errorf("body does not contain %q", s)
				}
			}
		})
	}
}

func TestSMTPEmailService_CustomAppName(t *testing.T) {
	cfg := validConfig()
	cfg.AppName = "Acme Planner"
	service, sent := newTestService(t, cfg)

	if err := service.SendWelcomeEmail("ada@example.com", "Ada"); err != nil {
		t.Fatalf("SendWelcomeEmail() error = %v", err)
	}
	got := decode(t, (*sent)[0])

	if got.Subject != "Welcome to Acme Planner!" {
		t.Errorf("Subject = %q, want %q", got.Subject, "Welcome to Acme Planner!")
	}
	if got.From.Name != "Acme Planner" {
		t.Errorf("From name = %q, want %q", got.From.Name, "Acme Planner")
	}
	if strings.Contains(got.Body, DefaultAppName) {
		t.Errorf("body mentions %q although AppName is %q", DefaultAppName, cfg.AppName)
	}

	// The code subjects name the app too
	*sent = nil
	_ = service.SendVerificationCode("ada@example.com", "Ada", "482916", 15*time.Minute)
	_ = service.SendPasswordResetCode("ada@example.com", "Ada", "731045", 15*time.Minute)
	_ = service.SendEmailChangeCode("ada@example.com", "Ada", "905127", 15*time.Minute)
	wantSubjects := []string{
		"482916 is your Acme Planner verification code",
		"731045 is your Acme Planner password reset code",
		"905127 is your code to confirm your new Acme Planner email",
	}
	if len(*sent) != len(wantSubjects) {
		t.Fatalf("sent %d messages, want %d", len(*sent), len(wantSubjects))
	}
	for i, want := range wantSubjects {
		if got := decode(t, (*sent)[i]).Subject; got != want {
			t.Errorf("Subject = %q, want %q", got, want)
		}
	}
}

func TestSMTPEmailService_RejectsBadInput(t *testing.T) {
	service, sent := newTestService(t, validConfig())

	tests := []struct {
		name string
		send func() error
	}{
		{"empty recipient", func() error { return service.SendWelcomeEmail("", "Ada") }},
		{"invalid recipient", func() error { return service.SendWelcomeEmail("invalid-email", "Ada") }},
		{"recipient with header injection", func() error {
			return service.SendWelcomeEmail("ada@example.com\r\nBcc: eve@example.com", "Ada")
		}},
		{"empty verification code", func() error { return service.SendVerificationCode("ada@example.com", "Ada", "", time.Hour) }},
		{"empty reset code", func() error { return service.SendPasswordResetCode("ada@example.com", "Ada", "", time.Hour) }},
		{"empty email change code", func() error { return service.SendEmailChangeCode("ada@example.com", "Ada", "", time.Hour) }},
		{"verification without a duration", func() error {
			return service.SendVerificationCode("ada@example.com", "Ada", "482916", 0)
		}},
		{"reset without a duration", func() error {
			return service.SendPasswordResetCode("ada@example.com", "Ada", "482916", 0)
		}},
		{"email change without a duration", func() error {
			return service.SendEmailChangeCode("ada@example.com", "Ada", "482916", -time.Minute)
		}},
		{"lock without a duration", func() error { return service.SendAccountLockedEmail("ada@example.com", "Ada", 0) }},
		{"code to an invalid recipient", func() error {
			return service.SendVerificationCode("invalid-email", "Ada", "482916", time.Hour)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			*sent = nil
			if err := tt.send(); err == nil {
				t.Error("error = nil, want an error")
			}
			if len(*sent) != 0 {
				t.Errorf("sent %d messages, want none", len(*sent))
			}
		})
	}
}

func TestSMTPEmailService_SendFailure(t *testing.T) {
	service, _ := newTestService(t, validConfig())
	dialErr := errors.New("dial tcp: connection refused")
	service.send = func(string, string, io.WriterTo) error { return dialErr }

	err := service.SendWelcomeEmail("ada@example.com", "Ada")
	if !errors.Is(err, dialErr) {
		t.Errorf("error = %v, want it to wrap %v", err, dialErr)
	}
}

// A code email's subject carries the code, so neither it nor the
// recipient reaches the logs, whether the email went out or not.
func TestSMTPEmailService_CodeStaysOutOfLogs(t *testing.T) {
	logs := observeLogs(t)
	service, _ := newTestService(t, validConfig())
	const to, code = "ada@example.com", "482916"

	if err := service.SendVerificationCode(to, "Ada", code, 15*time.Minute); err != nil {
		t.Fatalf("SendVerificationCode() error = %v", err)
	}
	service.send = func(string, string, io.WriterTo) error { return errors.New("dial tcp: connection refused") }
	if err := service.SendPasswordResetCode(to, "Ada", code, 15*time.Minute); err == nil {
		t.Fatal("SendPasswordResetCode() error = nil, want the send failure")
	}

	entries := logs.All()
	if len(entries) != 2 {
		t.Fatalf("logged %d entries, want 2 (one sent, one failed)", len(entries))
	}
	// The template still says which email it was
	for i, want := range []string{"verification_code.html", "reset_password_code.html"} {
		if got := entries[i].ContextMap()["template"]; got != want {
			t.Errorf("entry %d template = %v, want %q", i, got, want)
		}
	}
	for _, e := range entries {
		line := e.Message + " " + fmt.Sprint(e.ContextMap())
		if strings.Contains(line, code) || strings.Contains(line, to) {
			t.Errorf("log entry leaks the code or the recipient: %s", line)
		}
	}
}
