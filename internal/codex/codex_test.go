package codex

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testService(t *testing.T, authSource string) *Service {
	t.Helper()
	root := t.TempDir()
	return NewService("codex",
		filepath.Join(root, "home"),
		filepath.Join(root, "ws"),
		authSource, time.Minute,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestGenerateConfigMinimalMode(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{
		Name: "minimal", Model: "gpt-5.6-luna", ReasoningEffort: "low",
		SandboxMode: "read-only", IsMinimal: true,
	})

	for _, want := range []string{
		`model = "gpt-5.6-luna"`,
		`model_reasoning_effort = "low"`,
		`sandbox_mode = "read-only"`,
		`approval_policy = "never"`,
		`include_permissions_instructions = false`,
		`include_apps_instructions = false`,
		`include_collaboration_mode_instructions = false`,
		`include_environment_context = false`,
		"[skills]",
		"include_instructions = false",
		"[features]",
		"apps = false",
		"plugins = false",
		"computer_use = false",
		"browser_use = false",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("minimal config missing %q\n%s", want, cfg)
		}
	}
	// The isolation guarantee: nothing pulls in MCP servers or skills.
	if strings.Contains(cfg, "mcp_servers") {
		t.Errorf("minimal config must not declare mcp_servers\n%s", cfg)
	}
}

func TestGenerateConfigNonMinimalOmitsFeatureBlock(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{Name: "full", SandboxMode: "workspace-write"})
	if strings.Contains(cfg, "[features]") {
		t.Errorf("non-minimal config should not disable features\n%s", cfg)
	}
	if strings.Contains(cfg, "[skills]") {
		t.Errorf("non-minimal config should not force skills.include_instructions off\n%s", cfg)
	}
	if !strings.Contains(cfg, `sandbox_mode = "workspace-write"`) {
		t.Errorf("sandbox mode not written\n%s", cfg)
	}
}

func TestGenerateConfigWritesExplicitDefaults(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{Name: "blank"})
	for _, want := range []string{
		`model_reasoning_effort = "low"`,
		`approval_policy = "never"`,
		`sandbox_mode = "read-only"`,
		`include_permissions_instructions = false`,
		`include_apps_instructions = false`,
		`include_collaboration_mode_instructions = false`,
		`include_environment_context = false`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("default config missing %q\n%s", want, cfg)
		}
	}

	legacy := GenerateConfig(ProfileSpec{Name: "old", ReasoningEffort: "minimal", ApprovalPolicy: ""})
	if !strings.Contains(legacy, `model_reasoning_effort = "low"`) {
		t.Errorf("minimal effort should become low\n%s", legacy)
	}
	if !strings.Contains(legacy, `approval_policy = "never"`) {
		t.Errorf("empty approval should become never\n%s", legacy)
	}
}

func TestGenerateConfigMinimalForcesIncludeOffAndSkills(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{
		Name:                                 "m",
		IsMinimal:                            true,
		IncludePermissionsInstructions:       true,
		IncludeAppsInstructions:              true,
		IncludeCollaborationModeInstructions: true,
		IncludeEnvironmentContext:            true,
	})
	for _, want := range []string{
		`include_permissions_instructions = false`,
		`include_apps_instructions = false`,
		`include_collaboration_mode_instructions = false`,
		`include_environment_context = false`,
		"[skills]",
		"include_instructions = false",
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("minimal config missing %q\n%s", want, cfg)
		}
	}
}

func TestGenerateConfigIncludeFlags(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{
		Name:                                 "x",
		IncludePermissionsInstructions:       true,
		IncludeAppsInstructions:              true,
		IncludeCollaborationModeInstructions: true,
		IncludeEnvironmentContext:            true,
	})
	for _, want := range []string{
		`include_permissions_instructions = true`,
		`include_apps_instructions = true`,
		`include_collaboration_mode_instructions = true`,
		`include_environment_context = true`,
	} {
		if !strings.Contains(cfg, want) {
			t.Errorf("config missing %q\n%s", want, cfg)
		}
	}
}

func TestGenerateConfigIncludesExtraConfig(t *testing.T) {
	cfg := GenerateConfig(ProfileSpec{
		Name: "x", SandboxMode: "read-only",
		ExtraConfig: `[mcp_servers.demo]
command = "demo"`,
	})
	if !strings.Contains(cfg, "[mcp_servers.demo]") {
		t.Errorf("extra config not appended verbatim\n%s", cfg)
	}
}

