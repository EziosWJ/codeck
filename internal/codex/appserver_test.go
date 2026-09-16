package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if os.Getenv("CRONCODEX_FAKE_APPSERVER") == "1" {
		runFakeAppServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeAppServer() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	w := bufio.NewWriter(os.Stdout)
	initialized := false
	for sc.Scan() {
		line := sc.Bytes()
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		switch msg.Method {
		case methodInitialize:
			// A notification before the matching response must be ignored.
			fmt.Fprintf(w, `{"method":"account/updated","params":{}}`+"\n")
			fmt.Fprintf(w, `{"id":%s,"result":{"userAgent":"fake/1","codexHome":"/tmp/fake-codex","platformFamily":"unix","platformOs":"linux"}}`+"\n", msg.ID)
		case methodInitialized:
			initialized = true
		case methodAccountRead:
			if !initialized {
				fmt.Fprintf(w, `{"id":%s,"error":{"code":-32000,"message":"not initialized"}}`+"\n", msg.ID)
				break
			}
			fmt.Fprintf(w, `{"id":%s,"result":{"account":{"type":"chatgpt","email":"a@b.c","planType":"plus"},"requiresOpenaiAuth":false}}`+"\n", msg.ID)
		case methodAccountRateLimitsRead:
			fmt.Fprintf(w, `{"id":%s,"result":{"ordinaryUsageAllowed":true,"rateLimits":{},"accountId":"acc-1"}}`+"\n", msg.ID)
		case methodAccountUsageRead:
			fmt.Fprintf(w, `{"id":%s,"result":{"summary":{"lifetimeTokens":42},"dailyUsageBuckets":[]}}`+"\n", msg.ID)
		default:
			if len(msg.ID) > 0 && string(msg.ID) != "null" {
				fmt.Fprintf(w, `{"id":%s,"error":{"code":-32601,"message":"method not found"}}`+"\n", msg.ID)
			}
		}
		_ = w.Flush()
	}
}

func TestStartAppServerMissingBinary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := StartAppServer(ctx, AppServerOptions{
		Bin: "/no/such/codex-app-server",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("expected start to fail")
	}
}

func TestAppServerHandshakeAndAccountReads(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv, err := StartAppServer(ctx, AppServerOptions{
		Bin:           os.Args[0],
		ClientName:    "croncodex-test",
		ClientVersion: "test",
		Log:           slog.New(slog.NewTextHandler(io.Discard, nil)),
		ExtraEnv:      []string{"CRONCODEX_FAKE_APPSERVER=1"},
	})
	if err != nil {
		t.Fatalf("StartAppServer: %v", err)
	}
	defer srv.Close()

	init := srv.InitializeResult()
	if init.UserAgent != "fake/1" || init.CodexHome != "/tmp/fake-codex" {
		t.Fatalf("initialize result = %+v", init)
	}

	raw, err := srv.AccountRead(ctx)
	if err != nil {
		t.Fatalf("account/read: %v", err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"email":"a@b.c"`) {
		t.Fatalf("account/read = %s", raw)
	}

	raw, err = srv.AccountRateLimitsRead(ctx)
	if err != nil {
		t.Fatalf("account/rateLimits/read: %v", err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"accountId":"acc-1"`) {
		t.Fatalf("account/rateLimits/read = %s", raw)
	}

	raw, err = srv.AccountUsageRead(ctx)
	if err != nil {
		t.Fatalf("account/usage/read: %v", err)
	}
	if !json.Valid(raw) || !strings.Contains(string(raw), `"lifetimeTokens":42`) {
		t.Fatalf("account/usage/read = %s", raw)
	}

	if err := srv.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
}

func TestAppServerClosesChild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	srv, err := StartAppServer(ctx, AppServerOptions{
		Bin:      os.Args[0],
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		ExtraEnv: []string{"CRONCODEX_FAKE_APPSERVER=1"},
	})
	if err != nil {
		t.Fatalf("StartAppServer: %v", err)
	}
	if err := srv.Close(); err != nil {
		t.Logf("Close: %v", err)
	}
	_ = srv.Close()
	select {
	case <-srv.waitDone:
	case <-time.After(2 * time.Second):
		t.Fatal("child was not reaped")
	}
}

// TestIntegrationAppServerAccountAPIs drives the real Codex App Server. Opt-in
// because it talks to the operator's logged-in account:
//
//	CRONCODEX_INTEGRATION=1 go test -count=1 -v -run TestIntegrationAppServer ./internal/codex/
func TestIntegrationAppServerAccountAPIs(t *testing.T) {
	if os.Getenv("CRONCODEX_INTEGRATION") != "1" {
		t.Skip("set CRONCODEX_INTEGRATION=1 to run against the real Codex App Server")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	srv, err := StartAppServer(ctx, AppServerOptions{
		Bin:           "codex",
		ClientVersion: "test",
		Log:           slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("StartAppServer: %v", err)
	}
	defer srv.Close()

	t.Logf("initialize: %+v", srv.InitializeResult())

	for _, tc := range []struct {
		name string
		fn   func(context.Context) (json.RawMessage, error)
	}{
		{"account/read", srv.AccountRead},
		{"account/rateLimits/read", srv.AccountRateLimitsRead},
		{"account/usage/read", srv.AccountUsageRead},
	} {
		raw, err := tc.fn(ctx)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		pretty, _ := json.MarshalIndent(raw, "", "  ")
		t.Logf("%s\n%s", tc.name, pretty)
	}
}
