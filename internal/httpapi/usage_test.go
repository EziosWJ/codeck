package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"codeck/internal/config"
	"codeck/internal/store"
	"codeck/internal/usage"
)

func newUsageServer(t *testing.T) (*Server, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	homeRoot := t.TempDir()
	sess := filepath.Join(homeRoot, "luna", "sessions", "2026", "09", "16")
	if err := os.MkdirAll(sess, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "codex", "testdata", "session_turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sess, "rollout.jsonl"), src, 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewServer(config.Config{
		CodexHomeRoot: homeRoot,
		AuthSource:    filepath.Join(t.TempDir(), "auth.json"),
	}, db, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), "test", nil)
	return s, homeRoot
}

func TestUsageEndpointPricesLocalSessions(t *testing.T) {
	s, _ := newUsageServer(t)
	h := s.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/usage", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var got usage.Report
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Files != 1 || got.Turns != 3 {
		t.Fatalf("files=%d turns=%d", got.Files, got.Turns)
	}
	if got.Summary.USD == nil || *got.Summary.USD <= 0 {
		t.Fatalf("summary usd = %+v", got.Summary.USD)
	}
	foundSol := false
	for _, row := range got.ByModel {
		if row.Model == "gpt-6-sol" {
			foundSol = true
			if row.Wildcard || row.MatchedBy != "gpt-6-sol" {
				t.Fatalf("gpt-6-sol match = %+v", row)
			}
		}
	}
	if !foundSol {
		t.Fatalf("by_model missing gpt-6-sol: %+v", got.ByModel)
	}

	firstScan := got.ScannedAt
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/usage", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ScannedAt != firstScan {
		t.Fatalf("second GET rescanned: %s -> %s", firstScan, got.ScannedAt)
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/usage?refresh=1", nil))
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ScannedAt == firstScan {
		t.Fatal("refresh=1 kept the cached snapshot")
	}
}

func TestPriceCRUDAndRestore(t *testing.T) {
	s, _ := newUsageServer(t)
	h := s.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/prices", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	var listed []store.ModelPrice
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 11 {
		t.Fatalf("seeded %d rows", len(listed))
	}

	body := []byte(`{"pattern":"custom-x","input_usd_per_mtok":1,"cached_input_usd_per_mtok":0.1,"cache_write_usd_per_mtok":1.25,"output_usd_per_mtok":4}`)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/prices", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/prices/restore", nil)
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed) != 12 {
		t.Fatalf("after restore %d rows, want 12 (kept custom)", len(listed))
	}

	w = httptest.NewRecorder()
	bad := httptest.NewRequest(http.MethodPost, "/api/prices", bytes.NewReader([]byte(`{"pattern":""}`)))
	bad.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(w, bad)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("empty pattern status = %d", w.Code)
	}
}
