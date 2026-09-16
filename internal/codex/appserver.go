package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	methodInitialize            = "initialize"
	methodInitialized           = "initialized"
	methodAccountRead           = "account/read"
	methodAccountRateLimitsRead = "account/rateLimits/read"
	methodAccountUsageRead      = "account/usage/read"

	appServerInitTimeout = 30 * time.Second
)

// AppServerOptions configures a long-lived `codex app-server` child process.
type AppServerOptions struct {
	Bin           string
	ClientName    string
	ClientVersion string
	Log           *slog.Logger
	// ExtraEnv is appended to the inherited environment. Tests use it to flip
	// the in-process fake server on; production callers leave it empty.
	ExtraEnv []string
}

// InitializeResult is the subset of the `initialize` response we keep.
type InitializeResult struct {
	UserAgent      string `json:"userAgent"`
	CodexHome      string `json:"codexHome"`
	PlatformFamily string `json:"platformFamily"`
	PlatformOs     string `json:"platformOs"`
}

// AppServer is a managed `codex app-server --listen stdio://` process plus a
// JSON-RPC client on its stdin/stdout.
type AppServer struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	rpc    *RPCClient
	log    *slog.Logger
	init   InitializeResult
	cancel context.CancelFunc
	stop   func()

	closeOnce sync.Once
	waitDone  chan struct{}
	waitErr   error
}

// StartAppServer launches the App Server, completes initialize/initialized,
// and returns a client ready for JSON-RPC calls. The process is killed when
// ctx is cancelled or Close is called.
func StartAppServer(ctx context.Context, opt AppServerOptions) (*AppServer, error) {
	if opt.Bin == "" {
		opt.Bin = "codex"
	}
	if opt.ClientName == "" {
		opt.ClientName = "croncodex"
	}
	if opt.ClientVersion == "" {
		opt.ClientVersion = "dev"
	}
	if opt.Log == nil {
		opt.Log = slog.Default()
	}

	runCtx, cancel := context.WithCancel(ctx)
	cmd := exec.Command(opt.Bin, "app-server", "--listen", "stdio://")
	cmd.Env = append(os.Environ(), opt.ExtraEnv...)
	configureProcess(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("app-server stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("app-server stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("app-server stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start codex app-server: %w", err)
	}

	s := &AppServer{
		cmd:      cmd,
		stdin:    stdin,
		log:      opt.Log,
		cancel:   cancel,
		stop:     watchForCancel(runCtx, cmd),
		waitDone: make(chan struct{}),
	}
	s.rpc = NewRPCClient(stdout, stdin, func(n Notification) {
		s.log.Debug("codex app-server notification", "method", n.Method)
	})
	go s.logStderr(stderr)
	go s.reap()

	initCtx, initCancel := context.WithTimeout(runCtx, appServerInitTimeout)
	err = s.handshake(initCtx, opt.ClientName, opt.ClientVersion)
	initCancel()
	if err != nil {
		s.Close()
		return nil, err
	}

	s.log.Info("codex app-server initialized",
		"pid", cmd.Process.Pid,
		"user_agent", s.init.UserAgent,
		"codex_home", s.init.CodexHome)
	return s, nil
}

func (s *AppServer) handshake(ctx context.Context, name, ver string) error {
	raw, err := s.rpc.Call(ctx, methodInitialize, map[string]any{
		"clientInfo": map[string]any{
			"name":    name,
			"title":   "cronCodex",
			"version": ver,
		},
	})
	if err != nil {
		return fmt.Errorf("app-server initialize: %w", err)
	}
	if err := json.Unmarshal(raw, &s.init); err != nil {
		return fmt.Errorf("app-server initialize: decode result: %w", err)
	}
	if err := s.rpc.Notify(methodInitialized, nil); err != nil {
		return fmt.Errorf("app-server initialized: %w", err)
	}
	return nil
}

// Call is a generic JSON-RPC request against the running App Server.
func (s *AppServer) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if s == nil || s.rpc == nil {
		return nil, fmt.Errorf("app-server is not running")
	}
	return s.rpc.Call(ctx, method, params)
}

// AccountRead calls `account/read`. params is required by the protocol, so an
// empty object is sent.
func (s *AppServer) AccountRead(ctx context.Context) (json.RawMessage, error) {
	return s.Call(ctx, methodAccountRead, map[string]any{})
}

// AccountRateLimitsRead calls `account/rateLimits/read`.
func (s *AppServer) AccountRateLimitsRead(ctx context.Context) (json.RawMessage, error) {
	return s.Call(ctx, methodAccountRateLimitsRead, nil)
}

// AccountUsageRead calls `account/usage/read`.
func (s *AppServer) AccountUsageRead(ctx context.Context) (json.RawMessage, error) {
	return s.Call(ctx, methodAccountUsageRead, nil)
}

// InitializeResult returns the handshake payload from `initialize`.
func (s *AppServer) InitializeResult() InitializeResult { return s.init }

// Close stops the App Server child. It is safe to call more than once.
func (s *AppServer) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() {
		s.cancel()
		if s.stdin != nil {
			_ = s.stdin.Close()
		}
		if s.rpc != nil {
			s.rpc.Close()
		}
		select {
		case <-s.waitDone:
		case <-time.After(2 * time.Second):
			killProcessGroup(s.cmd)
			<-s.waitDone
		}
		if s.stop != nil {
			s.stop()
		}
	})
	return s.waitErr
}

func (s *AppServer) reap() {
	s.waitErr = s.cmd.Wait()
	if s.rpc != nil {
		s.rpc.Close()
	}
	close(s.waitDone)
}

func (s *AppServer) logStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		s.log.Warn("codex app-server stderr", "text", line)
	}
}
