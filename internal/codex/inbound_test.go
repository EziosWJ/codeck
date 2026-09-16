package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSessionInboundSplitsInjectedBlocks(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "session_inbound.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := parseSessionInbound(f)
	if err != nil {
		t.Fatalf("parseSessionInbound: %v", err)
	}
	if len(got.Blocks) != 4 {
		t.Fatalf("blocks = %d, want 4 (assistant skipped): %+v", len(got.Blocks), got.Blocks)
	}
	want := []string{kindSkills, kindPermissions, kindEnvironment, kindUser}
	for i, kind := range want {
		if got.Blocks[i].Kind != kind {
			t.Errorf("block %d kind = %q, want %q", i, got.Blocks[i].Kind, kind)
		}
	}
	if got.Blocks[3].Text != "Reply with the single word: ok" {
		t.Errorf("user text = %q", got.Blocks[3].Text)
	}
	if got.Settings == nil || got.Settings.Model != "gpt-5.6-luna" || got.Settings.Effort != "low" {
		t.Errorf("settings = %+v", got.Settings)
	}
}

func TestParsePromptInputJSONClassifiesByTag(t *testing.T) {
	raw := []byte(`[
	  {"type":"message","role":"developer","content":[{"type":"input_text","text":"<skills_instructions>\n## Skills"}]},
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"<recommended_plugins>\n- A"}]},
	  {"type":"message","role":"user","content":[{"type":"input_text","text":"Reply with the single word: ok"}]}
	]`)
	got, err := parsePromptInputJSON(raw)
	if err != nil {
		t.Fatalf("parsePromptInputJSON: %v", err)
	}
	if len(got.Blocks) != 3 {
		t.Fatalf("blocks = %d, want 3", len(got.Blocks))
	}
	if got.Blocks[0].Kind != kindSkills || got.Blocks[1].Kind != kindPluginRecs || got.Blocks[2].Kind != kindUser {
		t.Errorf("kinds = %q %q %q", got.Blocks[0].Kind, got.Blocks[1].Kind, got.Blocks[2].Kind)
	}
}

func TestParseSessionUsageKeepsCachedAndRawFields(t *testing.T) {
	raw := `{"type":"token_usage_record","payload":{"usage":{"input_tokens":9394,"cached_input_tokens":8960,"cache_write_input_tokens":0,"output_tokens":20,"reasoning_output_tokens":0,"total_tokens":9414}}}
{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":9394,"cached_input_tokens":8960,"output_tokens":20,"total_tokens":9414}}}}
`
	got, ok := parseSessionUsage(strings.NewReader(raw))
	if !ok {
		t.Fatal("expected usage")
	}
	if got.InputTokens != 9394 || got.CachedInputTokens != 8960 || got.OutputTokens != 20 || got.TotalTokens != 9414 {
		t.Fatalf("usage = %+v", got)
	}
	if !strings.Contains(string(got.Raw), `"cached_input_tokens":8960`) {
		t.Errorf("raw JSON lost cached_input_tokens: %s", got.Raw)
	}
}

func TestUsageRoundTripPreservesUnknownFields(t *testing.T) {
	in := []byte(`{"input_tokens":10,"cached_input_tokens":4,"output_tokens":2,"mystery_tokens":9}`)
	var u Usage
	if err := json.Unmarshal(in, &u); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"mystery_tokens":9`) {
		t.Errorf("unknown field dropped: %s", out)
	}
	if u.CachedInputTokens != 4 {
		t.Errorf("cached = %d", u.CachedInputTokens)
	}
}

func TestFindSessionFileMatchesThreadID(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "09", "16")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "rollout-2026-09-16T21-02-05-thread-abc.jsonl"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findSessionFile(root, "thread-abc")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, name) {
		t.Errorf("findSessionFile = %q, want suffix %s", got, name)
	}
	missing, err := findSessionFile(root, "no-such-thread")
	if err != nil || missing != "" {
		t.Errorf("missing thread: path=%q err=%v", missing, err)
	}
}
