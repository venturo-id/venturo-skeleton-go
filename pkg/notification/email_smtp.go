package notification

import "venturo-skeleton-go/pkg/email"

// NewEmailSender returns an EmailSender backed by the existing SMTP service.
// On misconfiguration it falls back to the no-op email service (degraded), so
// the skeleton boots without SMTP credentials. This is a thin wrapper — the
// EmailSender type is pkg/email.EmailService, reused as-is.
func NewEmailSender() EmailSender {
	svc, err := email.NewSMTPEmailService()
	if err != nil {
		return &email.NoOpEmailService{}
	}
	return svc
}
