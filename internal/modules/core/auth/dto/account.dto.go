package dto

// EmailRequest is the body of POST /auth/verify-email/resend and
// POST /auth/forgot-password.
type EmailRequest struct {
	Email string `json:"email" binding:"required,email,max=254"`
}

// EmailCodeRequest is the body of POST /auth/verify-email and
// POST /auth/reset-password/verify: the code emailed to Email.
type EmailCodeRequest struct {
	Email string `json:"email" binding:"required,email,max=254"`
	Code  string `json:"code" binding:"required,len=6,numeric"`
}

// CodeRequest is the body of POST /account/email/confirm: the code
// emailed to the new address.
type CodeRequest struct {
	Code string `json:"code" binding:"required,len=6,numeric"`
}

// CodeTicket answers a request that sends a code: how long the code works
// and when another may be asked for, in seconds. Requests that must not
// reveal whether an account exists answer with it either way.
type CodeTicket struct {
	ExpiresIn         int `json:"expires_in"`
	ResendAvailableIn int `json:"resend_available_in"`
}

// ResetGrantResponse answers POST /auth/reset-password/verify: the token
// that sets the new password, valid for ExpiresIn seconds.
type ResetGrantResponse struct {
	ResetToken string `json:"reset_token"`
	ExpiresIn  int    `json:"expires_in"`
}

// ResetPasswordRequest is the body of POST /auth/reset-password.
type ResetPasswordRequest struct {
	ResetToken  string `json:"reset_token" binding:"required,max=128"`
	NewPassword string `json:"new_password" binding:"required,password"`
}
