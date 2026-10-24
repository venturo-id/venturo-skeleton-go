// Package email renders the transactional email templates and delivers them
// over SMTP. The templates are embedded in the binary, so rendering does not
// depend on the working directory.
package email

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"time"

	"timebox-be/pkg/logger"
)

//go:embed templates/*.html
var templateFS embed.FS

// templates holds every email template, named by file name
// (e.g. "verification_code.html"). They are compiled into the binary, so a
// parse error is a build defect and panicking at init is the right response.
var templates = template.Must(template.ParseFS(templateFS, "templates/*.html"))

// EmailService defines the interface for sending emails. validFor and
// lockedFor are shown to the reader ("This code expires in 15 minutes").
type EmailService interface {
	// SendVerificationCode sends the code that verifies the address `to`, at sign-up or on request.
	SendVerificationCode(to, name, code string, validFor time.Duration) error
	// SendPasswordResetCode sends the code that lets the owner of `to` set a new password.
	SendPasswordResetCode(to, name, code string, validFor time.Duration) error
	// SendEmailChangeCode sends the code that confirms moving the account to the new address, to.
	SendEmailChangeCode(to, name, code string, validFor time.Duration) error
	SendWelcomeEmail(to, name string) error
	SendAccountLockedEmail(to, name string, lockedFor time.Duration) error
	SendPasswordChangedEmail(to, name string) error
	// SendEmailChangedEmail tells the old address, to, that the account moved to newEmail.
	SendEmailChangedEmail(to, name, newEmail string) error
	SendAccountClosedEmail(to, name string) error
	// SendGoogleLinkedEmail tells the account's address that the Google account googleEmail can now sign in to it.
	SendGoogleLinkedEmail(to, name, googleEmail string) error
	// SendGoogleUnlinkedEmail tells the account's address that the Google account googleEmail no longer signs in to it.
	SendGoogleUnlinkedEmail(to, name, googleEmail string) error
}

// templateData is what every email template renders from
type templateData struct {
	Name         string
	AppName      string
	AppURL       string
	Code         string // the one-time code of the *_code.html emails
	NewEmail     string
	GoogleEmail  string
	Duration     string // e.g. "15 minutes"; see FormatDuration
	SupportEmail string // the footer omits the contact line when empty
	Year         int
}

// FormatDuration writes d the way the emails say it: "48 hours",
// "1 hour", "30 minutes". Durations that are not whole minutes fall back
// to Go's notation.
func FormatDuration(d time.Duration) string {
	plural := func(n int64, unit string) string {
		if n == 1 {
			return "1 " + unit
		}
		return fmt.Sprintf("%d %ss", n, unit)
	}
	switch {
	case d >= time.Hour && d%time.Hour == 0:
		return plural(int64(d/time.Hour), "hour")
	case d >= time.Minute && d%time.Minute == 0:
		return plural(int64(d/time.Minute), "minute")
	default:
		return d.String()
	}
}

// renderTemplate renders an email template with the given data
func renderTemplate(templateName string, data templateData) (string, error) {
	var buf bytes.Buffer
	if err := templates.ExecuteTemplate(&buf, templateName, data); err != nil {
		return "", fmt.Errorf("failed to render template %s: %w", templateName, err)
	}
	return buf.String(), nil
}

// NoOpEmailService sends nothing. For development without SMTP: it logs each
// email at debug level, including the code so the flow can still be
// completed by hand. Production loggers drop debug entries.
type NoOpEmailService struct{}

var _ EmailService = (*NoOpEmailService)(nil)

func (s *NoOpEmailService) SendVerificationCode(to, name, code string, validFor time.Duration) error {
	logger.Debug("[NoOpEmail] Verification code", logger.String("to", to), logger.String("code", code))
	return nil
}

func (s *NoOpEmailService) SendPasswordResetCode(to, name, code string, validFor time.Duration) error {
	logger.Debug("[NoOpEmail] Password reset code", logger.String("to", to), logger.String("code", code))
	return nil
}

func (s *NoOpEmailService) SendEmailChangeCode(to, name, code string, validFor time.Duration) error {
	logger.Debug("[NoOpEmail] Email change code", logger.String("to", to), logger.String("code", code))
	return nil
}

func (s *NoOpEmailService) SendWelcomeEmail(to, name string) error {
	logger.Debug("[NoOpEmail] Welcome email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendAccountLockedEmail(to, name string, lockedFor time.Duration) error {
	logger.Debug("[NoOpEmail] Account locked email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendPasswordChangedEmail(to, name string) error {
	logger.Debug("[NoOpEmail] Password changed email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendEmailChangedEmail(to, name, newEmail string) error {
	logger.Debug("[NoOpEmail] Email changed email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendAccountClosedEmail(to, name string) error {
	logger.Debug("[NoOpEmail] Account closed email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendGoogleLinkedEmail(to, name, googleEmail string) error {
	logger.Debug("[NoOpEmail] Google linked email", logger.String("to", to))
	return nil
}

func (s *NoOpEmailService) SendGoogleUnlinkedEmail(to, name, googleEmail string) error {
	logger.Debug("[NoOpEmail] Google unlinked email", logger.String("to", to))
	return nil
}
