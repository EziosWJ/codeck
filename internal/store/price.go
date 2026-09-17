package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// DefaultLongThreshold is the official Standard long-context cutoff: prompts
// with more than this many input tokens are billed at long rates for the
// whole request.
const DefaultLongThreshold = 272_000

// DefaultPricePriority is assigned to user-created rows so they beat the
// seeded glob fallbacks (priority 10) and sit with the seeded exact slugs.
const DefaultPricePriority = 100

// ModelPrice is one row of the equivalent-API price table. Pattern is either
// an exact model slug or a glob with * / ?.
type ModelPrice struct {
	ID                        int64     `json:"id"`
	Pattern                   string    `json:"pattern"`
	InputUSDPerMTok           float64   `json:"input_usd_per_mtok"`
	CachedInputUSDPerMTok     float64   `json:"cached_input_usd_per_mtok"`
	CacheWriteUSDPerMTok      float64   `json:"cache_write_usd_per_mtok"`
	OutputUSDPerMTok          float64   `json:"output_usd_per_mtok"`
	LongInputUSDPerMTok       *float64  `json:"long_input_usd_per_mtok"`
	LongCachedInputUSDPerMTok *float64  `json:"long_cached_input_usd_per_mtok"`
	LongCacheWriteUSDPerMTok  *float64  `json:"long_cache_write_usd_per_mtok"`
	LongOutputUSDPerMTok      *float64  `json:"long_output_usd_per_mtok"`
	LongThresholdTokens       int       `json:"long_threshold_tokens"`
	Priority                  int       `json:"priority"`
	Notes                     string    `json:"notes"`
	CreatedAt                 time.Time `json:"created_at"`
	UpdatedAt                 time.Time `json:"updated_at"`
}

const priceCols = `id, pattern, input_usd_per_mtok, cached_input_usd_per_mtok,
	cache_write_usd_per_mtok, output_usd_per_mtok, long_input_usd_per_mtok,
	long_cached_input_usd_per_mtok, long_cache_write_usd_per_mtok,
	long_output_usd_per_mtok, long_threshold_tokens, priority, notes,
	created_at, updated_at`

func scanPrice(sc interface{ Scan(...any) error }) (ModelPrice, error) {
	var p ModelPrice
	var longIn, longCached, longWrite, longOut sql.NullFloat64
	var created, updated string
	err := sc.Scan(&p.ID, &p.Pattern, &p.InputUSDPerMTok, &p.CachedInputUSDPerMTok,
		&p.CacheWriteUSDPerMTok, &p.OutputUSDPerMTok, &longIn, &longCached, &longWrite,
		&longOut, &p.LongThresholdTokens, &p.Priority, &p.Notes, &created, &updated)
	if err != nil {
		return p, err
	}
	p.LongInputUSDPerMTok = nullFloat(longIn)
	p.LongCachedInputUSDPerMTok = nullFloat(longCached)
	p.LongCacheWriteUSDPerMTok = nullFloat(longWrite)
	p.LongOutputUSDPerMTok = nullFloat(longOut)
	p.CreatedAt, _ = parseTime(created)
	p.UpdatedAt, _ = parseTime(updated)
	return p, nil
}

func nullFloat(n sql.NullFloat64) *float64 {
	if !n.Valid {
		return nil
	}
	v := n.Float64
	return &v
}

func floatArg(v *float64) any {
	if v == nil {
		return nil
	}
	return *v
}

// Validate reports whether the price row can be stored.
func (p *ModelPrice) Validate() error {
	p.Pattern = strings.TrimSpace(p.Pattern)
	p.Notes = strings.TrimSpace(p.Notes)
	if p.Pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	if len(p.Pattern) > 128 {
		return fmt.Errorf("pattern must be at most 128 characters")
	}
	if strings.ContainsAny(p.Pattern, "/\\") {
		return fmt.Errorf("pattern must not contain a path separator")
	}
	for _, r := range p.Pattern {
		if unicode.IsSpace(r) {
			return fmt.Errorf("pattern must not contain whitespace")
		}
	}
	for _, pair := range []struct {
		name string
		v    float64
	}{
		{"input_usd_per_mtok", p.InputUSDPerMTok},
		{"cached_input_usd_per_mtok", p.CachedInputUSDPerMTok},
		{"cache_write_usd_per_mtok", p.CacheWriteUSDPerMTok},
		{"output_usd_per_mtok", p.OutputUSDPerMTok},
	} {
		if pair.v < 0 {
			return fmt.Errorf("%s must be >= 0", pair.name)
		}
	}
	for _, pair := range []struct {
		name string
		v    *float64
	}{
		{"long_input_usd_per_mtok", p.LongInputUSDPerMTok},
		{"long_cached_input_usd_per_mtok", p.LongCachedInputUSDPerMTok},
		{"long_cache_write_usd_per_mtok", p.LongCacheWriteUSDPerMTok},
		{"long_output_usd_per_mtok", p.LongOutputUSDPerMTok},
	} {
		if pair.v != nil && *pair.v < 0 {
			return fmt.Errorf("%s must be >= 0", pair.name)
		}
	}
	if p.LongThresholdTokens <= 0 {
		p.LongThresholdTokens = DefaultLongThreshold
	}
	if p.Priority == 0 && !strings.ContainsAny(p.Pattern, "*?") {
		p.Priority = DefaultPricePriority
	}
	return nil
}

