package notification

import (
	"context"

	"venturo-skeleton-go/pkg/logger"
)

// noopSMS logs instead of sending. Used when Twilio credentials are absent.
type noopSMS struct{}

func (noopSMS) SendSMS(_ context.Context, to, body string) error {
	logger.Info("[NoOpSMS] would send SMS",
		logger.String("to", to),
		logger.Int("body_len", len(body)),
	)
	return nil
}

// noopPush logs instead of sending. Used when FCM credentials are absent.
type noopPush struct{}

func (noopPush) SendPush(_ context.Context, token, title, _ string, _ map[string]string) error {
	logger.Info("[NoOpPush] would send push",
		logger.String("token", token),
		logger.String("title", title),
	)
	return nil
}
