package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

type rpcPair struct {
	client *RPCClient
	fromC  *bufio.Scanner
	toC    io.Writer
	notes  []Notification
}

func newRPCPair(t *testing.T) *rpcPair {
	t.Helper()
	clientR, serverW := io.Pipe()
	serverR, clientW := io.Pipe()
	p := &rpcPair{fromC: bufio.NewScanner(serverR), toC: serverW}
	p.client = NewRPCClient(clientR, clientW, func(n Notification) {
		p.notes = append(p.notes, n)
	})
	t.Cleanup(func() {
		p.client.Close()
		_ = clientR.Close()
		_ = clientW.Close()
		_ = serverR.Close()
		_ = serverW.Close()
	})
	return p
}

func (p *rpcPair) writeServer(t *testing.T, line string) {
	t.Helper()
	if _, err := io.WriteString(p.toC, line+"\n"); err != nil {
		t.Fatalf("write server line: %v", err)
	}
}

func (p *rpcPair) readRequest(t *testing.T) map[string]any {
	t.Helper()
	if !p.fromC.Scan() {
		t.Fatalf("expected a client request: %v", p.fromC.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(p.fromC.Bytes(), &msg); err != nil {
		t.Fatalf("decode request %q: %v", p.fromC.Text(), err)
	}
	return msg
}

func TestRPCClientMatchesResponseByID(t *testing.T) {
	p := newRPCPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	type result struct {
		raw json.RawMessage
		err error
	}
	got := make(chan result, 1)
	go func() {
		raw, err := p.client.Call(ctx, "account/read", map[string]any{})
		got <- result{raw, err}
	}()

	req := p.readRequest(t)
	if req["method"] != "account/read" {
		t.Fatalf("method = %v, want account/read", req["method"])
	}
	if _, ok := req["jsonrpc"]; ok {
		t.Fatal("must not send a jsonrpc version field")
	}
	id := int64(req["id"].(float64))

	// A notification and a response for a different id must be ignored.
	p.writeServer(t, `{"method":"account/updated","params":{"x":1}}`)
	p.writeServer(t, `{"id":999,"result":{"wrong":true}}`)
	p.writeServer(t, fmt.Sprintf(`{"id":%d,"result":{"account":null,"requiresOpenaiAuth":false}}`, id))

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("Call: %v", r.err)
		}
		if !json.Valid(r.raw) || string(r.raw) != `{"account":null,"requiresOpenaiAuth":false}` {
			t.Fatalf("result = %s", r.raw)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for matching response")
	}

	if len(p.notes) != 1 || p.notes[0].Method != "account/updated" {
		t.Fatalf("notifications = %+v, want account/updated", p.notes)
	}
}

func TestRPCClientSurfacesJSONRPCError(t *testing.T) {
	p := newRPCPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := p.client.Call(ctx, "account/read", map[string]any{})
		errCh <- err
	}()

	req := p.readRequest(t)
	id := int64(req["id"].(float64))
	p.writeServer(t, fmt.Sprintf(`{"id":%d,"error":{"code":-32601,"message":"method not found"}}`, id))

	err := <-errCh
	if err == nil {
		t.Fatal("expected a JSON-RPC error")
	}
	var rpcErr *RPCError
	if !errors.As(err, &rpcErr) {
		t.Fatalf("err = %v (%T), want *RPCError", err, err)
	}
	if rpcErr.Code != -32601 || rpcErr.Message != "method not found" {
		t.Fatalf("RPCError = %+v", rpcErr)
	}
}

func TestRPCClientNotifyOmitsID(t *testing.T) {
	p := newRPCPair(t)
	errCh := make(chan error, 1)
	go func() { errCh <- p.client.Notify("initialized", nil) }()
	msg := p.readRequest(t)
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if msg["method"] != "initialized" {
		t.Fatalf("method = %v", msg["method"])
	}
	if _, ok := msg["id"]; ok {
		t.Fatalf("notification must not include id: %v", msg)
	}
}

func TestRPCClientConcurrentCallsOutOfOrder(t *testing.T) {
	p := newRPCPair(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	results := make([]string, 2)
	errs := make([]error, 2)
	go func() {
		defer wg.Done()
		raw, err := p.client.Call(ctx, "a", nil)
		results[0], errs[0] = string(raw), err
	}()
	go func() {
		defer wg.Done()
		raw, err := p.client.Call(ctx, "b", nil)
		results[1], errs[1] = string(raw), err
	}()

	req1 := p.readRequest(t)
	req2 := p.readRequest(t)
	idA, idB := int64(0), int64(0)
	for _, req := range []map[string]any{req1, req2} {
		id := int64(req["id"].(float64))
		switch req["method"] {
		case "a":
			idA = id
		case "b":
			idB = id
		}
	}
	if idA == 0 || idB == 0 {
		t.Fatalf("missing requests: %v %v", req1, req2)
	}

	// Answer B first so matching cannot be FIFO.
	p.writeServer(t, fmt.Sprintf(`{"id":%d,"result":"b-ok"}`, idB))
	p.writeServer(t, fmt.Sprintf(`{"id":%d,"result":"a-ok"}`, idA))
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if results[0] != `"a-ok"` || results[1] != `"b-ok"` {
		t.Fatalf("results = %q, %q", results[0], results[1])
	}
}

func TestRPCClientCallRespectsCancel(t *testing.T) {
	p := newRPCPair(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := p.client.Call(ctx, "slow", nil)
		errCh <- err
	}()
	_ = p.readRequest(t)
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Call did not return after cancel")
	}
}

func TestParseRequestID(t *testing.T) {
	cases := []struct {
		raw  string
		want int64
		ok   bool
	}{
		{"", 0, false},
		{"null", 0, false},
		{"1", 1, true},
		{`"2"`, 2, true},
		{`"nope"`, 0, false},
	}
	for _, tc := range cases {
		got, ok := parseRequestID(json.RawMessage(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Errorf("parseRequestID(%s) = %d, %v; want %d, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}
