package email

import (
	"crypto/tls"
	"fmt"
	"os"
	"strconv"
	"time"

	"venturo-skeleton-go-simple/pkg/logger"

	"gopkg.in/gomail.v2"
)

// SMTPConfig holds SMTP configuration
type SMTPConfig struct {
	Host         string
	Port         int
	Username     string
	Password     string
	FromEmail    string
	FromName     string
	FrontendURL  string
	VerifyURL    string
	ResetURL     string
	SupportEmail string
}

// SMTPEmailService implements EmailService using SMTP
type SMTPEmailService struct {
	config *SMTPConfig
}

// NewSMTPEmailService creates a new SMTP email service
func NewSMTPEmailService() (*SMTPEmailService, error) {
	port, err := strconv.Atoi(getEnv("SMTP_PORT", "587"))
	if err != nil {
		port = 587
	}

	config := &SMTPConfig{
		Host:         getEnv("SMTP_HOST", "smtp.mailtrap.io"),
		Port:         port,
		Username:     getEnv("SMTP_USER", ""),
		Password:     getEnv("SMTP_PASSWORD", ""),
		FromEmail:    getEnv("SMTP_FROM_EMAIL", "noreply@tuai.com"),
		FromName:     getEnv("SMTP_FROM_NAME", "Lakukan"),
		FrontendURL:  getEnv("FRONTEND_URL", "http://localhost:3000"),
		VerifyURL:    getEnv("EMAIL_VERIFICATION_URL", "http://localhost:3000/verify-email"),
		ResetURL:     getEnv("RESET_PASSWORD_URL", "http://localhost:3000/reset-password"),
		SupportEmail: getEnv("SUPPORT_EMAIL", "support@tuai.com"),
	}

	// Validate SMTP credentials
	if config.Username == "" || config.Password == "" {
		return nil, fmt.Errorf("SMTP credentials not configured: SMTP_USER and SMTP_PASSWORD environment variables are required")
	}

	// Validate SMTP host
	if config.Host == "" {
		return nil, fmt.Errorf("SMTP host not configured: SMTP_HOST environment variable is required")
	}

	return &SMTPEmailService{
		config: config,
	}, nil
}

