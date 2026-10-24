package email

import (
	"errors"
	"fmt"
	"io"
	"net/mail"
	"net/url"
	"time"

	"timebox-be/pkg/logger"

	"gopkg.in/gomail.v2"
)

// DefaultAppName is the product name used when Config.AppName is empty.
const DefaultAppName = "Timebox"

// defaultPort is the SMTP submission port, upgraded with STARTTLS.
const defaultPort = 587

var (
	errEmptyCode     = errors.New("email: code is required")
	errEmptyDuration = errors.New("email: a positive duration is required")
)

// Config holds the SMTP connection and the values rendered into emails.
// Empty optional fields fall back to the defaults noted on each.
//
// The connection is encrypted — TLS from the first byte on port 465,
// STARTTLS on any other port — and sending fails when the server offers
// neither. The one exception is a local test server such as Mailpit: with
// no Username and Password, and Host on the local machine (localhost,
// 127.0.0.0/8 or ::1), mail goes in plain text when the server offers no
// STARTTLS. Credentials never cross an unencrypted connection.
type Config struct {
	Host string
	Port int // default 587 (STARTTLS); 465 uses TLS from the first byte
	// Username and Password are set together or not at all. Without them
	// the client does not authenticate, so the server must accept mail
	// as is — as a local Mailpit (localhost:1025) does.
	Username string
	Password string

	// FromEmail sends the code emails (verification, password reset,
	// email change), e.g. no-reply@. NotificationsFromEmail sends the
	// notices (welcome, account locked, password or email changed, account
	// closed, Google linked or unlinked), e.g. notifications@; default
	// FromEmail. Both are plain addresses, without a name.
	FromEmail              string
	NotificationsFromEmail string
	FromName               string // default AppName

	// AppName is the product name in subjects and templates. Default "Timebox".
	AppName string
	// FrontendURL is the app's base URL, the target of the welcome email's button.
	FrontendURL string
	// SupportEmail is the contact address in the footer. The footer leaves
	// the contact line out when it is empty.
	SupportEmail string
}

// Enabled reports whether SMTP is configured at all. When it is not, the
// caller can fall back to NoOpEmailService. Credentials are optional (see
// Config), so they do not count.
func (c Config) Enabled() bool {
	return c.Host != "" && c.FromEmail != ""
}

// withDefaults fills the optional fields left empty
func (c Config) withDefaults() Config {
	if c.AppName == "" {
		c.AppName = DefaultAppName
	}
	if c.FromName == "" {
		c.FromName = c.AppName
	}
	if c.NotificationsFromEmail == "" {
		c.NotificationsFromEmail = c.FromEmail
	}
	if c.Port == 0 {
		c.Port = defaultPort
	}
	return c
}

// validate names the first missing or malformed field. Values are never
// echoed back: the error ends up in logs, and Password is a secret.
func (c Config) validate() error {
	required := []struct{ name, value string }{
		{"Host", c.Host},
		{"FromEmail", c.FromEmail},
		{"FrontendURL", c.FrontendURL},
	}
	for _, f := range required {
		if f.value == "" {
			return fmt.Errorf("email: SMTP %s is required", f.name)
		}
	}

	// Half a credential is a typo, not a choice to skip authentication.
	if c.Username != "" && c.Password == "" {
		return errors.New("email: SMTP Password is required when Username is set")
	}
	if c.Password != "" && c.Username == "" {
		return errors.New("email: SMTP Username is required when Password is set")
	}

	if c.Port < 1 || c.Port > 65535 {
		return errors.New("email: SMTP Port must be between 1 and 65535")
	}
	senders := []struct{ name, value string }{
		{"FromEmail", c.FromEmail},
		{"NotificationsFromEmail", c.NotificationsFromEmail},
	}
	for _, f := range senders {
		if addr, err := mail.ParseAddress(f.value); err != nil || addr.Address != f.value {
			return fmt.Errorf("email: %s must be a plain email address", f.name)
		}
	}
	if u, err := url.Parse(c.FrontendURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("email: FrontendURL must be an absolute http(s) URL")
	}
	return nil
}

// SMTPEmailService implements EmailService using SMTP
type SMTPEmailService struct {
	cfg Config
	// send delivers msg from the envelope sender from to the recipient to,
	// within sendTimeout. Tests replace it to capture messages instead.
	send func(from, to string, msg io.WriterTo) error
}

var _ EmailService = (*SMTPEmailService)(nil)

// NewSMTPEmailService creates a new SMTP email service. It does not connect:
// a wrong host or credential only shows up on the first send.
func NewSMTPEmailService(cfg Config) (*SMTPEmailService, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &SMTPEmailService{
		cfg:  cfg,
		send: newTransport(cfg).send,
	}, nil
}

// SendVerificationCode sends the code that verifies the address to,
// valid for validFor
func (s *SMTPEmailService) SendVerificationCode(to, name, code string, validFor time.Duration) error {
	subject := code + " is your " + s.cfg.AppName + " verification code"
	return s.sendCode(to, name, code, validFor, subject, "verification_code.html")
}

