package service

import (
	"context"
	"time"

	"timebox-be/internal/modules/core/auditlog/dto"
	"timebox-be/internal/shared/audit"
)

// AuditLogService reads the audit log.
type AuditLogService struct {
	reader audit.Reader
}

func NewAuditLogService(reader audit.Reader) *AuditLogService {
	return &AuditLogService{reader: reader}
}

// List returns one page of entries, newest first, and the total. Without
// MONGO_URI it fails with audit.ErrDisabled.
func (s *AuditLogService) List(ctx context.Context, params *dto.AuditLogQueryParams) ([]audit.Entry, int64, error) {
	return s.reader.List(ctx, audit.Filter{
		ActorID:    params.ActorID,
		TargetType: params.TargetType,
		TargetID:   params.TargetID,
		Action:     params.Action,
		From:       bound(params.From),
		To:         bound(params.To),
	}, params.Page, params.Limit)
}

// bound drops a time that binding left at zero ("from=" with no value).
func bound(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	return t
}
