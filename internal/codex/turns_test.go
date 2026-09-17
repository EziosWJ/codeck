package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSessionTurnsJoinsModelByTurnID(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "session_turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseSessionTurns(f)
	if err != nil {
		t.Fatalf("ParseSessionTurns: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("turns = %d, want 3: %+v", len(got), got)
	}
	if got[0].TurnID != "t1" || got[0].Model != "gpt-5.6-luna" {
		t.Errorf("first = %+v", got[0])
	}
	if got[0].Usage.InputTokens != 1000 || got[0].Usage.CachedInputTokens != 400 ||
		got[0].Usage.OutputTokens != 50 || got[0].Usage.ReasoningOutputTokens != 20 {
		t.Errorf("first usage = %+v", got[0].Usage)
	}
	if got[0].Timestamp.UTC() != time.Date(2026, 9, 16, 22, 0, 1, 0, time.UTC) {
		t.Errorf("first ts = %s", got[0].Timestamp.UTC())
	}
	if got[1].Model != "gpt-6-sol" {
		t.Errorf("second model = %q", got[1].Model)
	}
	if got[2].Model != "gpt-6-sol" {
		t.Errorf("orphan should inherit last model, got %q", got[2].Model)
	}
}

func TestParseSessionTurnsUnknownModelWhenNoContext(t *testing.T) {
	raw := `{"timestamp":"2026-09-16T22:00:00Z","type":"token_usage_record","payload":{"turn_id":"x","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}
`
	got, err := ParseSessionTurns(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Model != unknownModel {
		t.Fatalf("got %+v", got)
	}
}
