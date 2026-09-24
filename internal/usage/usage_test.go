package usage

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codeck/internal/codex"
	"codeck/internal/store"
)

func TestDefaultPricesLoad(t *testing.T) {
	prices := DefaultPrices()
	if len(prices) != 11 {
		t.Fatalf("seed rows = %d, want 11", len(prices))
	}
	if prices[0].Pattern != "gpt-6-astra" || prices[0].InputUSDPerMTok != 10 {
		t.Fatalf("first seed = %+v", prices[0])
	}
}

func TestMatchExactBeatsGlob(t *testing.T) {
	prices := DefaultPrices()
	p, ok := Match("gpt-5.6-sol", prices)
	if !ok || p.Pattern != "gpt-5.6-sol" {
		t.Fatalf("got %+v ok=%v", p, ok)
	}
	// gpt-6-sol is a released slug with its own row, so it must not fall
	// through to the gpt-*-sol fallback.
	p, ok = Match("gpt-6-sol", prices)
	if !ok || p.Pattern != "gpt-6-sol" || p.InputUSDPerMTok != 2 {
		t.Fatalf("gpt-6-sol = %+v ok=%v", p, ok)
	}
	p, ok = Match("gpt-6-luna", prices)
	if !ok || p.Pattern != "gpt-6-luna" || p.InputUSDPerMTok != 0.1 {
		t.Fatalf("gpt-6-luna = %+v ok=%v", p, ok)
	}
	p, ok = Match("gpt-7-sol", prices)
	if !ok || p.Pattern != "gpt-*-sol" {
		t.Fatalf("future sol = %+v ok=%v", p, ok)
	}
	p, ok = Match("codex-auto-review", prices)
	if ok {
		t.Fatalf("unlisted model matched %s", p.Pattern)
	}
}

func TestCostUSDForGPT6Sol(t *testing.T) {
	p, ok := Match("gpt-6-sol", DefaultPrices())
	if !ok {
		t.Fatal("missing gpt-6-sol")
	}
	// 2000 uncached * 2 + 1000 cached * 0.2 + 500 cache write * 2.5
	// + 300 output * 10, per 1M.
	got := CostUSD(codex.Usage{
		InputTokens: 3000, CachedInputTokens: 1000, CacheWriteInputTokens: 500,
		OutputTokens: 300, TotalTokens: 3300,
	}, p)
	want := 2000/1e6*2 + 1000/1e6*0.2 + 500/1e6*2.5 + 300/1e6*10
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %g, want %g", got, want)
	}

	// Over 272K input tokens the whole request is billed at long rates.
	got = CostUSD(codex.Usage{InputTokens: 300_000, OutputTokens: 100, TotalTokens: 300_100}, p)
	want = 300_000/1e6*4 + 100/1e6*15
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("long cost = %g, want %g", got, want)
	}
}

func TestCostUSDSplitsCachedAndIgnoresReasoning(t *testing.T) {
	p, ok := Match("gpt-5.6-luna", DefaultPrices())
	if !ok {
		t.Fatal("missing luna")
	}
	// 600 uncached * 0.20 + 400 cached * 0.02 + 50 output * 1.20 per 1M
	got := CostUSD(codex.Usage{
		InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 50,
		ReasoningOutputTokens: 20, TotalTokens: 1050,
	}, p)
	want := 600/1e6*0.20 + 400/1e6*0.02 + 50/1e6*1.20
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("cost = %g, want %g", got, want)
	}
}

func TestCostUSDUsesLongRatesOverThreshold(t *testing.T) {
	p, ok := Match("gpt-5.6-luna", DefaultPrices())
	if !ok {
		t.Fatal("missing luna")
	}
	got := CostUSD(codex.Usage{InputTokens: 300_000, OutputTokens: 100, TotalTokens: 300_100}, p)
	want := 300_000/1e6*0.40 + 100/1e6*1.80
	if math.Abs(got-want) > 1e-12 {
		t.Fatalf("long cost = %g, want %g", got, want)
	}
}

func TestSummarizeUnpricedAndDailyUTC(t *testing.T) {
	prices := DefaultPrices()
	ts := time.Date(2026, 9, 16, 22, 0, 1, 0, time.UTC)
	turns := []codex.TurnUsage{
		{Model: "gpt-5.6-luna", Timestamp: ts, Usage: codex.Usage{InputTokens: 1000, CachedInputTokens: 400, OutputTokens: 50, TotalTokens: 1050}},
		{Model: "codex-auto-review", Timestamp: ts, Usage: codex.Usage{InputTokens: 100, OutputTokens: 5, TotalTokens: 105}},
	}
	rep := Summarize(turns, prices, time.UTC, ts)
	if rep.Turns != 2 {
		t.Fatalf("turns = %d", rep.Turns)
	}
	if rep.Summary.UnpricedTurns != 1 || rep.Summary.UnpricedTokens != 105 {
		t.Fatalf("unpriced = %+v", rep.Summary)
	}
	if rep.Summary.USD == nil {
		t.Fatal("summary usd is nil")
	}
	if len(rep.ByModel) != 2 {
		t.Fatalf("by_model = %+v", rep.ByModel)
	}
	if len(rep.Daily) != 1 || rep.Daily[0].Date != "2026-09-16" {
		t.Fatalf("daily = %+v", rep.Daily)
	}
	if rep.Daily[0].USD == nil || *rep.Daily[0].USD <= 0 {
		t.Fatalf("day usd = %+v", rep.Daily[0].USD)
	}
}

func TestScanCollectsJsonlUnderHomes(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026", "09", "16")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(filepath.Join("..", "codex", "testdata", "session_turns.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout.jsonl"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	got := Scan([]string{root})
	if got.Files != 1 || len(got.Turns) != 3 {
		t.Fatalf("scan files=%d turns=%d errors=%v", got.Files, len(got.Turns), got.Errors)
	}
}

func TestHomesDedupsAuthSourceInsideHomeRoot(t *testing.T) {
	root := t.TempDir()
	prof := filepath.Join(root, "luna")
	if err := os.MkdirAll(filepath.Join(prof, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	auth := filepath.Join(prof, "auth.json")
	if err := os.WriteFile(auth, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	homes := Homes(root, auth)
	if len(homes) != 1 {
		t.Fatalf("homes = %v, want 1 (profile == auth parent)", homes)
	}
}

func TestSeedThenRestoreDoesNotDuplicate(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := EnsureSeed(db); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSeed(db); err != nil {
		t.Fatal(err)
	}
	n, err := db.CountModelPrices()
	if err != nil {
		t.Fatal(err)
	}
	if n != 11 {
		t.Fatalf("count after double seed = %d", n)
	}
	extra, err := db.CreateModelPrice(store.ModelPrice{
		Pattern: "custom-x", InputUSDPerMTok: 1, OutputUSDPerMTok: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreDefaults(db); err != nil {
		t.Fatal(err)
	}
	n, _ = db.CountModelPrices()
	if n != 12 {
		t.Fatalf("count after restore = %d, want 12 (kept extra)", n)
	}
	if _, err := db.GetModelPrice(extra.ID); err != nil {
		t.Fatalf("extra row lost: %v", err)
	}
}
