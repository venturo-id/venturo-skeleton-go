package request

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"timebox-be/pkg/validator"
)

type signUp struct {
	Email    string `json:"email" binding:"required,email"`
	FullName string `json:"full_name" binding:"required,max=5"`
}

func init() {
	gin.SetMode(gin.TestMode)
	if err := validator.RegisterGinValidators(); err != nil {
		panic(err)
	}
}

// do runs handler for one request and decodes the JSON response.
func do(t *testing.T, handler gin.HandlerFunc, method, path, body string) (int, map[string]any) {
	t.Helper()
	r := gin.New()
	r.Handle(method, "/items/:id", handler)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))

	var resp map[string]any
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("response is not JSON: %s", w.Body.String())
		}
	}
	return w.Code, resp
}

func TestBindJSON_ReportsFieldsByJSONName(t *testing.T) {
	handler := func(c *gin.Context) {
		var req signUp
		if BindJSON(c, &req) {
			c.Status(http.StatusOK)
		}
	}

	code, resp := do(t, handler, http.MethodPost, "/items/1", `{"email":"nope","full_name":"too long"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	errs, _ := resp["errors"].(map[string]any)
	if _, ok := errs["email"]; !ok {
		t.Errorf("errors = %v, want a key for email", errs)
	}
	if _, ok := errs["full_name"]; !ok {
		t.Errorf("errors = %v, want the json name full_name, not FullName", errs)
	}

	// A wrong JSON type names the field, not the Go struct behind it.
	code, resp = do(t, handler, http.MethodPost, "/items/1", `{"email": 5}`)
	errs, _ = resp["errors"].(map[string]any)
	if code != http.StatusBadRequest || errs["email"] == nil {
		t.Errorf("type error: status = %d, body = %v, want 400 with errors.email", code, resp)
	}
}

func TestBindJSON_BodyTooLarge(t *testing.T) {
	handler := func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10)
		var req signUp
		if BindJSON(c, &req) {
			c.Status(http.StatusOK)
		}
	}

	code, _ := do(t, handler, http.MethodPost, "/items/1", `{"email":"someone@example.com","full_name":"x"}`)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", code)
	}
}

func TestUUIDParam(t *testing.T) {
	handler := func(c *gin.Context) {
		if id, ok := UUIDParam(c, "id"); ok {
			c.String(http.StatusOK, id)
		}
	}

	if code, _ := do(t, handler, http.MethodGet, "/items/abc", ""); code != http.StatusBadRequest {
		t.Errorf("non-UUID: status = %d, want 400", code)
	}

	r := gin.New()
	r.GET("/items/:id", handler)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/items/6F9619FF-8B86-D011-B42D-00C04FC964FF", nil))
	if w.Code != http.StatusOK || w.Body.String() != "6f9619ff-8b86-d011-b42d-00c04fc964ff" {
		t.Errorf("UUID: status = %d, body = %q, want 200 and the canonical form", w.Code, w.Body.String())
	}
}

func TestBindOptionalJSON_AcceptsEmptyBody(t *testing.T) {
	handler := func(c *gin.Context) {
		var req struct {
			Reason *string `json:"reason" binding:"omitempty,max=3"`
		}
		if BindOptionalJSON(c, &req) {
			c.Status(http.StatusOK)
		}
	}

	if code, _ := do(t, handler, http.MethodDelete, "/items/1", ""); code != http.StatusOK {
		t.Errorf("empty body: status = %d, want 200", code)
	}
	if code, _ := do(t, handler, http.MethodDelete, "/items/1", `{"reason":"too long"}`); code != http.StatusBadRequest {
		t.Errorf("invalid body: status = %d, want 400", code)
	}
}
