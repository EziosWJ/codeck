// Package config loads runtime configuration from environment variables,
// optionally seeded by a simple KEY=VALUE file.
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// EnvPrefix prefixes every environment variable this application reads.
	EnvPrefix = "CODECK_"
)

// Config holds all runtime settings.
type Config struct {
	// Addr is the HTTP listen address, e.g. ":8080".
	Addr string
	// DataDir holds the database, generated codex homes and workspaces.
	DataDir string
	// DBPath is the SQLite database file.
	DBPath string

	// CodexBin is the codex executable name or path.
	CodexBin string
	// CodexHomeRoot is the parent directory for per-profile CODEX_HOME dirs.
	CodexHomeRoot string
	// WorkspaceRoot is the parent directory for per-profile scratch workspaces.
	WorkspaceRoot string
	// AuthSource is the codex auth.json to link into each profile home.
	AuthSource string

	// SchedulerInterval is how often the scheduler polls for due tasks.
	SchedulerInterval time.Duration
	// DefaultTimeout is the default per-invocation timeout.
	DefaultTimeout time.Duration
	// MaxConcurrentRuns bounds simultaneous codex invocations.
	MaxConcurrentRuns int

	// LogLevel is one of debug, info, warn, error.
	LogLevel string
}

// Default returns the configuration with every field populated by defaults.
//
// The storage paths are deliberately left empty: they are derived from DataDir
// once every source has been applied, so that setting only DATA_DIR moves the
// database, the codex homes and the workspaces together.
func Default() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Addr:              ":8080",
		DataDir:           "./data",
		DBPath:            "",
		CodexBin:          "codex",
		CodexHomeRoot:     "",
		WorkspaceRoot:     "",
		AuthSource:        filepath.Join(home, ".codex", "auth.json"),
		SchedulerInterval: 10 * time.Second,
		DefaultTimeout:    5 * time.Minute,
		MaxConcurrentRuns: 4,
		LogLevel:          "info",
	}
}

// Load builds a Config from defaults, then the optional config file, then the
// environment. Later sources win. configPath may be empty, in which case only
// CODECK_CONFIG is consulted.
func Load(configPath string) (Config, error) {
	cfg := Default()

	if configPath == "" {
		configPath = os.Getenv(EnvPrefix + "CONFIG")
	}
	if configPath != "" {
		if err := applyFile(&cfg, configPath); err != nil {
			return cfg, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	cfg.derivePaths()

	// Paths given relatively are resolved against the process working
	// directory so that a launched binary behaves predictably.
	cfg.DBPath = absOrSelf(cfg.DBPath)
	cfg.DataDir = absOrSelf(cfg.DataDir)
	cfg.CodexHomeRoot = absOrSelf(cfg.CodexHomeRoot)
	cfg.WorkspaceRoot = absOrSelf(cfg.WorkspaceRoot)
	return cfg, nil
}

// derivePaths fills in the storage locations that were not set explicitly,
// keeping them anchored under DataDir.
func (c *Config) derivePaths() {
	if c.DataDir == "" {
		c.DataDir = "./data"
	}
	if c.DBPath == "" {
		c.DBPath = filepath.Join(c.DataDir, "codeck.db")
	}
	if c.CodexHomeRoot == "" {
		c.CodexHomeRoot = filepath.Join(c.DataDir, "codex-home")
	}
	if c.WorkspaceRoot == "" {
		c.WorkspaceRoot = filepath.Join(c.DataDir, "workspace")
	}
}

func absOrSelf(p string) string {
	if p == "" {
		return p
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}

// EnsureDirs creates every directory the application writes to.
func (c Config) EnsureDirs() error {
	for _, dir := range []string{c.DataDir, c.CodexHomeRoot, c.WorkspaceRoot, filepath.Dir(c.DBPath)} {
		if dir == "" {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}
	return nil
}

// applyFile reads KEY=VALUE lines. Blank lines and lines starting with # are
// ignored. Keys may be written with or without the CODECK_ prefix.
func applyFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open config file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, line)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if err := assign(cfg, key, value); err != nil {
			return fmt.Errorf("%s:%d: %w", path, line, err)
		}
	}
	return scanner.Err()
}

func applyEnv(cfg *Config) error {
	for _, kv := range os.Environ() {
		key, value, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(key, EnvPrefix) {
			continue
		}
		if key == EnvPrefix+"CONFIG" {
			continue
		}
		if err := assign(cfg, strings.TrimPrefix(key, EnvPrefix), value); err != nil {
			return err
		}
	}
	return nil
}

// assign maps a configuration key onto a Config field. Keys are matched
// case-insensitively so both KEY=value files and env vars work unmodified.
func assign(cfg *Config, key, value string) error {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "ADDR", "LISTEN":
		cfg.Addr = value
	case "DATA_DIR":
		cfg.DataDir = value
	case "DB_PATH", "DB":
		cfg.DBPath = value
	case "CODEX_BIN":
		cfg.CodexBin = value
	case "CODEX_HOME_ROOT":
		cfg.CodexHomeRoot = value
	case "WORKSPACE_ROOT", "WORK_DIR":
		cfg.WorkspaceRoot = value
	case "AUTH_SOURCE":
		cfg.AuthSource = value
	case "SCHEDULER_INTERVAL":
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid SCHEDULER_INTERVAL %q: %w", value, err)
		}
		cfg.SchedulerInterval = d
	case "DEFAULT_TIMEOUT":
		d, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid DEFAULT_TIMEOUT %q: %w", value, err)
		}
		cfg.DefaultTimeout = d
	case "MAX_CONCURRENT_RUNS":
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 {
			return fmt.Errorf("invalid MAX_CONCURRENT_RUNS %q: must be a positive integer", value)
		}
		cfg.MaxConcurrentRuns = n
	case "LOG_LEVEL":
		cfg.LogLevel = strings.ToLower(value)
	default:
		// Unknown keys are ignored rather than fatal: a stray variable in the
		// environment must not stop the service from booting.
	}
	return nil
}
