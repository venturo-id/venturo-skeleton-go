// Package notification generalises user-facing messaging (email, SMS, push)
// behind a single Notifier. It follows the repo's pkg/ convention: a
// package-local Config, dependencies injected via parameters, and degraded
// (no-op) adapters when credentials are absent — never importing
// internal/config. It reuses pkg/email as-is for the email channel.
//
// Note: pkg/discord also has a type named Notifier, but that one is alerting
// infra (warn/error logs → Discord). This Notifier is for business
// notifications and is intentionally separate.
package notification

import (
	"context"

	"venturo-skeleton-go/pkg/email"
	"venturo-skeleton-go/pkg/logger"
)

// Config holds credentials for the optional channels. Mirrors
// pkg/email.SMTPConfig: a local struct the caller populates (main.go maps
// internal/config values into it).
type Config struct {
	Twilio TwilioConfig
	FCM    FCMConfig
}

// TwilioConfig configures the SMS channel.
type TwilioConfig struct {
	AccountSID string
	AuthToken  string
	FromNumber string
}

// FCMConfig configures the push channel. CredentialsJSON may be empty to use
// Application Default Credentials.
type FCMConfig struct {
	CredentialsJSON string
}

// EmailSender is satisfied by pkg/email.EmailService — used as-is.
type EmailSender = email.EmailService

// SMSSender sends a plain SMS message.
type SMSSender interface {
	SendSMS(ctx context.Context, to, body string) error
}

// PushSender sends a push notification to a device token.
type PushSender interface {
	SendPush(ctx context.Context, token, title, body string, data map[string]string) error
}

// Channel identifies a notification transport.
type Channel string

const (
	ChannelEmail Channel = "email"
	ChannelSMS   Channel = "sms"
	ChannelPush  Channel = "push"
)

// Request is a high-level notification spanning one or more channels.
type Request struct {
	Channels []Channel

	// Email
	Email     string // recipient address
	EmailName string

	// SMS
	Phone string

	// Push
	PushToken string
	PushData  map[string]string

	// Shared content
	Title string
	Body  string
}

// Notifier aggregates the channel senders. Any sender may be a no-op when its
// credentials are absent.
type Notifier struct {
	Email EmailSender
	SMS   SMSSender
	Push  PushSender
}

// New builds a Notifier, selecting a real adapter per channel when credentials
// are present and falling back to no-op otherwise. Never fatal (degraded).
// The email sender is injected because pkg/email already owns SMTP setup; pass
// nil to use a no-op email sender.
func New(ctx context.Context, cfg Config, emailSender EmailSender) (*Notifier, error) {
	n := &Notifier{}

	if emailSender != nil {
		n.Email = emailSender
	} else {
		n.Email = &email.NoOpEmailService{}
	}

	n.SMS = newTwilioSMS(cfg.Twilio)
	n.Push = newFCMPush(ctx, cfg.FCM)

	logger.Info("notification initialized",
		logger.String("email", adapterLabel(isRealEmail(n.Email), "smtp")),
		logger.String("sms", adapterLabel(isReal(n.SMS), "twilio")),
		logger.String("push", adapterLabel(isReal(n.Push), "fcm")),
	)

	return n, nil
}

// isReal reports whether the sender is a real adapter (not one of the no-op
// fallbacks selected when credentials are absent).
func isReal(sender any) bool {
	switch sender.(type) {
	case noopSMS, noopPush:
		return false
	default:
		return sender != nil
	}
}

// isRealEmail reports whether the email sender is the real SMTP service rather
// than the no-op fallback.
func isRealEmail(sender EmailSender) bool {
	if sender == nil {
		return false
	}
	_, isNoOp := sender.(*email.NoOpEmailService)
	return !isNoOp
}

// adapterLabel returns the adapter name when enabled, else "noop".
func adapterLabel(enabled bool, name string) string {
	if enabled {
		return name
	}
	return "noop"
}

// Notify dispatches to each requested channel, returning the first error.
func (n *Notifier) Notify(ctx context.Context, req Request) error {
	for _, ch := range req.Channels {
		switch ch {
		case ChannelSMS:
			if err := n.SMS.SendSMS(ctx, req.Phone, req.Body); err != nil {
				return err
			}
		case ChannelPush:
			if err := n.Push.SendPush(ctx, req.PushToken, req.Title, req.Body, req.PushData); err != nil {
				return err
			}
		case ChannelEmail:
			// Email templates are domain-specific; the Notifier exposes the
			// EmailSender for callers to use directly. Generic send is a no-op
			// placeholder here.
		}
	}
	return nil
}
