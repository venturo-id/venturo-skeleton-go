package notification

import (
	"context"
	"fmt"

	"venturo-skeleton-go/pkg/logger"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"
)

// fcmPush sends push notifications via Firebase Cloud Messaging.
type fcmPush struct {
	client *messaging.Client
}

// newFCMPush returns an FCM-backed PushSender, or a no-op sender (with a warn
// log) when Firebase can't be initialized / credentials are absent. Never
// fatal. CredentialsJSON may be empty to use Application Default Credentials.
func newFCMPush(ctx context.Context, cfg FCMConfig) PushSender {
	var opts []option.ClientOption
	if cfg.CredentialsJSON != "" {
		opts = append(opts, option.WithCredentialsJSON([]byte(cfg.CredentialsJSON)))
	} else {
		// No explicit creds and no ADC hint — treat as unconfigured to avoid a
		// slow ADC lookup that will fail anyway in dev.
		logger.Warn("notification: FCM not configured — push channel is no-op")
		return noopPush{}
	}

	app, err := firebase.NewApp(ctx, nil, opts...)
	if err != nil {
		logger.Warn("notification: FCM init failed — push channel is no-op", logger.Err(err))
		return noopPush{}
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		logger.Warn("notification: FCM messaging client failed — push channel is no-op", logger.Err(err))
		return noopPush{}
	}

	return &fcmPush{client: client}
}

func (p *fcmPush) SendPush(ctx context.Context, token, title, body string, data map[string]string) error {
	_, err := p.client.Send(ctx, &messaging.Message{
		Token: token,
		Notification: &messaging.Notification{
			Title: title,
			Body:  body,
		},
		Data: data,
	})
	if err != nil {
		return fmt.Errorf("notification(fcm): send push: %w", err)
	}
	return nil
}
