package dto

// SignUpRequest represents the signup request payload
type SignUpRequest struct {
	Email string `json:"email" binding:"required,email,max=254"`
	// Username is optional: left out, one is derived from the email, as
	// for a Google sign-up.
	Username string  `json:"username" binding:"omitempty,username"`
	Password string  `json:"password" binding:"required,password"`
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
	// Timezone is an IANA zone name, e.g. the browser's
	// Intl.DateTimeFormat().resolvedOptions().timeZone; missing or null means
	// Asia/Jakarta.
	Timezone *string `json:"timezone" binding:"omitempty,timezone"`
}

// SignUpResponse represents the signup response: no tokens yet, the
// account signs in by entering the code sent to its email.
type SignUpResponse struct {
	User         UserInfo         `json:"user"`
	Verification VerificationSent `json:"verification"`
}

// VerificationSent tells the client a verification code went to Email.
type VerificationSent struct {
	Email string `json:"email"`
	CodeTicket
}
