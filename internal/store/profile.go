package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// Profile is a Codex configuration. Each profile is materialised on disk as its
// own CODEX_HOME directory, so profile settings never touch the operator's real
// ~/.codex/config.toml.
type Profile struct {
	ID              int64     `json:"id"`
	Name            string    `json:"name"`
	Description     string    `json:"description"`
	Model           string    `json:"model"`
	ReasoningEffort string    `json:"reasoning_effort"`
	SandboxMode     string    `json:"sandbox_mode"`
	ApprovalPolicy  string    `json:"approval_policy"`
	ExtraConfig     string    `json:"extra_config"`
	WorkDir         string    `json:"work_dir"`
	IsMinimal       bool      `json:"is_minimal"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

const (
	// Defaults written into every new profile and into generated config.toml.
	// Empty "let Codex decide" is not offered: it hides the real value.
	DefaultSandboxMode     = "read-only"
	DefaultApprovalPolicy  = "never"
	DefaultReasoningEffort = "low"
)

var (
	namePattern    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,63}$`)
	validSandbox   = map[string]bool{"read-only": true, "workspace-write": true, "danger-full-access": true}
	validEffort    = map[string]bool{"low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	validApprovals = map[string]bool{"never": true, "on-request": true, "on-failure": true, "untrusted": true}
)

// Validate reports whether the profile's fields are usable. The name is
// restricted because it becomes a directory name under CodexHomeRoot.
func (p *Profile) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.applyDefaults()
	if !namePattern.MatchString(p.Name) {
		return fmt.Errorf("name must start with a letter or digit and contain only letters, digits, dot, dash or underscore (max 64 chars)")
	}
	if !validSandbox[p.SandboxMode] {
		return fmt.Errorf("sandbox_mode must be one of read-only, workspace-write, danger-full-access")
	}
	if !validEffort[p.ReasoningEffort] {
		return fmt.Errorf("reasoning_effort must be one of low, medium, high, xhigh, max")
	}
	if !validApprovals[p.ApprovalPolicy] {
		return fmt.Errorf("approval_policy must be one of never, on-request, on-failure, untrusted")
	}
	if p.IsMinimal && p.RequiresSandboxEscape() {
		return fmt.Errorf("minimal mode cannot use the danger-full-access sandbox")
	}
	return nil
}

func (p *Profile) applyDefaults() {
	if p.SandboxMode == "" {
		p.SandboxMode = DefaultSandboxMode
	}
	if p.ApprovalPolicy == "" {
		p.ApprovalPolicy = DefaultApprovalPolicy
	}
	switch p.ReasoningEffort {
	case "", "none", "minimal":
		p.ReasoningEffort = DefaultReasoningEffort
	}
}

// RequiresSandboxEscape reports whether the profile permits unsandboxed writes.
func (p *Profile) RequiresSandboxEscape() bool {
	return p.SandboxMode == "danger-full-access"
}

// HomeDirName is the directory name used for this profile's CODEX_HOME.
func (p *Profile) HomeDirName() string { return p.Name }

const profileCols = `id, name, description, model, reasoning_effort, sandbox_mode,
	approval_policy, extra_config, work_dir, is_minimal, created_at, updated_at`

func scanProfile(sc interface{ Scan(...any) error }) (Profile, error) {
	var p Profile
	var created, updated string
	err := sc.Scan(&p.ID, &p.Name, &p.Description, &p.Model, &p.ReasoningEffort,
		&p.SandboxMode, &p.ApprovalPolicy, &p.ExtraConfig, &p.WorkDir, &p.IsMinimal,
		&created, &updated)
	if err != nil {
		return p, err
	}
	p.CreatedAt, _ = parseTime(created)
	p.UpdatedAt, _ = parseTime(updated)
	return p, nil
}

// ListProfiles returns all profiles ordered by name.
func (d *DB) ListProfiles() ([]Profile, error) {
	rows, err := d.sql.Query(`SELECT ` + profileCols + ` FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProfile loads one profile by id.
func (d *DB) GetProfile(id int64) (Profile, error) {
	row := d.sql.QueryRow(`SELECT `+profileCols+` FROM profiles WHERE id = ?`, id)
	p, err := scanProfile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	return p, err
}

// CreateProfile inserts a profile and returns it with its assigned id.
func (d *DB) CreateProfile(p Profile) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`
INSERT INTO profiles (name, description, model, reasoning_effort, sandbox_mode,
    approval_policy, extra_config, work_dir, is_minimal, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Description, p.Model, p.ReasoningEffort, p.SandboxMode,
		p.ApprovalPolicy, p.ExtraConfig, p.WorkDir, boolToInt(p.IsMinimal), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Profile{}, fmt.Errorf("a profile named %q already exists", p.Name)
		}
		return Profile{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Profile{}, err
	}
	return d.GetProfile(id)
}

// UpdateProfile replaces every mutable field of an existing profile.
func (d *DB) UpdateProfile(p Profile) (Profile, error) {
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	res, err := d.sql.Exec(`
UPDATE profiles SET name = ?, description = ?, model = ?, reasoning_effort = ?,
    sandbox_mode = ?, approval_policy = ?, extra_config = ?, work_dir = ?,
    is_minimal = ?, updated_at = ?
WHERE id = ?`,
		p.Name, p.Description, p.Model, p.ReasoningEffort, p.SandboxMode,
		p.ApprovalPolicy, p.ExtraConfig, p.WorkDir, boolToInt(p.IsMinimal),
		formatTime(time.Now()), p.ID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return Profile{}, fmt.Errorf("a profile named %q already exists", p.Name)
		}
		return Profile{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Profile{}, ErrNotFound
	}
	return d.GetProfile(p.ID)
}

// DeleteProfile removes a profile along with its conversations and tasks.
func (d *DB) DeleteProfile(id int64) error {
	res, err := d.sql.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountConversationsForProfile reports how many conversations reference a
// profile, used to warn before deletion.
func (d *DB) CountConversationsForProfile(id int64) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM conversations WHERE profile_id = ?`, id).Scan(&n)
	return n, err
}
