// Package usage scans local Codex session files and prices them against the
// operator-maintained model_prices table. The result is an equivalent API
// cost (Standard list prices), not ChatGPT credit spend.
package usage

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codeck/internal/codex"
	"codeck/internal/store"
)

//go:embed prices_seed.json
var pricesSeedJSON []byte

// DefaultPrices is the official Standard table snapshotted at first install.
func DefaultPrices() []store.ModelPrice {
	var prices []store.ModelPrice
	if err := json.Unmarshal(pricesSeedJSON, &prices); err != nil {
		return nil
	}
	return prices
}

// EnsureSeed inserts DefaultPrices when the table is empty.
func EnsureSeed(db *store.DB) error {
	return db.InsertModelPricesIfEmpty(DefaultPrices())
}

// RestoreDefaults upserts seed rows by pattern and does not delete extra rows
// the operator added.
func RestoreDefaults(db *store.DB) error {
	for _, p := range DefaultPrices() {
		if _, err := db.UpsertModelPriceByPattern(p); err != nil {
			return err
		}
	}
	return nil
}

// Report is the payload GET /api/usage returns.
type Report struct {
	ScannedAt string     `json:"scanned_at"`
	Roots     []string   `json:"roots"`
	Files     int        `json:"files"`
	Turns     int        `json:"turns"`
	Errors    []string   `json:"errors,omitempty"`
	Summary   Totals     `json:"summary"`
	ByModel   []ModelRow `json:"by_model"`
	Daily     []DayRow   `json:"daily"`
}

// Totals is an aggregate across every priced and unpriced turn.
type Totals struct {
	Turns           int      `json:"turns"`
	InputTokens     int      `json:"input_tokens"`
	CachedTokens    int      `json:"cached_tokens"`
	UncachedTokens  int      `json:"uncached_tokens"`
	CacheWrite      int      `json:"cache_write_tokens"`
	OutputTokens    int      `json:"output_tokens"`
	ReasoningTokens int      `json:"reasoning_tokens"`
	TotalTokens     int      `json:"total_tokens"`
	USD             *float64 `json:"usd"`
	UnpricedTurns   int      `json:"unpriced_turns"`
	UnpricedTokens  int      `json:"unpriced_tokens"`
}

// ModelRow is one model in the breakdown.
type ModelRow struct {
	Model     string `json:"model"`
	MatchedBy string `json:"matched_by,omitempty"`
	Priced    bool   `json:"priced"`
	Wildcard  bool   `json:"wildcard"`
	LongTurns int    `json:"long_turns"`
	Totals
}

// DayRow is one local calendar day.
type DayRow struct {
	Date   string        `json:"date"`
	Tokens int           `json:"tokens"`
	USD    *float64      `json:"usd"`
	Models []DayModelRow `json:"models"`
}

// DayModelRow is one model inside a day.
type DayModelRow struct {
	Model  string   `json:"model"`
	Tokens int      `json:"tokens"`
	USD    *float64 `json:"usd"`
}

type bucket struct {
	totals    Totals
	pricedUSD float64
	hasPriced bool
	matchedBy string
	wildcard  bool
	longTurns int
}

// Homes lists Codex home directories that contain a sessions/ folder:
// each profile under homeRoot, plus the directory that holds AUTH_SOURCE.
func Homes(homeRoot, authSource string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(dir string) {
		if dir == "" {
			return
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			abs = dir
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			real = abs
		}
		if seen[real] {
			return
		}
		if _, err := os.Stat(filepath.Join(real, "sessions")); err != nil {
			return
		}
		seen[real] = true
		out = append(out, real)
	}
	if homeRoot != "" {
		entries, err := os.ReadDir(homeRoot)
		if err == nil {
			for _, e := range entries {
				if e.IsDir() {
					add(filepath.Join(homeRoot, e.Name()))
				}
			}
		}
	}
	if authSource != "" {
		add(filepath.Dir(authSource))
	}
	sort.Strings(out)
	return out
}

