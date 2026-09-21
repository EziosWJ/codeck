package config

import (
	"os"
	"path/filepath"
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
	for _, key := range []string{"DATA_DIR", "DB_PATH", "ADDR", "AUTH_USER", "AUTH_PASSWORD", "CODEX_BIN", "SCHEDULER_INTERVAL", "ACCOUNT_CACHE_TTL"} {
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