// SendPasswordResetCode sends the code, valid for validFor, that lets the
// owner of to set a new password
func (s *SMTPEmailService) SendPasswordResetCode(to, name, code string, validFor time.Duration) error {
	subject := code + " is your " + s.cfg.AppName + " password reset code"
	return s.sendCode(to, name, code, validFor, subject, "reset_password_code.html")
}

// SendEmailChangeCode sends the code confirming an email change, valid
// for validFor, to the new address
func (s *SMTPEmailService) SendEmailChangeCode(to, name, code string, validFor time.Duration) error {
	subject := code + " is your code to confirm your new " + s.cfg.AppName + " email"
	return s.sendCode(to, name, code, validFor, subject, "email_change_code.html")
}

// SendWelcomeEmail sends a welcome email to new users
func (s *SMTPEmailService) SendWelcomeEmail(to, name string) error {
	return s.notify(to, "Welcome to "+s.cfg.AppName+"!", "welcome.html", s.newData(name))
}

// SendAccountLockedEmail tells the owner their account is locked for lockedFor
func (s *SMTPEmailService) SendAccountLockedEmail(to, name string, lockedFor time.Duration) error {
	if lockedFor <= 0 {
		return errEmptyDuration
	}

	data := s.newData(name)
	data.Duration = FormatDuration(lockedFor)

	return s.notify(to, "Account Security Alert - "+s.cfg.AppName, "account_locked.html", data)
}

// SendPasswordChangedEmail sends an email when password is changed
func (s *SMTPEmailService) SendPasswordChangedEmail(to, name string) error {
	return s.notify(to, "Password Changed Successfully - "+s.cfg.AppName, "password_changed.html", s.newData(name))
}

// SendEmailChangedEmail tells the old address that the account moved to
// newEmail
func (s *SMTPEmailService) SendEmailChangedEmail(to, name, newEmail string) error {
	data := s.newData(name)
	data.NewEmail = newEmail

	return s.notify(to, "Your Email Was Changed - "+s.cfg.AppName, "email_changed.html", data)
}

// SendAccountClosedEmail confirms that the account was closed
func (s *SMTPEmailService) SendAccountClosedEmail(to, name string) error {
	return s.notify(to, "Your Account Was Closed - "+s.cfg.AppName, "account_closed.html", s.newData(name))
}

// SendGoogleLinkedEmail tells the account's address that the Google
// account googleEmail can now sign in to it
func (s *SMTPEmailService) SendGoogleLinkedEmail(to, name, googleEmail string) error {
	data := s.newData(name)
	data.GoogleEmail = googleEmail

	return s.notify(to, "Google Account Linked - "+s.cfg.AppName, "google_linked.html", data)
}

// SendGoogleUnlinkedEmail tells the account's address that the Google
// account googleEmail no longer signs in to it
func (s *SMTPEmailService) SendGoogleUnlinkedEmail(to, name, googleEmail string) error {
	data := s.newData(name)
	data.GoogleEmail = googleEmail

	return s.notify(to, "Google Account Unlinked - "+s.cfg.AppName, "google_unlinked.html", data)
}

// newData returns the template data every email shares
func (s *SMTPEmailService) newData(name string) templateData {
	return templateData{
		Name:         name,
		AppName:      s.cfg.AppName,
		AppURL:       s.cfg.FrontendURL,
		SupportEmail: s.cfg.SupportEmail,
		Year:         time.Now().Year(),
	}
}

// sendCode sends templateName showing code and how long it stays valid
func (s *SMTPEmailService) sendCode(to, name, code string, validFor time.Duration, subject, templateName string) error {
	if code == "" {
		return errEmptyCode
	}
	if validFor <= 0 {
		return errEmptyDuration
	}

	data := s.newData(name)
	data.Code = code
	data.Duration = FormatDuration(validFor)

	return s.sendTemplate(s.cfg.FromEmail, to, subject, templateName, data)
}

// notify sends a notice (anything but a code) from NotificationsFromEmail
func (s *SMTPEmailService) notify(to, subject, templateName string, data templateData) error {
	return s.sendTemplate(s.cfg.NotificationsFromEmail, to, subject, templateName, data)
}

// sendTemplate renders templateName and sends it from the address from to
// a single recipient
func (s *SMTPEmailService) sendTemplate(from, to, subject, templateName string, data templateData) error {
	rcpt, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("email: invalid recipient address: %w", err)
	}

	body, err := renderTemplate(templateName, data)
	if err != nil {
		logger.Error("Failed to render email template",
			logger.String("template", templateName),
			logger.Err(err),
		)
		return err
	}

	// gomail only builds the message; transport delivers it.
	m := gomail.NewMessage()
	m.SetAddressHeader("From", from, s.cfg.FromName)
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", body)

	// The recipient and the subject stay out of the logs: errors are
	// forwarded to Discord, an address is personal data, and a code
	// email's subject carries the code. The template says which email it was.
	if err := s.send(from, rcpt.Address, m); err != nil {
		logger.Error("Failed to send email",
			logger.String("template", templateName),
			logger.Err(err),
		)
		return fmt.Errorf("failed to send email: %w", err)
	}

	logger.Info("Email sent successfully", logger.String("template", templateName))

	return nil
}