// ListModelPrices returns every price row, highest priority first.
func (d *DB) ListModelPrices() ([]ModelPrice, error) {
	rows, err := d.sql.Query(`SELECT ` + priceCols + ` FROM model_prices
		ORDER BY priority DESC, pattern ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelPrice
	for rows.Next() {
		p, err := scanPrice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountModelPrices reports how many rows are in the table.
func (d *DB) CountModelPrices() (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM model_prices`).Scan(&n)
	return n, err
}

// GetModelPrice loads one row by id.
func (d *DB) GetModelPrice(id int64) (ModelPrice, error) {
	row := d.sql.QueryRow(`SELECT `+priceCols+` FROM model_prices WHERE id = ?`, id)
	p, err := scanPrice(row)
	if err == sql.ErrNoRows {
		return ModelPrice{}, ErrNotFound
	}
	return p, err
}

// CreateModelPrice inserts a row.
func (d *DB) CreateModelPrice(p ModelPrice) (ModelPrice, error) {
	if err := p.Validate(); err != nil {
		return ModelPrice{}, err
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`
INSERT INTO model_prices (pattern, input_usd_per_mtok, cached_input_usd_per_mtok,
    cache_write_usd_per_mtok, output_usd_per_mtok, long_input_usd_per_mtok,
    long_cached_input_usd_per_mtok, long_cache_write_usd_per_mtok,
    long_output_usd_per_mtok, long_threshold_tokens, priority, notes,
    created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Pattern, p.InputUSDPerMTok, p.CachedInputUSDPerMTok, p.CacheWriteUSDPerMTok,
		p.OutputUSDPerMTok, floatArg(p.LongInputUSDPerMTok), floatArg(p.LongCachedInputUSDPerMTok),
		floatArg(p.LongCacheWriteUSDPerMTok), floatArg(p.LongOutputUSDPerMTok),
		p.LongThresholdTokens, p.Priority, p.Notes, now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ModelPrice{}, fmt.Errorf("a price for pattern %q already exists", p.Pattern)
		}
		return ModelPrice{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return ModelPrice{}, err
	}
	return d.GetModelPrice(id)
}

// UpdateModelPrice replaces a row.
func (d *DB) UpdateModelPrice(p ModelPrice) (ModelPrice, error) {
	if err := p.Validate(); err != nil {
		return ModelPrice{}, err
	}
	res, err := d.sql.Exec(`
UPDATE model_prices SET pattern = ?, input_usd_per_mtok = ?,
    cached_input_usd_per_mtok = ?, cache_write_usd_per_mtok = ?,
    output_usd_per_mtok = ?, long_input_usd_per_mtok = ?,
    long_cached_input_usd_per_mtok = ?, long_cache_write_usd_per_mtok = ?,
    long_output_usd_per_mtok = ?, long_threshold_tokens = ?, priority = ?,
    notes = ?, updated_at = ?
WHERE id = ?`,
		p.Pattern, p.InputUSDPerMTok, p.CachedInputUSDPerMTok, p.CacheWriteUSDPerMTok,
		p.OutputUSDPerMTok, floatArg(p.LongInputUSDPerMTok), floatArg(p.LongCachedInputUSDPerMTok),
		floatArg(p.LongCacheWriteUSDPerMTok), floatArg(p.LongOutputUSDPerMTok),
		p.LongThresholdTokens, p.Priority, p.Notes, formatTime(time.Now()), p.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return ModelPrice{}, fmt.Errorf("a price for pattern %q already exists", p.Pattern)
		}
		return ModelPrice{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ModelPrice{}, ErrNotFound
	}
	return d.GetModelPrice(p.ID)
}

// DeleteModelPrice removes a row.
func (d *DB) DeleteModelPrice(id int64) error {
	res, err := d.sql.Exec(`DELETE FROM model_prices WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InsertModelPricesIfEmpty writes rows only when the table has none. Used to
// seed official Standard prices without clobbering later edits.
func (d *DB) InsertModelPricesIfEmpty(prices []ModelPrice) error {
	n, err := d.CountModelPrices()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, p := range prices {
		if _, err := d.CreateModelPrice(p); err != nil {
			return err
		}
	}
	return nil
}

// UpsertModelPriceByPattern inserts or updates the row with this pattern.
// Existing ids are kept. Used by "restore defaults".
func (d *DB) UpsertModelPriceByPattern(p ModelPrice) (ModelPrice, error) {
	if err := p.Validate(); err != nil {
		return ModelPrice{}, err
	}
	var id int64
	err := d.sql.QueryRow(`SELECT id FROM model_prices WHERE pattern = ?`, p.Pattern).Scan(&id)
	if err == sql.ErrNoRows {
		return d.CreateModelPrice(p)
	}
	if err != nil {
		return ModelPrice{}, err
	}
	p.ID = id
	return d.UpdateModelPrice(p)
}
