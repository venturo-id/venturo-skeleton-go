package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"venturo-skeleton-go/pkg/derrors"
)

func init() { gin.SetMode(gin.TestMode) }

// renderInto runs RenderError for err and returns the HTTP status and decoded body.
func renderInto(err error) (int, Response) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	RenderError(c, err)

	var body Response
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// TestRenderError_AllCodesMapped guards completeness: every ErrorCode the package
// defines must resolve to a deliberate status, never fall through to the unmapped
// generic-500 branch. If someone appends a code to derrors and forgets to map it,
// this fails. Bump the upper bound when a new code is added.
func TestRenderError_AllCodesMapped(t *testing.T) {
	const lastCode = derrors.ErrorCodeCustomForbidden

	// Codes that legitimately render as 500.
	want500 := map[derrors.ErrorCode]bool{
		derrors.ErrorCodeUnknown:              true,
		derrors.ErrorCodeCustomInternalServer: true,
	}

	for code := derrors.ErrorCode(0); code <= lastCode; code++ {
		err := derrors.NewErrorf(code, "boom")
		status, _ := renderInto(err)

		if want500[code] {
			if status != http.StatusInternalServerError {
				t.Errorf("code %d: want 500, got %d", code, status)
			}
			continue
		}
		if status == http.StatusInternalServerError {
			t.Errorf("code %d fell through to an unmapped generic 500 — add it to render.go", code)
		}
		if status < 100 || status > 599 {
			t.Errorf("code %d: implausible status %d", code, status)
		}
	}
}

func TestRenderError_NotFoundIs404(t *testing.T) {
	status, body := renderInto(derrors.NewErrorf(derrors.ErrorCodeNotFound, "branch %s not found", "abc"))
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d", status)
	}
	// Generic NotFound must NOT leak the internal message.
	if body.Message != "Data Not Found" {
		t.Errorf("want safe static message, got %q", body.Message)
	}
}

func TestRenderError_PassthroughSurfacesMessage(t *testing.T) {
	const msg = "invoice already settled"
	status, body := renderInto(derrors.NewErrorf(derrors.ErrorCodeCustomNotFound, msg))
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d", status)
	}
	if body.Message != msg {
		t.Errorf("passthrough should surface message verbatim; want %q, got %q", msg, body.Message)
	}
}

func TestRenderError_CustomAlreadyExistsIs409WithMessage(t *testing.T) {
	const msg = "Email already exists"
	status, body := renderInto(derrors.NewErrorf(derrors.ErrorCodeCustomAlreadyExists, msg))
	if status != http.StatusConflict {
		t.Fatalf("want 409, got %d", status)
	}
	if body.Message != msg {
		t.Errorf("passthrough should surface message verbatim; want %q, got %q", msg, body.Message)
	}
}

func TestRenderError_CustomForbiddenIs403WithMessage(t *testing.T) {
	const msg = "cannot delete default branch"
	status, body := renderInto(derrors.NewErrorf(derrors.ErrorCodeCustomForbidden, msg))
	if status != http.StatusForbidden {
		t.Fatalf("want 403, got %d", status)
	}
	if body.Message != msg {
		t.Errorf("passthrough should surface message verbatim; want %q, got %q", msg, body.Message)
	}
}

// A passthrough code must surface only the user-safe message, never the wrapped
// cause (e.g. pgx.ErrNoRows), which Error() would otherwise append.
func TestRenderError_PassthroughHidesWrappedCause(t *testing.T) {
	cause := errors.New("no rows in result set")
	err := derrors.WrapErrorf(cause, derrors.ErrorCodeCustomNotFound, "Branch %s not found", "abc")
	status, body := renderInto(err)
	if status != http.StatusNotFound {
		t.Fatalf("want 404, got %d", status)
	}
	if body.Message != "Branch abc not found" {
		t.Errorf("want clean message, got %q", body.Message)
	}
	if strings.Contains(body.Message, "no rows in result set") {
		t.Errorf("passthrough leaked wrapped cause: %q", body.Message)
	}
}

func TestRenderError_NonDomainErrorIsGeneric500(t *testing.T) {
	status, body := renderInto(http.ErrBodyNotAllowed) // any non-*derrors.Error
	if status != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", status)
	}
	if body.Message != "Internal server error" {
		t.Errorf("want generic message, got %q", body.Message)
	}
	// Must never echo the raw error into the body.
	if body.Errors != nil {
		t.Errorf("non-domain error must not leak detail, got %v", body.Errors)
	}
}
