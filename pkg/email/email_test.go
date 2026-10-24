package email

import (
	"html"
	"strings"
	"testing"
	"time"

	"timebox-be/pkg/logger"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// templateNames is every template the senders use
var templateNames = []string{
	"verification_code.html",
	"reset_password_code.html",
	"password_changed.html",
	"account_locked.html",
	"welcome.html",
	"email_change_code.html",
	"email_changed.html",
	"account_closed.html",
	"google_linked.html",
	"google_unlinked.html",
}

// codeTemplateNames is the templates that show a one-time code
var codeTemplateNames = []string{
	"verification_code.html",
	"reset_password_code.html",
	"email_change_code.html",
}

func TestNoOpEmailService(t *testing.T) {
	service := &NoOpEmailService{}

	tests := []struct {
		name string
		fn   func() error
	}{
		{"SendVerificationCode", func() error {
			return service.SendVerificationCode("test@example.com", "Test User", "482916", 15*time.Minute)
		}},
		{"SendPasswordResetCode", func() error {
			return service.SendPasswordResetCode("test@example.com", "Test User", "731045", 15*time.Minute)
		}},
		{"SendWelcomeEmail", func() error {
			return service.SendWelcomeEmail("test@example.com", "Test User")
		}},
		{"SendAccountLockedEmail", func() error {
			return service.SendAccountLockedEmail("test@example.com", "Test User", 30*time.Minute)
		}},
		{"SendPasswordChangedEmail", func() error {
			return service.SendPasswordChangedEmail("test@example.com", "Test User")
		}},
		{"SendEmailChangeCode", func() error {
			return service.SendEmailChangeCode("test@example.com", "Test User", "905127", 15*time.Minute)
		}},
		{"SendEmailChangedEmail", func() error {
			return service.SendEmailChangedEmail("test@example.com", "Test User", "new@example.com")
		}},
		{"SendAccountClosedEmail", func() error {
			return service.SendAccountClosedEmail("test@example.com", "Test User")
		}},
		{"SendGoogleLinkedEmail", func() error {
			return service.SendGoogleLinkedEmail("test@example.com", "Test User", "test.user@gmail.com")
		}},
		{"SendGoogleUnlinkedEmail", func() error {
			return service.SendGoogleUnlinkedEmail("test@example.com", "Test User", "test.user@gmail.com")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err != nil {
				t.Errorf("%s returned error: %v, want nil", tt.name, err)
			}
		})
	}
}

// observeLogs sends the package logger's entries, debug included, to the
// returned observer until the test ends
func observeLogs(t *testing.T) *observer.ObservedLogs {
	t.Helper()
	core, logs := observer.New(zapcore.DebugLevel)
	prev := logger.Log
	logger.Log = zap.New(core)
	t.Cleanup(func() { logger.Log = prev })
	return logs
}

// Without SMTP, the debug log is where a developer reads the code.
func TestNoOpEmailService_LogsCode(t *testing.T) {
	logs := observeLogs(t)
	service := &NoOpEmailService{}

	_ = service.SendVerificationCode("ada@example.com", "Ada", "482916", 15*time.Minute)
	_ = service.SendPasswordResetCode("ada@example.com", "Ada", "731045", 15*time.Minute)
	_ = service.SendEmailChangeCode("ada@new.example.com", "Ada", "905127", 15*time.Minute)

	entries := logs.All()
	if len(entries) != 3 {
		t.Fatalf("logged %d entries, want 3", len(entries))
	}
	for i, want := range []string{"482916", "731045", "905127"} {
		if entries[i].Level != zapcore.DebugLevel {
			t.Errorf("entry %d level = %s, want debug", i, entries[i].Level)
		}
		if got := entries[i].ContextMap()["code"]; got != want {
			t.Errorf("entry %d code = %v, want %q", i, got, want)
		}
	}
}

func TestTemplates_Embedded(t *testing.T) {
	for _, name := range templateNames {
		if templates.Lookup(name) == nil {
			t.Errorf("template %s is not embedded", name)
		}
	}
	if got, want := len(templates.Templates()), len(templateNames); got != want {
		t.Errorf("embedded %d templates, want %d", got, want)
	}
}

func TestRenderTemplate(t *testing.T) {
	data := templateData{
		Name:         "Ada Lovelace",
		AppName:      "Acme Planner",
		AppURL:       "https://app.example.com",
		Code:         "482916",
		NewEmail:     "ada@new.example.com",
		GoogleEmail:  "ada.lovelace@gmail.com",
		Duration:     "48 hours",
		SupportEmail: "help@example.com",
		Year:         2031,
	}

	// What each template must show beyond the shared name / app / footer.
	// The code stands alone in its box: nothing inserted between the digits.
	specific := map[string][]string{
		"verification_code.html":   {`<div class="code">482916</div>`, "This code expires in 48 hours."},
		"reset_password_code.html": {`<div class="code">482916</div>`, "This code expires in 48 hours.", "you can ignore this email"},
		"password_changed.html":    nil,
		"account_locked.html":      {"unlocked in 48 hours"},
		"welcome.html":             {"<title>Welcome to Acme Planner</title>", `href="https://app.example.com"`},
		"email_change_code.html":   {`<div class="code">482916</div>`, "This code expires in 48 hours.", "you can safely ignore this email"},
		"email_changed.html":       {"ada@new.example.com"},
		"account_closed.html":      {"Your Acme Planner account has been closed"},
		"google_linked.html":       {"The Google account <strong>ada.lovelace@gmail.com</strong> can now sign in", "Reset your password"},
		"google_unlinked.html":     {"The Google account <strong>ada.lovelace@gmail.com</strong> can no longer sign in", "Reset your password"},
	}

	for _, name := range templateNames {
		t.Run(name, func(t *testing.T) {
			body, err := renderTemplate(name, data)
			if err != nil {
				t.Fatalf("renderTemplate() error = %v", err)
			}

			want := append([]string{
				"Hi Ada Lovelace,",
				"The Acme Planner",
				"© 2031 Acme Planner. All rights reserved.",
				`href="mailto:help@example.com"`,
			}, specific[name]...)
			for _, s := range want {
				if !strings.Contains(body, s) {
					t.Errorf("rendered %s does not contain %q", name, s)
				}
			}

			// The app name comes from data only: no default or leftover brand.
			if strings.Contains(body, DefaultAppName) {
				t.Errorf("rendered %s contains %q although AppName is %q", name, DefaultAppName, data.AppName)
			}
			if strings.Contains(body, "{{") {
				t.Errorf("rendered %s contains an unexecuted action", name)
			}
		})
	}
}

// The code emails carry the code only: no link or button to click,
// although the app URL is at hand.
func TestRenderTemplate_CodeEmailsHaveNoLink(t *testing.T) {
	data := templateData{
		Name:    "Ada",
		AppName: DefaultAppName,
		AppURL:  "https://app.example.com",
		Code:    "482916",
		Year:    2031,
	}
	for _, name := range codeTemplateNames {
		body, err := renderTemplate(name, data)
		if err != nil {
			t.Fatalf("renderTemplate(%s) error = %v", name, err)
		}
		for _, s := range []string{"https://app.example.com", "href=", `class="button"`} {
			if strings.Contains(body, s) {
				t.Errorf("%s contains %q", name, s)
			}
		}
	}
}

func TestRenderTemplate_EscapesData(t *testing.T) {
	body, err := renderTemplate("welcome.html", templateData{
		Name:    `<script>alert("x")</script>`,
		AppName: DefaultAppName,
		AppURL:  "https://app.example.com",
		Year:    2031,
	})
	if err != nil {
		t.Fatalf("renderTemplate() error = %v", err)
	}
	if strings.Contains(body, "<script>") {
		t.Error("user-controlled name was rendered unescaped")
	}
	if !strings.Contains(html.UnescapeString(body), `<script>alert("x")</script>`) {
		t.Error("escaped name is missing from the body")
	}
}

func TestRenderTemplate_WithoutSupportEmail(t *testing.T) {
	for _, name := range templateNames {
		body, err := renderTemplate(name, templateData{AppName: DefaultAppName, Year: 2031})
		if err != nil {
			t.Fatalf("renderTemplate(%s) error = %v", name, err)
		}
		if strings.Contains(body, "mailto:") || strings.Contains(body, "Need help?") {
			t.Errorf("%s renders the support line without a support email", name)
		}
	}
}

func TestRenderTemplate_Unknown(t *testing.T) {
	if _, err := renderTemplate("missing.html", templateData{}); err == nil {
		t.Error("renderTemplate() of an unknown template: error = nil")
	}
}

func TestFormatDuration(t *testing.T) {
	tests := map[time.Duration]string{
		48 * time.Hour:   "48 hours",
		time.Hour:        "1 hour",
		30 * time.Minute: "30 minutes",
		time.Minute:      "1 minute",
		90 * time.Minute: "90 minutes",
		45 * time.Second: "45s",
	}
	for d, want := range tests {
		if got := FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
