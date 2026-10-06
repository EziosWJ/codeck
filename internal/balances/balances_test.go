package balances

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codeck/internal/store"
)

func TestCredentialEncryption(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	credential := "provider-secret-value"
	ciphertext, err := seal(key, []byte(credential))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ciphertext), credential) {
		t.Fatal("ciphertext contains the plaintext credential")
	}
	plain, err := open(key, ciphertext)
	if err != nil || string(plain) != credential {
		t.Fatalf("decrypt = %q, %v", plain, err)
	}
	if _, err := open([]byte(strings.Repeat("x", 32)), ciphertext); err == nil {
		t.Fatal("decrypting with the wrong key should fail")
	}
}

func TestParseEncryptionKey(t *testing.T) {
	valid := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("z", 32)))
	if _, err := parseKey(valid); err != nil {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, invalid := range []string{"", "bad-base64", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := parseKey(invalid); err == nil {
			t.Fatalf("parseKey(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestProvidersUseBearerAndReturnAccountBalances(t *testing.T) {
	const secret = "do-not-log-this-key"
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Errorf("authorization = %q", got)
		}
		body := `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"123.45","granted_balance":"3.45","topped_up_balance":"120.00"},{"currency":"USD","total_balance":"2.10","granted_balance":"0.10","topped_up_balance":"2.00"}]}`
		if strings.Contains(r.URL.Path, "/credits") {
			body = `{"data":{"total_credits":100.5,"total_usage":25.75}}`
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	client := &http.Client{Transport: transport}
	p := httpProvider{client: client, deepSeekURL: "https://deepseek.test/user/balance", openRouterURL: "https://openrouter.test/api/v1/credits"}
	got, err := p.Fetch(context.Background(), "deepseek", secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Total != "123.45" || got[0].Details["granted_balance"] != "3.45" || got[1].Currency != "USD" {
		t.Fatalf("DeepSeek balance = %+v", got)
	}
	got, err = p.Fetch(context.Background(), "openrouter", secret)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Currency != "USD" || got[0].Total != "74.75" {
		t.Fatalf("OpenRouter balance = %+v", got)
	}
}

func TestProviderErrorsNeverIncludeCredentialOrBody(t *testing.T) {
	const secret = "sensitive-key"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized, Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(secret)),
		}, nil
	})}
	p := httpProvider{client: client, deepSeekURL: "https://deepseek.test/user/balance"}
	_, err := p.Fetch(context.Background(), "deepseek", secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked credential: %v", err)
	}
}

func TestManagerRejectsWrongEnvironmentKeyAndDoesNotPoll(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "balances.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	correct := NewManager(db, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))), nil)
	ciphertext, err := correct.EncryptCredential("provider-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateBalanceConfig(store.BalanceConfig{
		Provider: "deepseek", Name: "test", IntervalSeconds: 300,
		CredentialCiphertext: ciphertext,
	}); err != nil {
		t.Fatal(err)
	}

	wrong := NewManager(db, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("b", 32))), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrong.Start(ctx)
	deadline := time.Now().Add(time.Second)
	for wrong.Available() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if wrong.Available() || !strings.Contains(wrong.AvailabilityError(), "密钥") {
		t.Fatal("manager should disable itself when stored credentials cannot be decrypted")
	}
}

func TestManagerDropsRefreshResultAfterCredentialReplacement(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "balances.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	m := NewManager(db, base64.StdEncoding.EncodeToString([]byte(strings.Repeat("a", 32))), nil)
	oldCipher, err := m.EncryptCredential("old-key")
	if err != nil {
		t.Fatal(err)
	}
	config, err := db.CreateBalanceConfig(store.BalanceConfig{
		Provider: "deepseek", Name: "test", IntervalSeconds: 300,
		CredentialCiphertext: oldCipher,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	m.fetcher = fetcherFunc(func(_ context.Context, _ string, key string) ([]store.BalanceAmount, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return []store.BalanceAmount{{Currency: "USD", Total: "1.00"}}, nil
		}
		if key != "new-key" {
			t.Errorf("second poll used %q, want replacement key", key)
		}
		return []store.BalanceAmount{{Currency: "USD", Total: "9.00"}}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("startup poll did not begin")
	}

	newCipher, err := m.EncryptCredential("new-key")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.UpdateBalanceConfig(store.BalanceConfig{
		ID: config.ID, Provider: config.Provider, Name: config.Name,
		IntervalSeconds: config.IntervalSeconds, CredentialCiphertext: newCipher,
	}, true); err != nil {
		t.Fatal(err)
	}
	m.ScheduleUpdate(config.ID, true)
	close(release)

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		got, err := db.GetBalanceConfig(config.ID)
		if err == nil && len(got.Balances) == 1 && got.Balances[0].Total == "9.00" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got, _ := db.GetBalanceConfig(config.ID)
	t.Fatalf("new credential balance did not replace stale poll: %+v; calls=%d", got.Balances, calls.Load())
}

type fetcherFunc func(context.Context, string, string) ([]store.BalanceAmount, error)

func (f fetcherFunc) Fetch(ctx context.Context, provider, key string) ([]store.BalanceAmount, error) {
	return f(ctx, provider, key)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