// ScanResult is the raw walk of session files before pricing.
type ScanResult struct {
	Roots  []string
	Files  int
	Turns  []codex.TurnUsage
	Errors []string
}

// Scan walks sessions/**/*.jsonl under each Codex home.
func Scan(homes []string) ScanResult {
	res := ScanResult{Roots: append([]string(nil), homes...)}
	for _, home := range homes {
		root := filepath.Join(home, "sessions")
		_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				res.Errors = append(res.Errors, path+": "+err.Error())
				return nil
			}
			if d.IsDir() || !strings.HasSuffix(d.Name(), ".jsonl") {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				res.Errors = append(res.Errors, path+": "+err.Error())
				return nil
			}
			turns, err := codex.ParseSessionTurns(f)
			f.Close()
			res.Files++
			if err != nil {
				res.Errors = append(res.Errors, path+": "+err.Error())
			}
			res.Turns = append(res.Turns, turns...)
			return nil
		})
	}
	return res
}

// Match finds the best price row for a model slug. Exact (no glob) still
// respects priority: a higher-priority glob can win, but equal priority
// prefers fewer wildcards.
func Match(model string, prices []store.ModelPrice) (store.ModelPrice, bool) {
	var best store.ModelPrice
	found := false
	bestWild := 0
	for _, p := range prices {
		ok, err := filepath.Match(p.Pattern, model)
		if err != nil || !ok {
			continue
		}
		wild := strings.Count(p.Pattern, "*") + strings.Count(p.Pattern, "?")
		if !found || p.Priority > best.Priority ||
			(p.Priority == best.Priority && wild < bestWild) ||
			(p.Priority == best.Priority && wild == bestWild && len(p.Pattern) > len(best.Pattern)) {
			best = p
			bestWild = wild
			found = true
		}
	}
	return best, found
}

// CostUSD is the Standard equivalent cost of one turn. Reasoning tokens are
// a subset of output and are not added again.
func CostUSD(u codex.Usage, p store.ModelPrice) float64 {
	uncached := u.InputTokens - u.CachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	in, cached, write, out := p.InputUSDPerMTok, p.CachedInputUSDPerMTok, p.CacheWriteUSDPerMTok, p.OutputUSDPerMTok
	if u.InputTokens > p.LongThresholdTokens && p.LongInputUSDPerMTok != nil {
		in = *p.LongInputUSDPerMTok
		if p.LongCachedInputUSDPerMTok != nil {
			cached = *p.LongCachedInputUSDPerMTok
		}
		if p.LongCacheWriteUSDPerMTok != nil {
			write = *p.LongCacheWriteUSDPerMTok
		}
		if p.LongOutputUSDPerMTok != nil {
			out = *p.LongOutputUSDPerMTok
		}
	}
	const million = 1_000_000.0
	return float64(uncached)/million*in +
		float64(u.CachedInputTokens)/million*cached +
		float64(u.CacheWriteInputTokens)/million*write +
		float64(u.OutputTokens)/million*out
}

