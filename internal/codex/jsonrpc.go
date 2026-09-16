package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// ErrRPCClosed is returned when a call is made after the JSON-RPC client has
// been closed or the underlying stream has ended.
var ErrRPCClosed = errors.New("json-rpc client closed")

// RPCError is a JSON-RPC error object from a matching response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return "json-rpc error"
	}
	if e.Code == 0 {
		return "json-rpc error: " + e.Message
	}
	return fmt.Sprintf("json-rpc error %d: %s", e.Code, e.Message)
}

// Notification is a JSON-RPC message with a method and no request id.
type Notification struct {
	Method string
	Params json.RawMessage
}

type rpcReply struct {
	result json.RawMessage
	err    error
}

type inbound struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// RPCClient is a JSON-RPC client over a JSONL stream. Codex App Server uses
// this shape without a `jsonrpc` version field, so none is sent or required.
type RPCClient struct {
	w io.Writer

	wmu     sync.Mutex
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan rpcReply
	onNote  func(Notification)

	closed atomic.Bool
	done   chan struct{}
	err    error
}

// NewRPCClient starts a read loop on r. w is used for outbound messages.
// onNote, when non-nil, receives notifications (messages with a method and no
// id). It must not block for long; it is called from the read goroutine.
func NewRPCClient(r io.Reader, w io.Writer, onNote func(Notification)) *RPCClient {
	c := &RPCClient{
		w:       w,
		pending: make(map[int64]chan rpcReply),
		onNote:  onNote,
		done:    make(chan struct{}),
	}
	go c.readLoop(r)
	return c
}

// Call sends a request, generates an id, and waits for the matching response.
func (c *RPCClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method == "" {
		return nil, fmt.Errorf("json-rpc method is required")
	}
	if c.closed.Load() {
		return nil, ErrRPCClosed
	}

	id, ch := c.register()
	msg := map[string]any{"id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.write(msg); err != nil {
		c.unregister(id)
		return nil, err
	}

	select {
	case <-ctx.Done():
		c.unregister(id)
		return nil, ctx.Err()
	case <-c.done:
		c.unregister(id)
		return nil, c.closeErr()
	case reply := <-ch:
		if reply.err != nil {
			return nil, fmt.Errorf("%s: %w", method, reply.err)
		}
		return reply.result, nil
	}
}

// Notify sends a JSON-RPC notification (no id, no response).
func (c *RPCClient) Notify(method string, params any) error {
	if method == "" {
		return fmt.Errorf("json-rpc method is required")
	}
	if c.closed.Load() {
		return ErrRPCClosed
	}
	msg := map[string]any{"method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.write(msg)
}

// Close fails every in-flight call. It does not close the writer; the owner
// of the stream is responsible for that.
func (c *RPCClient) Close() {
	c.failAll(ErrRPCClosed)
}

// Done is closed when the read loop exits (EOF or a read error).
func (c *RPCClient) Done() <-chan struct{} { return c.done }

func (c *RPCClient) register() (int64, chan rpcReply) {
	ch := make(chan rpcReply, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	return id, ch
}

func (c *RPCClient) unregister(id int64) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *RPCClient) write(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err = c.w.Write(append(b, '\n'))
	return err
}

func (c *RPCClient) readLoop(r io.Reader) {
	defer close(c.done)
	defer c.failAll(c.closeErr())

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || line[0] != '{' {
			continue
		}
		var msg inbound
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		c.dispatch(msg)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		c.setErr(err)
	}
}

func (c *RPCClient) dispatch(msg inbound) {
	id, hasID := parseRequestID(msg.ID)
	if hasID {
		c.mu.Lock()
		ch, ok := c.pending[id]
		if ok {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if !ok {
			// A server-originated request, or a late response after cancel.
			return
		}
		if msg.Error != nil {
			ch <- rpcReply{err: msg.Error}
			return
		}
		ch <- rpcReply{result: msg.Result}
		return
	}

	if msg.Method == "" {
		return
	}
	if c.onNote != nil {
		c.onNote(Notification{Method: msg.Method, Params: msg.Params})
	}
}

func (c *RPCClient) failAll(err error) {
	c.closed.Store(true)
	c.setErr(err)
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[int64]chan rpcReply)
	c.mu.Unlock()
	for _, ch := range pending {
		ch <- rpcReply{err: err}
	}
}

func (c *RPCClient) setErr(err error) {
	if err == nil {
		err = ErrRPCClosed
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *RPCClient) closeErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return ErrRPCClosed
}

// parseRequestID accepts a JSON number or string, matching the protocol's
// RequestId = string | int64. Missing and null ids are not request ids.
func parseRequestID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
