package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codeck/internal/codex"
	"codeck/internal/config"
	"codeck/internal/scheduler"
	"codeck/internal/store"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestBasicAuthProtectsUIAndAPI(t *testing.T) {
	cfg := config.Default()
	cfg.HTTPAuthUser = "admin"
	cfg.HTTPAuthPassword = "secret"
	s := NewServer(cfg, nil, nil, nil, nil, testLogger(), "test", nil)
	h := s.Handler()

	for _, target := range []string{"/", "/api/no-such-endpoint"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without auth: status=%d, want 401", target, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="Codeck"` {
			t.Fatalf("%s WWW-Authenticate=%q", target, got)
		}

		req = httptest.NewRequest(http.MethodGet, target, nil)
		req.SetBasicAuth("admin", "secret")
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusUnauthorized {
			t.Fatalf("%s with valid auth still returned 401", target)
		}
	}
}

func TestMutationRequiresJSONAndSameOrigin(t *testing.T) {
	cfg := config.Default()
	s := NewServer(cfg, nil, nil, nil, nil, testLogger(), "test", nil)
	h := s.Handler()

	req := httptest.NewRequest(http.MethodPost, "/api/no-such-endpoint", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain mutation status=%d, want 415", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/no-such-endpoint", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("foreign origin status=%d, want 403", rec.Code)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/no-such-endpoint", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	req.Host = "example.com"
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("same-origin mutation status=%d, want route 404", rec.Code)
	}
}

func TestHealthDoesNotExposeBasicCredentials(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	log := testLogger()
	svc := codex.NewService("definitely-not-a-real-codex-binary", filepath.Join(dir, "homes"),
		filepath.Join(dir, "workspaces"), "", time.Second, 1, log)
	sched := scheduler.New(db, svc, time.Second, log)
	cfg := config.Default()
	cfg.DBPath = filepath.Join(dir, "test.db")
	cfg.HTTPAuthUser = "admin"
	cfg.HTTPAuthPassword = "super-secret-password"

	s := NewServer(cfg, db, svc, sched, nil, log, "test", nil)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.SetBasicAuth(cfg.HTTPAuthUser, cfg.HTTPAuthPassword)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if strings.Contains(body, cfg.HTTPAuthUser) || strings.Contains(body, cfg.HTTPAuthPassword) {
		t.Fatalf("health response leaked HTTP credentials: %s", body)
	}
}
