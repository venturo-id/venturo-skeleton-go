package dto

// SignUpRequest represents the signup request payload
type SignUpRequest struct {
	Email    string  `json:"email" binding:"required,email"`
	Username string  `json:"username" binding:"required,min=3,max=100"`
	Password string  `json:"password" binding:"required,min=8"`
	FullName *string `json:"full_name" binding:"omitempty,max=255"`
	Phone    *string `json:"phone" binding:"omitempty,max=20"`
}

// SignUpResponse represents the signup response
type SignUpResponse struct {
	Message string   `json:"message"`
	User    UserInfo `json:"user"`
}
