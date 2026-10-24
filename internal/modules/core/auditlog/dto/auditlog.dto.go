package dto

import "time"

// AuditLogQueryParams are the filters of GET /audit-logs. Every one is
// optional; from is inclusive, to exclusive.
type AuditLogQueryParams struct {
	Page       int        `form:"page,default=1" binding:"min=1,max=100000"`
	Limit      int        `form:"limit,default=20" binding:"min=1,max=100"`
	ActorID    string     `form:"actor_id" binding:"omitempty,uuid"`
	TargetType string     `form:"target_type" binding:"omitempty,oneof=user role api_key"`
	TargetID   string     `form:"target_id" binding:"omitempty,max=64"`
	Action     string     `form:"action" binding:"omitempty,max=64"`
	From       *time.Time `form:"from" time_format:"2006-01-02T15:04:05Z07:00"`
	To         *time.Time `form:"to" time_format:"2006-01-02T15:04:05Z07:00"`
}
