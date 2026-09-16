// Package codex is the only place in this program that executes the Codex CLI.
// Every invocation is isolated inside a per-profile CODEX_HOME so a profile's
// configuration can never be contaminated by, or contaminate, the operator's
// own ~/.codex setup.
package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Update kinds emitted to a streaming caller.
const (
	UpdateThreadStarted = "thread_started"
	UpdateProgress      = "progress"
	UpdateMessage       = "message"
	UpdateUsage         = "usage"
	UpdateError         = "error"
)

// Update is an incremental notification produced while a run is in flight.
type Update struct {
	Kind     string
	ThreadID string
	// Text is the assistant text (KindMessage) or the note body (KindProgress).
	Text string
	// ItemKind classifies a KindProgress note: command, reasoning, tool, …
	ItemKind string
	// Message carries an error description for KindError.
	Message string
	Usage   *Usage
}

// Result is the outcome of one Codex turn.
type Result struct {
	ThreadID string
	// Output is the concatenated assistant text for the turn.
	Output   string
	Messages []string
	Usage    Usage
	Duration time.Duration
}

// RunOptions describes a single Codex invocation.
type RunOptions struct {
	Spec   ProfileSpec
	Prompt string
	// ThreadID, when set, resumes an existing Codex session.
	ThreadID string
	Timeout  time.Duration
	// OnUpdate, when non-nil, receives progress as it happens. It must not
	// block for long; it is called from the process-reading goroutine.
	OnUpdate func(Update)
}

// Service runs the Codex CLI against isolated per-profile homes.
type Service struct {
	bin            string
	homeRoot       string
	workspaceRoot  string
	authSource     string
	defaultTimeout time.Duration
	log            *slog.Logger

	// homeMu serialises CODEX_HOME provisioning so two concurrent runs for the
	// same profile cannot interleave writes to config.toml.
	homeMu sync.Mutex
}

// NewService constructs a Service. bin may be a bare name resolved via PATH.
func NewService(bin, homeRoot, workspaceRoot, authSource string, defaultTimeout time.Duration, log *slog.Logger) *Service {
	return &Service{
		bin:            bin,
		homeRoot:       homeRoot,
		workspaceRoot:  workspaceRoot,
		authSource:     authSource,
		defaultTimeout: defaultTimeout,
		log:            log,
	}
}

// Bin reports the configured codex executable.
func (s *Service) Bin() string { return s.bin }

// Available reports whether the Codex CLI can be found and its version string.
func (s *Service) Available(ctx context.Context) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, s.bin, "--version").Output()
	if err != nil {
		return false, ""
	}
	return true, strings.TrimSpace(string(out))
}

// Run executes one turn and returns when Codex finishes. Progress is reported
// through opts.OnUpdate when supplied.
func (s *Service) Run(ctx context.Context, opts RunOptions) (Result, error) {
	started := time.Now()
	result := Result{ThreadID: opts.ThreadID}

	spec := opts.Spec
	home, err := s.ensureHome(spec)
	if err != nil {
		return result, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = s.defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := s.buildArgs(opts)
	cmd := exec.CommandContext(ctx, s.bin, args...)
	cmd.Dir = s.WorkspaceDir(spec)
	cmd.Env = s.childEnv(home)
	// The prompt is delivered on stdin so arbitrarily long prompts are not
	// constrained by the argument size limit. `-` tells Codex to read it.
	cmd.Stdin = strings.NewReader(opts.Prompt)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, fmt.Errorf("stdout pipe: %w", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	configureProcess(cmd)

	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("start codex: %w", err)
	}
	// Kill the whole process group on timeout: Codex spawns shell children
	// that would otherwise survive and keep consuming the session.
	stopKiller := watchForCancel(ctx, cmd)
	defer stopKiller()

	s.log.Info("codex run started",
		"profile", spec.Name, "resume", opts.ThreadID != "", "args", strings.Join(args, " "))

	var messages []string
	var streamErrs []string
	var usage *Usage
	scanErr := scanEvents(stdout, func(ev Event) {
		if msg := eventErrorText(ev); msg != "" {
			streamErrs = append(streamErrs, msg)
		}
		switch ev.Type {
		case EventThreadStarted:
			result.ThreadID = ev.ThreadID
			emit(opts, Update{Kind: UpdateThreadStarted, ThreadID: ev.ThreadID})
		case EventItemCompleted, EventItemStarted:
			if ev.Item == nil {
				return
			}
			if ev.Item.IsMessage() {
				messages = append(messages, ev.Item.Text)
				emit(opts, Update{Kind: UpdateMessage, Text: ev.Item.Text, ItemKind: ev.Item.Kind()})
				return
			}
			// Only surface finished notes; item.started is a duplicate with no
			// content yet.
			if ev.Type == EventItemCompleted {
				emit(opts, Update{
					Kind:     UpdateProgress,
					Text:     ev.Item.Describe(),
					ItemKind: ev.Item.Kind(),
				})
			}
		case EventError:
			emit(opts, Update{Kind: UpdateError, Message: eventErrorText(ev)})
		case EventTurnCompleted:
			usage = ev.Usage
		}
	})

	// Drain any remainder so the child is never blocked on a full pipe.
	io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	result.Duration = time.Since(started)
	if usage != nil {
		result.Usage = *usage
		emit(opts, Update{Kind: UpdateUsage, Usage: usage})
	}
	result.Messages = messages
	result.Output = strings.Join(messages, "\n\n")

	if ctx.Err() == context.DeadlineExceeded {
		return result, fmt.Errorf("codex timed out after %s", timeout)
	}
	if waitErr != nil {
		detail := failureDetail(waitErr, stderr.String(), streamErrs)
		if scanErr != nil && !errors.Is(scanErr, context.Canceled) {
			detail = detail + "; stream error: " + scanErr.Error()
		}
		// A turn can fail after producing useful text; keep whatever arrived.
		if result.Output != "" {
			return result, fmt.Errorf("codex exited with an error: %s", truncate(detail, 2000))
		}
		return result, fmt.Errorf("codex failed: %s", truncate(detail, 2000))
	}
	if result.Output == "" {
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return result, fmt.Errorf("codex produced no output: %s", truncate(detail, 2000))
		}
	}
	return result, nil
}

// buildArgs assembles the CLI arguments for a new or resumed turn.
func (s *Service) buildArgs(opts RunOptions) []string {
	// --json       : emit JSONL progress, which is what makes streaming possible
	// --skip-git-repo-check : the scratch workspace is intentionally not a repo
	args := []string{"exec"}
	if opts.ThreadID != "" {
		// `resume` accepts neither -s nor -C; the sandbox and working directory
		// are taken from the generated config.toml and the process cwd.
		args = append(args, "resume", "--json", "--skip-git-repo-check", opts.ThreadID, "-")
		return args
	}
	args = append(args,
		"--json",
		"--skip-git-repo-check",
		"-s", opts.Spec.SandboxOrDefault(),
		"-",
	)
	return args
}

func emit(opts RunOptions, u Update) {
	if opts.OnUpdate != nil {
		opts.OnUpdate(u)
	}
}

// ensureHome provisions the profile's CODEX_HOME under a lock.
func (s *Service) ensureHome(spec ProfileSpec) (string, error) {
	s.homeMu.Lock()
	defer s.homeMu.Unlock()
	return s.EnsureHome(spec)
}

// childEnv builds the child environment with CODEX_HOME pointed at the
// profile's directory. The rest of the environment is inherited so PATH,
// proxies and any ambient credentials continue to work.
func (s *Service) childEnv(home string) []string {
	env := os.Environ()
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "CODEX_HOME="+home)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
