package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthStartingThenOk(t *testing.T) {
	h := &Health{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-ready code = %d, want 503", rec.Code)
	}
	h.SetReady()
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("post-ready code = %d, want 200", rec.Code)
	}
}
