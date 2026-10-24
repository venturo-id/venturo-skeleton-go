package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"timebox-be/internal/modules/core/auditlog/service"
	"timebox-be/internal/shared/audit"
)

// fakeReader records the filter it was asked for.
type fakeReader struct {
	filter      audit.Filter
	page, limit int
	err         error
}

func (f *fakeReader) List(_ context.Context, filter audit.Filter, page, limit int) ([]audit.Entry, int64, error) {
	f.filter, f.page, f.limit = filter, page, limit
	if f.err != nil {
		return nil, 0, f.err
	}
	return []audit.Entry{{Action: audit.ActionSignIn}}, 1, nil
}

func (f *fakeReader) ListByActor(context.Context, string, int) ([]audit.Entry, error) {
	return nil, nil
}

func get(t *testing.T, reader audit.Reader, query string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/audit-logs", NewAuditLogHandler(service.NewAuditLogService(reader)).List)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/audit-logs"+query, nil))
	return w
}

func TestList_PassesFilters(t *testing.T) {
	reader := &fakeReader{}
	w := get(t, reader, "?page=2&limit=5&actor_id=4b8c6f0e-6c1a-4f5e-9d39-2f1f6a7b8c9d"+
		"&target_type=role&target_id=r-1&action=role.updated&from=2026-10-01T00:00:00Z&to=2026-10-23T07:00:00%2B07:00")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}

	f := reader.filter
	if f.ActorID != "4b8c6f0e-6c1a-4f5e-9d39-2f1f6a7b8c9d" || f.TargetType != "role" || f.TargetID != "r-1" || f.Action != "role.updated" {
		t.Errorf("filter = %+v", f)
	}
	if f.From == nil || !f.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("from = %v", f.From)
	}
	if f.To == nil || !f.To.Equal(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("to = %v, want 2026-10-23T00:00:00Z", f.To)
	}
	if reader.page != 2 || reader.limit != 5 {
		t.Errorf("page, limit = %d, %d", reader.page, reader.limit)
	}

	var body struct {
		Meta struct {
			Pagination struct {
				Total int64 `json:"total"`
			} `json:"pagination"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Meta.Pagination.Total != 1 {
		t.Errorf("pagination total = %d (%v)", body.Meta.Pagination.Total, err)
	}
}

func TestList_EmptyBoundIsNoBound(t *testing.T) {
	reader := &fakeReader{}
	if w := get(t, reader, "?from=&to="); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", w.Code, w.Body)
	}
	if reader.filter.From != nil || reader.filter.To != nil {
		t.Errorf("from, to = %v, %v; want no bounds", reader.filter.From, reader.filter.To)
	}
}

func TestList_RejectsBadQuery(t *testing.T) {
	for _, query := range []string{"?actor_id=not-a-uuid", "?target_type=table", "?target_type=session", "?from=yesterday", "?limit=101"} {
		if w := get(t, &fakeReader{}, query); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, w.Code)
		}
	}
}

func TestList_DisabledLog(t *testing.T) {
	if w := get(t, audit.Discard{}, ""); w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}