// Summarize prices turns and groups them by model and local calendar day.
func Summarize(turns []codex.TurnUsage, prices []store.ModelPrice, loc *time.Location, scannedAt time.Time) Report {
	if loc == nil {
		loc = time.UTC
	}
	byModel := map[string]*bucket{}
	byDayModel := map[string]map[string]*bucket{}

	add := func(b *bucket, t codex.TurnUsage, p store.ModelPrice, priced bool) {
		u := t.Usage
		uncached := u.InputTokens - u.CachedInputTokens
		if uncached < 0 {
			uncached = 0
		}
		b.totals.Turns++
		b.totals.InputTokens += u.InputTokens
		b.totals.CachedTokens += u.CachedInputTokens
		b.totals.UncachedTokens += uncached
		b.totals.CacheWrite += u.CacheWriteInputTokens
		b.totals.OutputTokens += u.OutputTokens
		b.totals.ReasoningTokens += u.ReasoningOutputTokens
		b.totals.TotalTokens += u.TotalTokens
		if !priced {
			b.totals.UnpricedTurns++
			b.totals.UnpricedTokens += u.TotalTokens
			return
		}
		if u.InputTokens > p.LongThresholdTokens && p.LongInputUSDPerMTok != nil {
			b.longTurns++
		}
		b.pricedUSD += CostUSD(u, p)
		b.hasPriced = true
	}

	for _, t := range turns {
		p, priced := Match(t.Model, prices)
		b := byModel[t.Model]
		if b == nil {
			b = &bucket{}
			if priced {
				b.matchedBy = p.Pattern
				b.wildcard = strings.ContainsAny(p.Pattern, "*?")
			}
			byModel[t.Model] = b
		}
		add(b, t, p, priced)

		day := "unknown"
		if !t.Timestamp.IsZero() {
			day = t.Timestamp.In(loc).Format("2006-01-02")
		}
		models := byDayModel[day]
		if models == nil {
			models = map[string]*bucket{}
			byDayModel[day] = models
		}
		db := models[t.Model]
		if db == nil {
			db = &bucket{}
			models[t.Model] = db
		}
		add(db, t, p, priced)
	}

	rep := Report{
		ScannedAt: scannedAt.UTC().Format(time.RFC3339Nano),
		Turns:     len(turns),
		ByModel:   make([]ModelRow, 0, len(byModel)),
		Daily:     make([]DayRow, 0, len(byDayModel)),
	}

	var summary bucket
	for model, b := range byModel {
		row := modelRow(model, b)
		rep.ByModel = append(rep.ByModel, row)
		mergeBucket(&summary, b)
	}
	sort.Slice(rep.ByModel, func(i, j int) bool {
		return rep.ByModel[i].TotalTokens > rep.ByModel[j].TotalTokens
	})
	rep.Summary = finishTotals(summary)

	days := make([]string, 0, len(byDayModel))
	for d := range byDayModel {
		days = append(days, d)
	}
	sort.Strings(days)
	for _, d := range days {
		models := byDayModel[d]
		day := DayRow{Date: d, Models: make([]DayModelRow, 0, len(models))}
		var dayUSD float64
		hasUSD := false
		names := make([]string, 0, len(models))
		for m := range models {
			names = append(names, m)
		}
		sort.Strings(names)
		for _, m := range names {
			b := models[m]
			day.Tokens += b.totals.TotalTokens
			item := DayModelRow{Model: m, Tokens: b.totals.TotalTokens}
			if b.hasPriced {
				item.USD = floatPtr(b.pricedUSD)
				dayUSD += b.pricedUSD
				hasUSD = true
			}
			day.Models = append(day.Models, item)
		}
		if hasUSD {
			day.USD = floatPtr(dayUSD)
		}
		rep.Daily = append(rep.Daily, day)
	}
	return rep
}

func modelRow(model string, b *bucket) ModelRow {
	return ModelRow{
		Model:     model,
		MatchedBy: b.matchedBy,
		Priced:    b.hasPriced,
		Wildcard:  b.wildcard,
		LongTurns: b.longTurns,
		Totals:    finishTotals(*b),
	}
}

func mergeBucket(dst, src *bucket) {
	dst.totals.Turns += src.totals.Turns
	dst.totals.InputTokens += src.totals.InputTokens
	dst.totals.CachedTokens += src.totals.CachedTokens
	dst.totals.UncachedTokens += src.totals.UncachedTokens
	dst.totals.CacheWrite += src.totals.CacheWrite
	dst.totals.OutputTokens += src.totals.OutputTokens
	dst.totals.ReasoningTokens += src.totals.ReasoningTokens
	dst.totals.TotalTokens += src.totals.TotalTokens
	dst.totals.UnpricedTurns += src.totals.UnpricedTurns
	dst.totals.UnpricedTokens += src.totals.UnpricedTokens
	dst.pricedUSD += src.pricedUSD
	if src.hasPriced {
		dst.hasPriced = true
	}
}

func finishTotals(b bucket) Totals {
	t := b.totals
	if b.hasPriced {
		t.USD = floatPtr(b.pricedUSD)
	}
	return t
}

func floatPtr(v float64) *float64 { return &v }
