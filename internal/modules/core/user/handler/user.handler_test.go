package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/middleware"
	"timebox-be/internal/modules/core/user/service"
	"timebox-be/internal/shared/audit"
	"timebox-be/pkg/avatar"
	jwtpkg "timebox-be/pkg/jwt"
)

const testUserID = "550e8400-e29b-41d4-a716-446655440000"

func init() {
	gin.SetMode(gin.TestMode)
}

// noObjects is a bucket that must not be written to: every request in
// these tests is refused before anything is stored.
type noObjects struct{ t *testing.T }

func (o noObjects) Upload(context.Context, string, io.Reader, string) error {
	o.t.Error("Upload called")
	return nil
}

func (o noObjects) Delete(context.Context, string) error {
	o.t.Error("Delete called")
	return nil
}

func (noObjects) PublicURL(key string) string { return "https://cdn.timebox.test/" + key }

// newHandler returns a handler whose service has storage when withStorage
// is set, and nothing else: the requests tested never reach the database.
func newHandler(t *testing.T, withStorage bool) *UserHandler {
	var store *avatar.Store
	if withStorage {
		store = avatar.NewStore(noObjects{t}, "https://cdn.timebox.test")
	}
	return NewUserHandler(service.NewUserService(nil, nil, nil, nil, nil, audit.Discard{}, store))
}

// serve runs one request as a signed-in user, the body capped at
// bodyLimit bytes as the router caps it, and decodes the JSON response.
func serve(t *testing.T, h *UserHandler, req *http.Request, bodyLimit int64) (int, map[string]any) {
	t.Helper()
	r := gin.New()
	signedIn := func(c *gin.Context) {
		middleware.SetUserContext(c, &jwtpkg.Claims{UserID: testUserID})
	}
	r.PUT("/users/me/avatar", middleware.MaxBodySize(bodyLimit), signedIn, h.UploadAvatar)
	r.DELETE("/users/me/avatar", middleware.MaxBodySize(bodyLimit), signedIn, h.RemoveAvatar)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not JSON: %s", w.Body.String())
	}
	return w.Code, resp
}

// upload builds PUT /users/me/avatar with data in the multipart field
// field.
func upload(t *testing.T, field string, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile(field, "photo.png")
	if err != nil {
		t.Fatalf("CreateFormFile() error = %v", err)
	}
	_, _ = part.Write(data)
	if err := form.Close(); err != nil {
		t.Fatalf("multipart Close() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/users/me/avatar", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func TestUploadAvatar_Refused(t *testing.T) {
	jsonBody := httptest.NewRequest(http.MethodPut, "/users/me/avatar", strings.NewReader(`{"avatar_url":"https://evil.example/a.png"}`))
	jsonBody.Header.Set("Content-Type", "application/json")

	tests := []struct {
		name        string
		withStorage bool
		req         *http.Request
		bodyLimit   int64
		wantStatus  int
		wantCode    string
		wantErrors  map[string]string // field -> first message
	}{
		{
			name: "no file field", withStorage: true, req: upload(t, "photo", []byte("x")), bodyLimit: 1 << 20,
			wantStatus: http.StatusBadRequest, wantCode: "validation_failed",
			wantErrors: map[string]string{"file": "is required"},
		},
		{
			name: "not a multipart body", withStorage: true, req: jsonBody, bodyLimit: 1 << 20,
			wantStatus: http.StatusBadRequest, wantCode: "validation_failed",
			wantErrors: map[string]string{"file": "is required"},
		},
		{
			name: "body over the limit", withStorage: true, req: upload(t, "file", bytes.Repeat([]byte{0xff}, 4096)), bodyLimit: 1024,
			wantStatus: http.StatusRequestEntityTooLarge, wantCode: "payload_too_large",
			wantErrors: map[string]string{"detail": "limit is 1024 bytes"},
		},
		{
			name: "not an image", withStorage: true, req: upload(t, "file", []byte("<svg onload=alert(1)>")), bodyLimit: 1 << 20,
			wantStatus: http.StatusBadRequest, wantCode: CodeInvalidImage,
			wantErrors: map[string]string{"detail": "Upload a JPEG, PNG or WebP image up to 4096×4096"},
		},
		{
			name: "no storage", withStorage: false, req: upload(t, "file", []byte("x")), bodyLimit: 1 << 20,
			wantStatus: http.StatusServiceUnavailable, wantCode: CodeStorageNotConfigured,
		},
		{
			name: "remove without storage", withStorage: false, req: httptest.NewRequest(http.MethodDelete, "/users/me/avatar", nil), bodyLimit: 1 << 20,
			wantStatus: http.StatusServiceUnavailable, wantCode: CodeStorageNotConfigured,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, resp := serve(t, newHandler(t, tt.withStorage), tt.req, tt.bodyLimit)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%v)", status, tt.wantStatus, resp)
			}
			if resp["code"] != tt.wantCode {
				t.Errorf("code = %v, want %s", resp["code"], tt.wantCode)
			}
			errs, _ := resp["errors"].(map[string]any)
			for field, want := range tt.wantErrors {
				messages, _ := errs[field].([]any)
				if len(messages) == 0 || messages[0] != want {
					t.Errorf("errors.%s = %v, want [%s]", field, errs[field], want)
				}
			}
		})
	}
}