func TestEnsureHomeWritesConfigAndLinksAuth(t *testing.T) {
	authDir := t.TempDir()
	authPath := filepath.Join(authDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := testService(t, authPath)

	spec := ProfileSpec{Name: "minimal", SandboxMode: "read-only", IsMinimal: true}
	home, err := svc.EnsureHome(spec)
	if err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "config.toml")); err != nil {
		t.Errorf("config.toml not written: %v", err)
	}
	// The credential must be a link, never a copy.
	info, err := os.Lstat(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("auth.json missing: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("auth.json should be a symlink so tokens are never duplicated")
	}
	if target, _ := os.Readlink(filepath.Join(home, "auth.json")); target != authPath {
		t.Errorf("auth.json -> %q, want %q", target, authPath)
	}
	if _, err := os.Stat(svc.WorkspaceDir(spec)); err != nil {
		t.Errorf("workspace not created: %v", err)
	}

	// Re-provisioning must be idempotent and must not error on the existing link.
	if _, err := svc.EnsureHome(spec); err != nil {
		t.Fatalf("second EnsureHome: %v", err)
	}
}

func TestEnsureHomeToleratesMissingAuth(t *testing.T) {
	svc := testService(t, filepath.Join(t.TempDir(), "does-not-exist.json"))
	spec := ProfileSpec{Name: "noauth", SandboxMode: "read-only"}

	home, err := svc.EnsureHome(spec)
	if err != nil {
		t.Fatalf("missing credentials must not be fatal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Error("no auth.json should have been linked")
	}
}

func TestEnsureHomeRejectsUnsafeNames(t *testing.T) {
	svc := testService(t, "")
	for _, name := range []string{"", "..", "../evil", "a/b", `a\b`} {
		if _, err := svc.EnsureHome(ProfileSpec{Name: name}); err == nil {
			t.Errorf("EnsureHome(%q) should have failed", name)
		}
	}
}

func TestWorkspaceDirPrefersExplicitWorkDir(t *testing.T) {
	svc := testService(t, "")
	if got := svc.WorkspaceDir(ProfileSpec{Name: "p", WorkDir: "/custom/dir"}); got != "/custom/dir" {
		t.Errorf("WorkspaceDir = %q, want /custom/dir", got)
	}
	if got := svc.WorkspaceDir(ProfileSpec{Name: "p"}); !strings.HasSuffix(got, filepath.Join("ws", "p")) {
		t.Errorf("WorkspaceDir = %q, want it under the workspace root", got)
	}
}

func TestRemoveHome(t *testing.T) {
	svc := testService(t, "")
	spec := ProfileSpec{Name: "gone", SandboxMode: "read-only"}
	home, err := svc.EnsureHome(spec)
	if err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if err := svc.RemoveHome(spec.Name); err != nil {
		t.Fatalf("RemoveHome: %v", err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Error("codex home still present after RemoveHome")
	}
}

func TestBuildArgsNewTurnPassesSandboxButResumeCannot(t *testing.T) {
	svc := testService(t, "")
	spec := ProfileSpec{Name: "p", SandboxMode: "workspace-write"}

	fresh := strings.Join(svc.buildArgs(RunOptions{Spec: spec}), " ")
	for _, want := range []string{"exec", "--json", "--skip-git-repo-check", "-s workspace-write"} {
		if !strings.Contains(fresh, want) {
			t.Errorf("fresh args missing %q: %s", want, fresh)
		}
	}
	if strings.HasSuffix(fresh, "resume --json") {
		t.Errorf("fresh turn must not resume: %s", fresh)
	}

	resumed := strings.Join(svc.buildArgs(RunOptions{Spec: spec, ThreadID: "abc-123"}), " ")
	if !strings.Contains(resumed, "resume") || !strings.Contains(resumed, "abc-123") {
		t.Errorf("resume args wrong: %s", resumed)
	}
	// `codex exec resume` rejects -s, so it must not be present.
	if strings.Contains(resumed, "-s ") {
		t.Errorf("resume args must not carry -s: %s", resumed)
	}
}

func TestChildEnvReplacesCodexHome(t *testing.T) {
	svc := testService(t, "")
	t.Setenv("CODEX_HOME", "/should/be/replaced")
	env := svc.childEnv("/profile/home")

	var found int
	for _, kv := range env {
		if strings.HasPrefix(kv, "CODEX_HOME=") {
			found++
			if kv != "CODEX_HOME=/profile/home" {
				t.Errorf("CODEX_HOME = %q, want the profile home", kv)
			}
		}
	}
	if found != 1 {
		t.Errorf("expected exactly one CODEX_HOME entry, got %d", found)
	}
}

func TestScanEventsParsesProgressAndSkipsNoise(t *testing.T) {
	stream := `
WARNING: proceeding, even though we could not create PATH aliases
{"type":"thread.started","thread_id":"t-1"}
{"type":"turn.started"}
{"type":"item.started","item":{"id":"i0","type":"command_execution","command":"ls","aggregated_output":"","status":"in_progress"}}
{"type":"item.completed","item":{"id":"i0","type":"command_execution","command":"ls","aggregated_output":"file.txt","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"all done"}}
{"type":"turn.completed","usage":{"input_tokens":123,"cached_input_tokens":40,"output_tokens":7,"total_tokens":130}}
{"type":"token_count","info":{"last_token_usage":{"input_tokens":123,"cached_input_tokens":40,"output_tokens":7,"total_tokens":130}}}
this line is not json either
`
	var events []Event
	if err := scanEvents(strings.NewReader(stream), func(ev Event) { events = append(events, ev) }); err != nil {
		t.Fatalf("scanEvents: %v", err)
	}
	if len(events) != 7 {
		t.Fatalf("got %d events, want 7", len(events))
	}
	if events[0].Type != EventThreadStarted || events[0].ThreadID != "t-1" {
		t.Errorf("thread event wrong: %+v", events[0])
	}
	if events[5].Usage == nil || events[5].Usage.InputTokens != 123 || events[5].Usage.CachedInputTokens != 40 {
		t.Errorf("usage not parsed: %+v", events[5])
	}
	if got := eventUsage(events[6]); got == nil || got.CachedInputTokens != 40 || got.TotalTokens != 130 {
		t.Errorf("token_count usage not parsed: %+v", events[6].Info)
	}

	item := events[4].Item
	if item == nil || !item.IsMessage() || item.Text != "all done" {
		t.Fatalf("agent message not parsed: %+v", item)
	}
	if item.Kind() != "message" {
		t.Errorf("Kind = %q, want message", item.Kind())
	}

	cmdItem := events[3].Item
	if cmdItem.Kind() != "command" {
		t.Errorf("Kind = %q, want command", cmdItem.Kind())
	}
	if desc := cmdItem.Describe(); !strings.Contains(desc, "file.txt") || !strings.Contains(desc, "exit 0") {
		t.Errorf("Describe = %q, want command output and exit code", desc)
	}
}

func TestFlattenCodexErrorUnwrapsAPIEnvelope(t *testing.T) {
	raw := `{
  "type": "error",
  "error": {
    "type": "invalid_request_error",
    "code": "unsupported_value",
    "message": "Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model. Supported values are: 'none', 'low', 'medium', 'high', 'xhigh', and 'max'.",
    "param": "reasoning.effort"
  },
  "status": 400
}`
	got := flattenCodexError(raw)
	if !strings.Contains(got, "not supported with the 'gpt-5.6-luna' model") {
		t.Errorf("flattenCodexError = %q, want the inner API message", got)
	}
	if !strings.Contains(got, "reasoning.effort") {
		t.Errorf("flattenCodexError = %q, want the param", got)
	}
	if flattenCodexError("plain text") != "plain text" {
		t.Error("plain errors must pass through")
	}
}

func TestEventErrorTextFromStdoutJSONL(t *testing.T) {
	stream := `
{"type":"thread.started","thread_id":"t-1"}
{"type":"turn.started"}
{"type":"error","message":"{\"type\":\"error\",\"error\":{\"message\":\"Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model.\",\"param\":\"reasoning.effort\"}}"}
{"type":"turn.failed","error":{"message":"{\"type\":\"error\",\"error\":{\"message\":\"Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model.\",\"param\":\"reasoning.effort\"}}"}}
`
	var errs []string
	if err := scanEvents(strings.NewReader(stream), func(ev Event) {
		if msg := eventErrorText(ev); msg != "" {
			errs = append(errs, msg)
		}
	}); err != nil {
		t.Fatalf("scanEvents: %v", err)
	}
	joined := uniqueJoin(errs)
	if !strings.Contains(joined, "not supported with the 'gpt-5.6-luna' model") {
		t.Errorf("stream errors = %q, want unwrapped API message", joined)
	}
}

func TestFailureDetailPrefersJSONLOverExitStatus(t *testing.T) {
	waitErr := errors.New("exit status 1")
	got := failureDetail(waitErr, "", []string{
		"Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model. (reasoning.effort)",
		"Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model. (reasoning.effort)",
	})
	if got != "Unsupported value: 'minimal' is not supported with the 'gpt-5.6-luna' model. (reasoning.effort)" {
		t.Errorf("failureDetail = %q", got)
	}
	if failureDetail(waitErr, "", nil) != "exit status 1" {
		t.Errorf("empty stream should fall back to waitErr")
	}
}

func TestScanEventsHandlesEmptyAndGarbageOnlyInput(t *testing.T) {
	var events []Event
	if err := scanEvents(strings.NewReader(""), func(ev Event) { events = append(events, ev) }); err != nil {
		t.Fatalf("scanEvents on empty input: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no events, got %d", len(events))
	}
}

// TestIntegrationRealCodex exercises the actual Codex CLI, including multi-turn
// resume through the generated profile home. It is opt-in because it calls a
// paid API:
//
//	CODECK_INTEGRATION=1 go test ./internal/codex/ -run Integration -v
func TestIntegrationRealCodex(t *testing.T) {
	if os.Getenv("CODECK_INTEGRATION") != "1" {
		t.Skip("set CODECK_INTEGRATION=1 to run against the real Codex CLI")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService("codex",
		filepath.Join(t.TempDir(), "home"),
		filepath.Join(t.TempDir(), "ws"),
		filepath.Join(home, ".codex", "auth.json"),
		3*time.Minute,
		slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))

	ctx := context.Background()
	if ok, version := svc.Available(ctx); !ok {
		t.Fatalf("codex CLI not available: %s", version)
	} else {
		t.Logf("codex version: %s", version)
	}

	spec := ProfileSpec{
		Name: "integration-minimal", Model: "", ReasoningEffort: "low",
		SandboxMode: "read-only", ApprovalPolicy: "never", IsMinimal: true,
	}

	var sawThread bool
	first, err := svc.Run(ctx, RunOptions{
		Spec:   spec,
		Prompt: "Remember the number 4242. Reply with just: ACK",
		OnUpdate: func(u Update) {
			if u.Kind == UpdateThreadStarted {
				sawThread = true
			}
		},
	})
	if err != nil {
		t.Fatalf("first turn: %v", err)
	}
	if !sawThread || first.ThreadID == "" {
		t.Error("expected a thread id from the stream")
	}
	if !strings.Contains(first.Output, "ACK") {
		t.Errorf("first turn output = %q, want it to contain ACK", first.Output)
	}
	if first.Usage.InputTokens == 0 {
		t.Error("expected non-zero input tokens")
	}
	t.Logf("turn 1: %q, in=%d out=%d, %s", first.Output, first.Usage.InputTokens, first.Usage.OutputTokens, first.Duration)

	second, err := svc.Run(ctx, RunOptions{
		Spec:     spec,
		ThreadID: first.ThreadID,
		Prompt:   "What number did I ask you to remember? Reply with just the number.",
	})
	if err != nil {
		t.Fatalf("resume turn: %v", err)
	}
	if !strings.Contains(second.Output, "4242") {
		t.Errorf("resume lost context: output = %q, want it to contain 4242", second.Output)
	}
	if second.ThreadID != first.ThreadID {
		t.Errorf("thread id changed across resume: %q -> %q", first.ThreadID, second.ThreadID)
	}
	t.Logf("turn 2: %q, in=%d out=%d, %s", second.Output, second.Usage.InputTokens, second.Usage.OutputTokens, second.Duration)
}
