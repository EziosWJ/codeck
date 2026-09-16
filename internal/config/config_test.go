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
	if cfg.DBPath != filepath.Join(dir, "croncodex.db") {
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
	for _, key := range []string{"DATA_DIR", "DB_PATH", "ADDR", "CODEX_BIN", "SCHEDULER_INTERVAL"} {
		os.Unsetenv(EnvPrefix + key)
	}
	t.Chdir(t.TempDir())

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.SchedulerInterval != 10*time.Second {
		t.Errorf("SchedulerInterval = %s, want 10s", cfg.SchedulerInterval)
	}
	if cfg.DefaultTimeout != 5*time.Minute {
		t.Errorf("DefaultTimeout = %s, want 5m", cfg.DefaultTimeout)
	}
	if cfg.MaxConcurrentRuns != 4 {
		t.Errorf("MaxConcurrentRuns = %d, want 4", cfg.MaxConcurrentRuns)
	}
	// Relative defaults are resolved against the working directory.
	wantDB, _ := filepath.Abs(filepath.Join("data", "croncodex.db"))
	if cfg.DBPath != wantDB {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, wantDB)
	}
}

func TestConfigFileThenEnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "croncodex.env")
	content := `# comment line
ADDR = ":9999"
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

func TestUnknownEnvironmentVariablesAreIgnored(t *testing.T) {
	// A stray CRONCODEX_ variable in the environment must not stop the service.
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
