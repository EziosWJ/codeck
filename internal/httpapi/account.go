package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"codeck/internal/codex"
)

// defaultAccountCacheTTL applies when no TTL was configured, i.e. when a Server
// is built from a bare config.Config literal rather than through config.Load.
const defaultAccountCacheTTL = 30 * time.Second

// accountCacheTTL reports how long an account snapshot may be reused. The ttl
// is a config knob because every open web tab polls /account on its own timer;
// this cache is what turns that traffic into one App Server call per window.
func (s *Server) accountCacheTTL() time.Duration {
	if s.cfg.AccountCacheTTL > 0 {
		return s.cfg.AccountCacheTTL
	}
	return defaultAccountCacheTTL
}

// accountSnapshot is the dashboard payload. Nested objects are the App Server's
// own JSON, passed through so the UI can show whatever the protocol returns.
type accountSnapshot struct {
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	Account    json.RawMessage `json:"account"`
	RateLimits json.RawMessage `json:"rate_limits"`
	Usage      json.RawMessage `json:"usage"`
}

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.loadAccount(r.Context()))
}

func (s *Server) loadAccount(ctx context.Context) accountSnapshot {
	if s.appServer == nil {
		return accountSnapshot{OK: false, Error: "Codex App Server 未启动"}
	}

	s.accountMu.Lock()
	defer s.accountMu.Unlock()
	if !s.accountCachedAt.IsZero() && time.Since(s.accountCachedAt) < s.accountCacheTTL() {
		return s.accountCached
	}

	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	snap := fetchAccount(ctx, s.appServer)
	s.accountCached = snap
	s.accountCachedAt = time.Now()
	return snap
}

func fetchAccount(ctx context.Context, app *codex.AppServer) accountSnapshot {
	type piece struct {
		raw json.RawMessage
		err error
	}
	var acc, limits, usage piece
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		acc.raw, acc.err = app.AccountRead(ctx)
	}()
	go func() {
		defer wg.Done()
		limits.raw, limits.err = app.AccountRateLimitsRead(ctx)
	}()
	go func() {
		defer wg.Done()
		usage.raw, usage.err = app.AccountUsageRead(ctx)
	}()
	wg.Wait()

	snap := accountSnapshot{
		OK:         true,
		Account:    acc.raw,
		RateLimits: limits.raw,
		Usage:      usage.raw,
	}
	var errs []string
	if acc.err != nil {
		errs = append(errs, "account/read: "+acc.err.Error())
	}
	if limits.err != nil {
		errs = append(errs, "account/rateLimits/read: "+limits.err.Error())
	}
	if usage.err != nil {
		errs = append(errs, "account/usage/read: "+usage.err.Error())
	}
	if len(errs) > 0 {
		snap.OK = false
		snap.Error = strings.Join(errs, "; ")
	}
	return snap
}
