package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"codeck/internal/config"
	"codeck/internal/store"
)

func newBalanceTestServer(t *testing.T, encodedKey string) (*Server, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "balances.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := NewServer(config.Config{BalanceEncryptionKey: encodedKey}, db, nil, nil, nil, logger, "test", nil)
	return s, db
}

func TestBalanceAPIEncryptsAndNeverReturnsProviderKey(t *testing.T) {
	encodedKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	s, db := newBalanceTestServer(t, encodedKey)
	const credential = "provider-private-key"

	req := httptest.NewRequest(http.MethodPost, "/api/balances", strings.NewReader(
		`{"provider":"deepseek","name":"personal","key":"`+credential+`","interval_seconds":300}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), credential) {
		t.Fatalf("create response leaked credential: %s", w.Body.String())
	}

	var created store.BalanceConfig
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	stored, err := db.GetBalanceConfig(created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.CredentialCiphertext), credential) {
		t.Fatal("SQLite contains the credential in plaintext")
	}

	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/balances", nil))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), credential) {
		t.Fatalf("list status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBalanceFeatureWithoutEncryptionKeyDoesNotBlockOtherEndpoints(t *testing.T) {
	s, _ := newBalanceTestServer(t, "")
	h := s.Handler()

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/balances", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"available":false`) {
		t.Fatalf("balance list status=%d body=%s", w.Code, w.Body.String())
	}

	req := httptest.NewRequest(http.MethodPost, "/api/balances", strings.NewReader(
		`{"provider":"deepseek","name":"personal","key":"secret","interval_seconds":300}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret") {
		t.Fatalf("create without key status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/no-such-endpoint", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unrelated API route was blocked: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestUnavailableBalanceFeaturePreservesStoredConfigs(t *testing.T) {
	encodedKey := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	s, db := newBalanceTestServer(t, encodedKey)
	ciphertext, err := s.balances.EncryptCredential("provider-secret")
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateBalanceConfig(store.BalanceConfig{
		Provider: "deepseek", Name: "keep", IntervalSeconds: 300,
		CredentialCiphertext: ciphertext,
	})
	if err != nil {
		t.Fatal(err)
	}

	s.balances = nil
	req := httptest.NewRequest(http.MethodDelete, "/api/balances/"+strconv.FormatInt(c.ID, 10), strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete status=%d body=%s", w.Code, w.Body.String())
	}
	if _, err := db.GetBalanceConfig(c.ID); err != nil {
		t.Fatalf("configuration was deleted while balance feature was unavailable: %v", err)
	}
}
