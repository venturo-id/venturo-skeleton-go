package dto

import "time"

// ─── CONFIG REQUESTS ────────────────────────────────────────────

// ApprovalConfigLevelInput is the payload piece describing one level.
type ApprovalConfigLevelInput struct {
	Level           int16    `json:"level" binding:"required,min=1"`
	RoleID          string   `json:"role_id" binding:"required,uuid"`
	ApproverUserIDs []string `json:"approver_user_ids" binding:"required,min=1,dive,uuid"`
}

// CreateApprovalConfigRequest creates a new per (branch, feature_key) config.
type CreateApprovalConfigRequest struct {
	BranchID   string                     `json:"branch_id" binding:"required,uuid"`
	FeatureKey string                     `json:"feature_key" binding:"required"`
	IsActive   *bool                      `json:"is_active"`
	Levels     []ApprovalConfigLevelInput `json:"levels" binding:"required,dive"`
}

// UpdateApprovalConfigRequest fully replaces the level list on update.
// Levels are treated as a set — if you omit one it gets deleted.
type UpdateApprovalConfigRequest struct {
	IsActive *bool                       `json:"is_active"`
	Levels   *[]ApprovalConfigLevelInput `json:"levels,omitempty" binding:"omitempty,dive"`
}

// ApprovalConfigQueryParams filters list endpoint.
type ApprovalConfigQueryParams struct {
	BranchID   string `form:"branch_id" binding:"omitempty,uuid"`
	FeatureKey string `form:"feature_key"`
}

// ─── CONFIG RESPONSES ───────────────────────────────────────────

type ApprovalConfigLevelResponse struct {
	ID              string   `json:"id"`
	Level           int16    `json:"level"`
	RoleID          string   `json:"role_id"`
	ApproverUserIDs []string `json:"approver_user_ids"`
}

type ApprovalConfigResponse struct {
	ID         string                        `json:"id"`
	CompanyID  string                        `json:"company_id"`
	BranchID   string                        `json:"branch_id"`
	FeatureKey string                        `json:"feature_key"`
	IsActive   bool                          `json:"is_active"`
	Levels     []ApprovalConfigLevelResponse `json:"levels"`
	CreatedAt  time.Time                     `json:"created_at"`
	UpdatedAt  time.Time                     `json:"updated_at"`
}
