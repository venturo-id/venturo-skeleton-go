package response

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestInternalError_HidesErrorDetail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/users", nil)

	InternalError(c, "Failed to get users", errors.New(`pq: relation "core.users" does not exist at 10.0.0.5`))

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, "core.users") || strings.Contains(body, "10.0.0.5") {
		t.Errorf("body leaks the internal error: %s", body)
	}
	if !strings.Contains(body, "Failed to get users") {
		t.Errorf("body = %s, want the message", body)
	}
}
