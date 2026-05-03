package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestStrictAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	allowed := map[string]struct{}{
		"GET /probe": {},
	}
	h := securityHeaders(strictAllowlist(allowed, mux))

	t.Run("unknown path is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty body, got %q", rec.Body.String())
		}
	})
	t.Run("wrong method on registered path is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/probe", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d want 404", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty body, got %q", rec.Body.String())
		}
	})
	t.Run("allowed route reaches mux", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/probe", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
	})
}

func TestStrictAllowlistFinanceHistory(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /finance/history/{page}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := securityHeaders(strictAllowlist(map[string]struct{}{}, mux))

	t.Run("bare history path is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/finance/history", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty body, got %q", rec.Body.String())
		}
	})
	t.Run("history with page reaches mux", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/finance/history/1", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("got %d", rec.Code)
		}
	})
	t.Run("history page zero is 404", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/finance/history/0", nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("got %d", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Fatalf("expected empty body, got %q", rec.Body.String())
		}
	})
}
