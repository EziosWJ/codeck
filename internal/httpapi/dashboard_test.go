package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"codeck/internal/codex"
	"codeck/internal/config"
	"codeck/internal/scheduler"
	"codeck/internal/store"
)

// fakeCodex writes a `codex` executable that appends one line to a log for
// every invocation, so tests can count how many processes were spawned.
func fakeCodex(t *testing.T, version string) (bin, logPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "probes.log")
	bin = filepath.Join(dir, "codex")
	script := "#!/bin/sh\n" +
		"printf 'probe\\n' >> '" + logPath + "'\n" +
		"printf 'codex-cli " + version + "\\n'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, logPath
}

func probeCount(t *testing.T, logPath string) int {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatal(err)
	}
	return strings.Count(string(data), "probe\n")
}

func newProbeServer(t *testing.T, bin string) *Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := codex.NewService(bin, filepath.Join(dir, "homes"), filepath.Join(dir, "ws"),
		"", time.Second, 1, log)
	cfg := config.Default()
	cfg.DBPath = filepath.Join(dir, "t.db")
	return NewServer(cfg, db, svc, scheduler.New(db, svc, time.Second, log), nil, log, "test", nil)
}

func healthBody(t *testing.T, s *Server) map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

// The whole point of the cache: the UI polls /health and /dashboard every 10s
// from every open tab, and each uncached call spawns `codex --version`.
func TestCodexProbeIsSharedAcrossHealthAndDashboard(t *testing.T) {
	bin, logPath := fakeCodex(t, "9.9.9")
	s := newProbeServer(t, bin)

	body := healthBody(t, s)
	if got := body["codex_version"]; got != "codex-cli 9.9.9" {
		t.Fatalf("codex_version = %v, want codex-cli 9.9.9", got)
	}
	if got := body["codex_available"]; got != true {
		t.Fatalf("codex_available = %v, want true", got)
	}

	for i := 0; i < 3; i++ {
		healthBody(t, s)
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/dashboard", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("dashboard status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if n := probeCount(t, logPath); n != 1 {
		t.Fatalf("spawned the CLI %d times across 8 requests, want 1", n)
	}

	// Past the TTL the next request re-probes, then caches again.
	s.codexMu.Lock()
	s.codexCachedAt = time.Now().Add(-codexCacheTTL - time.Second)
	s.codexMu.Unlock()
	healthBody(t, s)
	healthBody(t, s)
	if n := probeCount(t, logPath); n != 2 {
		t.Fatalf("spawned the CLI %d times after TTL expiry, want 2", n)
	}
}

// Concurrent tabs must collapse into a single probe rather than one each.
func TestCodexProbeIsSerializedUnderConcurrency(t *testing.T) {
	bin, logPath := fakeCodex(t, "9.9.9")
	s := newProbeServer(t, bin)
	h := s.Handler()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("health status=%d", rec.Code)
			}
		}()
	}
	wg.Wait()

	if n := probeCount(t, logPath); n != 1 {
		t.Fatalf("20 concurrent requests spawned the CLI %d times, want 1", n)
	}
}
