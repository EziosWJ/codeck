package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDerivesPathsFromDataDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPrefix+"DATA_DIR", dir)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Setting only DATA_DIR must move every storage location together.
	if cfg.DBPath != filepath.Join(dir, "codeck.db") {
		t.Errorf("DBPath = %q, want it under DataDir", cfg.DBPath)
	}
	if cfg.CodexHomeRoot != filepath.Join(dir, "codex-home") {
		t.Errorf("CodexHomeRoot = %q, want it under DataDir", cfg.CodexHomeRoot)
	}
	if cfg.WorkspaceRoot != filepath.Join(dir, "workspace") {
		t.Errorf("WorkspaceRoot = %q, want it under DataDir", cfg.WorkspaceRoot)
	}
}

func TestExplicitPathBeatsDerivedPath(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "elsewhere", "custom.db")
	t.Setenv(EnvPrefix+"DATA_DIR", dir)
	t.Setenv(EnvPrefix+"DB_PATH", custom)

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != custom {
		t.Errorf("DBPath = %q, want the explicit %q", cfg.DBPath, custom)
	}
	// The other derived paths still follow DataDir.
	if cfg.CodexHomeRoot != filepath.Join(dir, "codex-home") {
		t.Errorf("CodexHomeRoot = %q, want it under DataDir", cfg.CodexHomeRoot)
	}
}

func TestDefaultsWhenNothingIsSet(t *testing.T) {
	for _, key := range []string{"DATA_DIR", "DB_PATH", "ADDR", "AUTH_USER", "AUTH_PASSWORD", "CODEX_BIN", "SCHEDULER_INTERVAL", "ACCOUNT_CACHE_TTL", "BALANCE_ENCRYPTION_KEY"} {
		os.Unsetenv(EnvPrefix + key)
	}
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:8080" {
		t.Errorf("Addr = %q, want 127.0.0.1:8080", cfg.Addr)
	}
	if cfg.SchedulerInterval != 10*time.Second {
		t.Errorf("SchedulerInterval = %s, want 10s", cfg.SchedulerInterval)
	}
	if cfg.AccountCacheTTL != 30*time.Second {
		t.Errorf("AccountCacheTTL = %s, want 30s", cfg.AccountCacheTTL)
	}
	if cfg.DefaultTimeout != 5*time.Minute {
		t.Errorf("DefaultTimeout = %s, want 5m", cfg.DefaultTimeout)
	}
	if cfg.MaxConcurrentRuns != 4 {
		t.Errorf("MaxConcurrentRuns = %d, want 4", cfg.MaxConcurrentRuns)
	}
	// Relative defaults are resolved against the working directory.
	wantDB, _ := filepath.Abs(filepath.Join("data", "codeck.db"))
	if cfg.DBPath != wantDB {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, wantDB)
	}
}

func TestBalanceEncryptionKeyComesOnlyFromEnvironment(t *testing.T) {
	const encoded = "dGVzdC1rZXk="
	t.Setenv(EnvPrefix+"BALANCE_ENCRYPTION_KEY", encoded)
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load from environment: %v", err)
	}
	if cfg.BalanceEncryptionKey != encoded {
		t.Fatalf("BalanceEncryptionKey = %q", cfg.BalanceEncryptionKey)
	}

	path := filepath.Join(t.TempDir(), "codeck.env")
	if err := os.WriteFile(path, []byte("BALANCE_ENCRYPTION_KEY=must-not-be-here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "process environment") {
		t.Fatalf("config-file secret should be rejected, got %v", err)
	}
}

func TestConfigFileThenEnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codeck.env")
	content := `# comment line
ADDR = ":9999"
AUTH_USER=admin
AUTH_PASSWORD=secret
SCHEDULER_INTERVAL=45s
CODEX_BIN=/from/file/codex
LOG_LEVEL=debug
DATA_DIR=` + dir + `
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	// The environment wins over the file.
	t.Setenv(EnvPrefix+"CODEX_BIN", "/from/env/codex")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q, want :9999 from the file", cfg.Addr)
	}
	if cfg.SchedulerInterval != 45*time.Second {
		t.Errorf("SchedulerInterval = %s, want 45s from the file", cfg.SchedulerInterval)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.LogLevel)
	}
	if cfg.CodexBin != "/from/env/codex" {
		t.Errorf("CodexBin = %q, want the environment to win", cfg.CodexBin)
	}
}

// The CODECK_ prefix is optional in a config file (README "配置": keys may be
// written with or without the prefix). Silently ignoring a prefixed key is what
// let CODECK_SCHEDULER_ENABLED=false be discarded while the scheduler stayed on.
func TestConfigFileAcceptsOptionalEnvPrefix(t *testing.T) {
	dir := t.TempDir()
	for _, key := range []string{"ADDR", "SCHEDULER_ENABLED", "LOG_LEVEL"} {
		os.Unsetenv(EnvPrefix + key)
	}

	cases := []struct {
		name    string
		content string
	}{
		{"without prefix", "ADDR=127.0.0.1:9999\nSCHEDULER_ENABLED=false\nLOG_LEVEL=warn\n"},
		{"with prefix", "CODECK_ADDR=127.0.0.1:9999\nCODECK_SCHEDULER_ENABLED=false\nCODECK_LOG_LEVEL=warn\n"},
		{"lowercase prefix", "codeck_addr=127.0.0.1:9999\ncodeck_scheduler_enabled=false\ncodeck_log_level=warn\n"},
		{"mixed within one file", "ADDR=127.0.0.1:9999\nCODECK_SCHEDULER_ENABLED=false\nLOG_LEVEL=warn\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "codeck.env")
			if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Addr != "127.0.0.1:9999" {
				t.Errorf("Addr = %q, want 127.0.0.1:9999", cfg.Addr)
			}
			if cfg.SchedulerEnabled {
				t.Error("SchedulerEnabled = true, want false; a discarded key here starts real scheduled runs")
			}
			if cfg.LogLevel != "warn" {
				t.Errorf("LogLevel = %q, want warn", cfg.LogLevel)
			}
		})
	}
}

// Keys are matched case-insensitively, both with and without the prefix.
func TestConfigFileKeyCaseInsensitive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codeck.env")
	if err := os.WriteFile(path, []byte("codeck_Data_Dir="+dir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Unsetenv(EnvPrefix + "DATA_DIR")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != dir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dir)
	}
}

// The environment still requires the prefix: applyEnv must not start consuming
// unrelated lowercase variables such as a shell's own `addr`.
func TestEnvironmentStillRequiresPrefix(t *testing.T) {
	t.Setenv("ADDR", "127.0.0.1:12345")
	os.Unsetenv(EnvPrefix + "ADDR")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr == "127.0.0.1:12345" {
		t.Error("bare ADDR leaked in from the environment; applyEnv must require CODECK_")
	}
}

func TestSchedulerEnabledDefaultsToTrue(t *testing.T) {
	os.Unsetenv(EnvPrefix + "SCHEDULER_ENABLED")
	os.Unsetenv(EnvPrefix + "ENABLE_SCHEDULER")
	os.Unsetenv(EnvPrefix + "SCHEDULER_DISABLED")
	os.Unsetenv(EnvPrefix + "DISABLE_SCHEDULER")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.SchedulerEnabled {
		t.Error("SchedulerEnabled = false, want the default true")
	}
}

func TestSchedulerEnabledParsesCommonSpellings(t *testing.T) {
	for _, value := range []string{"0", "false", "no", "off", "disable", "FALSE", " No "} {
		t.Setenv(EnvPrefix+"SCHEDULER_ENABLED", value)
		cfg, err := Load("")
		if err != nil {
			t.Fatalf("Load with SCHEDULER_ENABLED=%q: %v", value, err)
		}
		if cfg.SchedulerEnabled {
			t.Errorf("SCHEDULER_ENABLED=%q: got enabled, want disabled", value)
		}
	}
}

func TestSchedulerDisabledAliasIsNegated(t *testing.T) {
	t.Setenv(EnvPrefix+"SCHEDULER_DISABLED", "true")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.SchedulerEnabled {
		t.Error("SCHEDULER_DISABLED=true: got enabled, want disabled")
	}
}

func TestSchedulerEnabledRejectsGarbage(t *testing.T) {
	t.Setenv(EnvPrefix+"SCHEDULER_ENABLED", "sometimes")
	if _, err := Load(""); err == nil {
		t.Error("SCHEDULER_ENABLED=sometimes should be rejected")
	}
}

func TestConfigFileRejectsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.env")
	if err := os.WriteFile(path, []byte("this line has no equals sign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a line without KEY=VALUE should be rejected")
	}
}

func TestInvalidValuesAreRejected(t *testing.T) {
	t.Setenv(EnvPrefix+"SCHEDULER_INTERVAL", "not-a-duration")
	if _, err := Load(""); err == nil {
		t.Error("invalid SCHEDULER_INTERVAL should be rejected")
	}

	t.Setenv(EnvPrefix+"SCHEDULER_INTERVAL", "10s")
	t.Setenv(EnvPrefix+"MAX_CONCURRENT_RUNS", "0")
	if _, err := Load(""); err == nil {
		t.Error("MAX_CONCURRENT_RUNS=0 should be rejected")
	}
}

func TestAccountCacheTTLIsConfigurable(t *testing.T) {
	t.Setenv(EnvPrefix+"ACCOUNT_CACHE_TTL", "45s")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.AccountCacheTTL != 45*time.Second {
		t.Errorf("AccountCacheTTL = %s, want 45s", cfg.AccountCacheTTL)
	}

	// ACCOUNT_INTERVAL is the friendlier alias for the same knob.
	os.Unsetenv(EnvPrefix + "ACCOUNT_CACHE_TTL")
	t.Setenv(EnvPrefix+"ACCOUNT_INTERVAL", "1m")
	cfg, err = Load("")
	if err != nil {
		t.Fatalf("Load with ACCOUNT_INTERVAL: %v", err)
	}
	if cfg.AccountCacheTTL != time.Minute {
		t.Errorf("AccountCacheTTL = %s, want 1m", cfg.AccountCacheTTL)
	}
}

func TestAccountCacheTTLRejectsBadValues(t *testing.T) {
	for _, value := range []string{"not-a-duration", "0s", "-5s"} {
		t.Setenv(EnvPrefix+"ACCOUNT_CACHE_TTL", value)
		if _, err := Load(""); err == nil {
			t.Errorf("ACCOUNT_CACHE_TTL=%q should be rejected", value)
		}
	}
}

func TestUnknownEnvironmentVariablesAreIgnored(t *testing.T) {
	// A stray CODECK_ variable in the environment must not stop the service.
	t.Setenv(EnvPrefix+"SOMETHING_UNKNOWN", "value")
	if _, err := Load(""); err != nil {
		t.Errorf("Load: %v", err)
	}
}

func TestEnsureDirsCreatesEverything(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvPrefix+"DATA_DIR", filepath.Join(dir, "nested", "deeper"))

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("EnsureDirs: %v", err)
	}
	for _, p := range []string{cfg.DataDir, cfg.CodexHomeRoot, cfg.WorkspaceRoot, filepath.Dir(cfg.DBPath)} {
		if info, err := os.Stat(p); err != nil || !info.IsDir() {
			t.Errorf("directory %s was not created (%v)", p, err)
		}
	}
}

func TestBasicAuthConfigurationMustBeComplete(t *testing.T) {
	t.Setenv(EnvPrefix+"AUTH_USER", "admin")
	t.Setenv(EnvPrefix+"AUTH_PASSWORD", "")
	if _, err := Load(""); err == nil {
		t.Fatal("AUTH_USER without AUTH_PASSWORD should be rejected")
	}

	t.Setenv(EnvPrefix+"AUTH_USER", "")
	t.Setenv(EnvPrefix+"AUTH_PASSWORD", "secret")
	if _, err := Load(""); err == nil {
		t.Fatal("AUTH_PASSWORD without AUTH_USER should be rejected")
	}
}

func TestRemoteListenRequiresAuthentication(t *testing.T) {
	t.Setenv(EnvPrefix+"ADDR", ":8080")
	t.Setenv(EnvPrefix+"AUTH_USER", "")
	t.Setenv(EnvPrefix+"AUTH_PASSWORD", "")
	if _, err := Load(""); err == nil {
		t.Fatal("non-loopback listen without authentication should be rejected")
	}
}

func TestRemoteListenAllowedWithAuthentication(t *testing.T) {
	t.Setenv(EnvPrefix+"ADDR", "0.0.0.0:8080")
	t.Setenv(EnvPrefix+"AUTH_USER", "admin")
	t.Setenv(EnvPrefix+"AUTH_PASSWORD", "secret")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTPAuthUser != "admin" || cfg.HTTPAuthPassword != "secret" {
		t.Fatalf("basic auth configuration was not loaded")
	}
}
