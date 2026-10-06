package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// BalanceAmount is a platform reported amount kept as decimal strings so
// monetary precision is not lost to binary floating-point conversions.
type BalanceAmount struct {
	Currency string            `json:"currency"`
	Total    string            `json:"total"`
	Details  map[string]string `json:"details,omitempty"`
}

// BalanceConfig is safe to return from the API. CredentialCiphertext is
// deliberately excluded from JSON responses.
type BalanceConfig struct {
	ID                   int64           `json:"id"`
	Provider             string          `json:"provider"`
	Name                 string          `json:"name"`
	IntervalSeconds      int             `json:"interval_seconds"`
	Balances             []BalanceAmount `json:"balances"`
	LastSuccessAt        string          `json:"last_success_at,omitempty"`
	LastError            string          `json:"last_error,omitempty"`
	CredentialVersion    int64           `json:"-"`
	CredentialConfigured bool            `json:"credential_configured"`
	CredentialCiphertext []byte          `json:"-"`
	CreatedAt            string          `json:"created_at"`
	UpdatedAt            string          `json:"updated_at"`
}

var ErrBalanceInvalid = errors.New("invalid balance configuration")
var ErrBalanceCredentialChanged = errors.New("balance credential changed during refresh")

func (c BalanceConfig) Validate() error {
	if c.Provider != "deepseek" && c.Provider != "openrouter" {
		return fmt.Errorf("%w: provider must be deepseek or openrouter", ErrBalanceInvalid)
	}
	if strings.TrimSpace(c.Name) == "" || len(c.Name) > 120 {
		return fmt.Errorf("%w: name is required and must be at most 120 characters", ErrBalanceInvalid)
	}
	if c.IntervalSeconds < 60 || c.IntervalSeconds > 30*24*60*60 {
		return fmt.Errorf("%w: interval must be between 1 minute and 30 days", ErrBalanceInvalid)
	}
	return nil
}

// CreateBalanceConfig persists a new encrypted credential and starts without
// a cached result.
func (d *DB) CreateBalanceConfig(c BalanceConfig) (BalanceConfig, error) {
	if err := c.Validate(); err != nil {
		return BalanceConfig{}, err
	}
	if len(c.CredentialCiphertext) == 0 {
		return BalanceConfig{}, fmt.Errorf("%w: credential is required", ErrBalanceInvalid)
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`INSERT INTO balance_configs
(provider, name, credential_enc, interval_seconds, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?)`, c.Provider, strings.TrimSpace(c.Name), c.CredentialCiphertext,
		c.IntervalSeconds, now, now)
	if err != nil {
		return BalanceConfig{}, err
	}
	c.ID, err = res.LastInsertId()
	if err != nil {
		return BalanceConfig{}, err
	}
	return d.GetBalanceConfig(c.ID)
}

// ListBalanceConfigs returns configurations in creation order.
func (d *DB) ListBalanceConfigs() ([]BalanceConfig, error) {
	rows, err := d.sql.Query(`SELECT id, provider, name, credential_enc, credential_version,
interval_seconds, balance_json, last_success_at, last_error, created_at, updated_at
FROM balance_configs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var configs []BalanceConfig
	for rows.Next() {
		c, err := scanBalanceConfig(rows)
		if err != nil {
			return nil, err
		}
		configs = append(configs, c)
	}
	return configs, rows.Err()
}

// GetBalanceConfig returns one configuration, including its encrypted key for
// internal use. JSON serialization omits that key.
func (d *DB) GetBalanceConfig(id int64) (BalanceConfig, error) {
	return scanBalanceConfig(d.sql.QueryRow(`SELECT id, provider, name, credential_enc, credential_version,
interval_seconds, balance_json, last_success_at, last_error, created_at, updated_at
FROM balance_configs WHERE id = ?`, id))
}

// UpdateBalanceConfig replaces metadata and optionally the encrypted key. A
// new key invalidates the previous account snapshot until the next success.
func (d *DB) UpdateBalanceConfig(c BalanceConfig, replaceCredential bool) (BalanceConfig, error) {
	if err := c.Validate(); err != nil {
		return BalanceConfig{}, err
	}
	var err error
	if replaceCredential {
		if len(c.CredentialCiphertext) == 0 {
			return BalanceConfig{}, fmt.Errorf("%w: replacement credential is empty", ErrBalanceInvalid)
		}
		_, err = d.sql.Exec(`UPDATE balance_configs SET provider=?, name=?, credential_enc=?,
credential_version=credential_version+1, interval_seconds=?, balance_json='', last_success_at='', last_error='', updated_at=? WHERE id=?`,
			c.Provider, strings.TrimSpace(c.Name), c.CredentialCiphertext, c.IntervalSeconds,
			formatTime(time.Now()), c.ID)
	} else {
		_, err = d.sql.Exec(`UPDATE balance_configs SET provider=?, name=?, interval_seconds=?, updated_at=? WHERE id=?`,
			c.Provider, strings.TrimSpace(c.Name), c.IntervalSeconds, formatTime(time.Now()), c.ID)
	}
	if err != nil {
		return BalanceConfig{}, err
	}
	return d.GetBalanceConfig(c.ID)
}

// DeleteBalanceConfig deletes a configuration and its encrypted credential.
func (d *DB) DeleteBalanceConfig(id int64) error {
	res, err := d.sql.Exec(`DELETE FROM balance_configs WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveBalanceSuccess replaces the last known snapshot and clears any old error.
func (d *DB) SaveBalanceSuccess(id, credentialVersion int64, balances []BalanceAmount, at time.Time) error {
	raw, err := json.Marshal(balances)
	if err != nil {
		return err
	}
	res, err := d.sql.Exec(`UPDATE balance_configs SET balance_json=?, last_success_at=?,
last_error='', updated_at=? WHERE id=? AND credential_version=?`, string(raw), formatTime(at), formatTime(time.Now()), id, credentialVersion)
	return requireBalanceVersion(res, err)
}

// SaveBalanceError records a sanitized error while retaining the last success.
func (d *DB) SaveBalanceError(id, credentialVersion int64, message string) error {
	res, err := d.sql.Exec(`UPDATE balance_configs SET last_error=?, updated_at=? WHERE id=? AND credential_version=?`,
		message, formatTime(time.Now()), id, credentialVersion)
	return requireBalanceVersion(res, err)
}

func requireBalanceVersion(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrBalanceCredentialChanged
	}
	return nil
}

type rowScanner interface{ Scan(...any) error }

func scanBalanceConfig(row rowScanner) (BalanceConfig, error) {
	var c BalanceConfig
	var cached string
	err := row.Scan(&c.ID, &c.Provider, &c.Name, &c.CredentialCiphertext, &c.CredentialVersion,
		&c.IntervalSeconds, &cached, &c.LastSuccessAt, &c.LastError, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BalanceConfig{}, ErrNotFound
		}
		return BalanceConfig{}, err
	}
	c.CredentialConfigured = len(c.CredentialCiphertext) > 0
	c.Balances = []BalanceAmount{}
	if cached != "" {
		if err := json.Unmarshal([]byte(cached), &c.Balances); err != nil {
			return BalanceConfig{}, fmt.Errorf("decode cached balance: %w", err)
		}
	}
	return c, nil
}
