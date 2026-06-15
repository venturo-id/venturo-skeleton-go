package notification

import (
	"context"
	"fmt"

	"venturo-skeleton-go/pkg/logger"

	"github.com/twilio/twilio-go"
	twilioApi "github.com/twilio/twilio-go/rest/api/v2010"
)

// twilioSMS sends SMS via Twilio's REST API.
type twilioSMS struct {
	client *twilio.RestClient
	from   string
}

// newTwilioSMS returns a Twilio-backed SMSSender, or a no-op sender (with a
// warn log) when credentials are absent. Never fatal.
func newTwilioSMS(cfg TwilioConfig) SMSSender {
	if cfg.AccountSID == "" || cfg.AuthToken == "" || cfg.FromNumber == "" {
		logger.Warn("notification: Twilio not configured — SMS channel is no-op")
		return noopSMS{}
	}

	client := twilio.NewRestClientWithParams(twilio.ClientParams{
		Username: cfg.AccountSID,
		Password: cfg.AuthToken,
	})
	return &twilioSMS{client: client, from: cfg.FromNumber}
}

func (s *twilioSMS) SendSMS(_ context.Context, to, body string) error {
	params := &twilioApi.CreateMessageParams{}
	params.SetTo(to)
	params.SetFrom(s.from)
	params.SetBody(body)

	if _, err := s.client.Api.CreateMessage(params); err != nil {
		return fmt.Errorf("notification(twilio): send sms to %q: %w", to, err)
	}
	return nil
}
