package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"codeck/internal/config"
)

func TestAccountCacheTTLFallsBackWhenUnset(t *testing.T) {
	// config.Load always populates the field, but tests and internal callers
	// build Server from a bare literal, so the fallback must stay in place.
	s := &Server{cfg: config.Config{}}
	if got := s.accountCacheTTL(); got != defaultAccountCacheTTL {
		t.Errorf("accountCacheTTL() = %s, want the fallback %s", got, defaultAccountCacheTTL)
	}

	s = &Server{cfg: config.Config{AccountCacheTTL: 90 * time.Second}}
	if got := s.accountCacheTTL(); got != 90*time.Second {
		t.Errorf("accountCacheTTL() = %s, want the configured 90s", got)
	}
}

func TestAccountEndpointWithoutAppServer(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	s.handleAccount(w, httptest.NewRequest(http.MethodGet, "/api/account", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got accountSnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OK {
		t.Fatal("expected ok=false when App Server is not running")
	}
	if got.Error == "" {
		t.Fatal("expected an error message")
	}
}