// SendVerificationEmail sends an email verification email
func (s *SMTPEmailService) SendVerificationEmail(to, name, token string) error {
	verificationURL := fmt.Sprintf("%s?token=%s", s.config.VerifyURL, token)

	data := EmailData{
		Name:            name,
		AppName:         "Lakukan",
		AppURL:          s.config.FrontendURL,
		VerificationURL: verificationURL,
		SupportEmail:    s.config.SupportEmail,
		Year:            time.Now().Year(),
	}

	body, err := renderTemplate("verification.html", data)
	if err != nil {
		logger.Error("Failed to render verification email template", logger.Err(err))
		return err
	}

	subject := "Verify Your Email - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendOTPVerificationEmail sends an OTP code for email verification
func (s *SMTPEmailService) SendOTPVerificationEmail(to, name, otpCode string) error {
	data := EmailData{
		Name:         name,
		AppName:      "Lakukan",
		AppURL:       s.config.FrontendURL,
		OTPCode:      otpCode,
		SupportEmail: s.config.SupportEmail,
		Year:         time.Now().Year(),
	}

	body, err := renderTemplate("otp_verification.html", data)
	if err != nil {
		logger.Error("Failed to render OTP verification email template", logger.Err(err))
		return err
	}

	subject := "Your Verification Code - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendVerificationEmailWithOTP sends an email with both verification link and OTP code
func (s *SMTPEmailService) SendVerificationEmailWithOTP(to, name, token, otpCode string) error {
	// Build verification URL
	verificationURL := fmt.Sprintf("%s?token=%s", s.config.VerifyURL, token)

	data := EmailData{
		Name:            name,
		AppName:         "Lakukan",
		AppURL:          s.config.FrontendURL,
		VerificationURL: verificationURL,
		OTPCode:         otpCode,
		SupportEmail:    s.config.SupportEmail,
		Year:            time.Now().Year(),
	}

	body, err := renderTemplate("verification_with_otp.html", data)
	if err != nil {
		logger.Error("Failed to render verification with OTP email template", logger.Err(err))
		return err
	}

	subject := "Verify Your Email - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendPasswordResetEmail sends a password reset email
func (s *SMTPEmailService) SendPasswordResetEmail(to, name, token string) error {
	resetURL := fmt.Sprintf("%s?token=%s", s.config.ResetURL, token)

	data := EmailData{
		Name:             name,
		AppName:          "Lakukan",
		AppURL:           s.config.FrontendURL,
		ResetPasswordURL: resetURL,
		SupportEmail:     s.config.SupportEmail,
		Year:             time.Now().Year(),
	}

	body, err := renderTemplate("reset_password.html", data)
	if err != nil {
		logger.Error("Failed to render password reset email template", logger.Err(err))
		return err
	}

	subject := "Reset Your Password - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendPasswordResetOTP sends an OTP code for password reset
func (s *SMTPEmailService) SendPasswordResetOTP(to, name, otpCode string) error {
	data := EmailData{
		Name:         name,
		AppName:      "Lakukan",
		AppURL:       s.config.FrontendURL,
		OTPCode:      otpCode,
		SupportEmail: s.config.SupportEmail,
		Year:         time.Now().Year(),
	}

	body, err := renderTemplate("forgot_password_otp.html", data)
	if err != nil {
		logger.Error("Failed to render password reset OTP email template", logger.Err(err))
		return err
	}

	subject := "Your Password Reset Code - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendWelcomeEmail sends a welcome email to new users
func (s *SMTPEmailService) SendWelcomeEmail(to, name string) error {
	data := EmailData{
		Name:         name,
		AppName:      "Lakukan",
		AppURL:       s.config.FrontendURL,
		SupportEmail: s.config.SupportEmail,
		Year:         time.Now().Year(),
	}

	body, err := renderTemplate("welcome.html", data)
	if err != nil {
		logger.Error("Failed to render welcome email template", logger.Err(err))
		return err
	}

	subject := "Welcome to Lakukan!"
	return s.sendEmail(to, subject, body)
}

// SendAccountLockedEmail sends an email when account is locked
func (s *SMTPEmailService) SendAccountLockedEmail(to, name string) error {
	data := EmailData{
		Name:         name,
		AppName:      "Lakukan",
		AppURL:       s.config.FrontendURL,
		SupportEmail: s.config.SupportEmail,
		Year:         time.Now().Year(),
	}

	body, err := renderTemplate("account_locked.html", data)
	if err != nil {
		logger.Error("Failed to render account locked email template", logger.Err(err))
		return err
	}

	subject := "Account Security Alert - Lakukan"
	return s.sendEmail(to, subject, body)
}

// SendPasswordChangedEmail sends an email when password is changed
func (s *SMTPEmailService) SendPasswordChangedEmail(to, name string) error {
	data := EmailData{
		Name:         name,
		AppName:      "Lakukan",
		AppURL:       s.config.FrontendURL,
		SupportEmail: s.config.SupportEmail,
		Year:         time.Now().Year(),
	}

	body, err := renderTemplate("password_changed.html", data)
	if err != nil {
		logger.Error("Failed to render password changed email template", logger.Err(err))
		return err
	}

	subject := "Password Changed Successfully - Lakukan"
	return s.sendEmail(to, subject, body)
}

// sendEmail sends an email using SMTP
func (s *SMTPEmailService) sendEmail(to, subject, body string) error {
	m := gomail.NewMessage()
	m.SetHeader("From", fmt.Sprintf("%s <%s>", s.config.FromName, s.config.FromEmail))
	m.SetHeader("To", to)
	m.SetHeader("Subject", subject)
	m.SetBody("text/html", body)

	d := gomail.NewDialer(s.config.Host, s.config.Port, s.config.Username, s.config.Password)

	// Use TLS for port 587 (STARTTLS)
	if s.config.Port == 587 {
		d.TLSConfig = &tls.Config{
			ServerName:         s.config.Host,
			InsecureSkipVerify: false,
		}
	}

	// Send the email
	if err := d.DialAndSend(m); err != nil {
		logger.Error("Failed to send email",
			logger.String("to", to),
			logger.String("subject", subject),
			logger.Err(err),
		)
		return fmt.Errorf("failed to send email: %w", err)
	}

	logger.Info("Email sent successfully",
		logger.String("to", to),
		logger.String("subject", subject),
	)

	return nil
}

// getEnv gets environment variable with fallback
func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
