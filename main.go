// Command codeck is a small web tool for chatting with the Codex CLI through
// isolated profiles and for running Codex prompts on a schedule.
//
// It serves both the REST API and the built web UI from a single binary, and
// keeps all state in one SQLite file.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codeck/internal/codex"
	"codeck/internal/config"
	"codeck/internal/httpapi"
	"codeck/internal/scheduler"
	"codeck/internal/store"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

// webAssets carries the compiled React app. The path is relative to this file,
// so the UI ships inside the binary with no external files to deploy.
//
//go:embed all:web/dist
var webAssets embed.FS

// staticRoot is the directory inside webAssets that holds the built app.
const staticRoot = "web/dist"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "path to a KEY=VALUE config file (optional)")
	showVersion := flag.Bool("version", false, "print the version and exit")
	probeAppServer := flag.Bool("appserver-probe", false, "start Codex App Server, print account JSON, and exit")
	noScheduler := flag.Bool("no-scheduler", false, "disable automatic cron dispatch (view the UI without firing scheduled tasks)")
	flag.Parse()

	if *showVersion {
		fmt.Println("codeck", version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *noScheduler {
		cfg.SchedulerEnabled = false
	}

	log := newLogger(cfg.LogLevel)
	slog.SetDefault(log)

	if *probeAppServer {
		return runAppServerProbe(cfg, log)
	}

	if err := cfg.EnsureDirs(); err != nil {
		return err
	}

	log.Info("starting codeck",
		"version", version, "addr", cfg.Addr, "data_dir", cfg.DataDir, "db", cfg.DBPath,
		"scheduler_enabled", cfg.SchedulerEnabled)

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	schema, err := db.SchemaVersion()
	if err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	log.Info("database ready", "path", cfg.DBPath, "schema_version", schema)

	// Anything left mid-flight by a previous process is closed out, so the UI
	// never shows a run or a reply that spins forever.
	if n, err := db.MarkStaleRunsFailed(); err != nil {
		log.Warn("could not close stale task runs", "error", err)
	} else if n > 0 {
		log.Warn("closed task runs interrupted by the previous shutdown", "count", n)
	}
	if n, err := db.MarkStaleMessagesFailed(); err != nil {
		log.Warn("could not close stale chat replies", "error", err)
	} else if n > 0 {
		log.Warn("closed chat replies interrupted by the previous shutdown", "count", n)
	}

	codexService := codex.NewService(
		cfg.CodexBin, cfg.CodexHomeRoot, cfg.WorkspaceRoot,
		cfg.AuthSource, cfg.DefaultTimeout, log.With("component", "codex"))

	// A root context cancelled by SIGINT/SIGTERM; every background run is
	// derived from it so shutdown stops Codex processes promptly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startupCtx, cancelStartup := context.WithTimeout(ctx, 20*time.Second)
	if available, codexVersion := codexService.Available(startupCtx); available {
		log.Info("codex CLI detected", "bin", cfg.CodexBin, "version", codexVersion)
	} else {
		log.Warn("codex CLI not found; chats and tasks will fail until it is installed",
			"bin", cfg.CodexBin)
	}
	cancelStartup()
	log.Info("profiles are isolated per-CODEX_HOME",
		"codex_home_root", cfg.CodexHomeRoot, "auth_source", cfg.AuthSource)

	appServer, err := codex.StartAppServer(ctx, codex.AppServerOptions{
		Bin:           cfg.CodexBin,
		ClientVersion: version,
		Log:           log.With("component", "app-server"),
	})
	if err != nil {
		log.Warn("codex app-server failed to start; account APIs will be unavailable",
			"bin", cfg.CodexBin, "error", err)
	} else {
		defer func() {
			if err := appServer.Close(); err != nil {
				log.Warn("codex app-server did not shut down cleanly", "error", err)
			}
		}()
	}

	sched := scheduler.New(db, codexService, cfg.SchedulerInterval, cfg.MaxConcurrentRuns,
		log.With("component", "scheduler"))
	if cfg.SchedulerEnabled {
		sched.Start(ctx)
	} else {
		// Dev/viewing mode: the cron loop never runs, so due tasks stay
		// untouched. Manual "run now" from the UI still works — it is an
		// explicit action, not an automatic trigger.
		log.Info("scheduler disabled: automatic task dispatch is off; manual runs still allowed")
	}

	static, err := staticFS()
	if err != nil {
		return err
	}

	api := httpapi.NewServer(cfg, db, codexService, sched, appServer, log.With("component", "http"), version, static)

	srv := &http.Server{
		Addr:    cfg.Addr,
		Handler: api.Handler(),
		// A streaming chat turn can legitimately take minutes, so no write
		// timeout is set; the per-run Codex timeout bounds it instead.
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("web UI and API listening", "url", "http://localhost"+cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received")
	}

	// Stop accepting requests, then let the scheduler wind down. Background runs
	// observe the cancelled context and their Codex processes are killed.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http server did not shut down cleanly", "error", err)
	}
	sched.Stop()
	log.Info("stopped")
	return nil
}

// runAppServerProbe starts a short-lived App Server, prints the raw JSON of
// the three account methods, and exits. It is the debug entry for the JSON-RPC
// wiring and does not start the HTTP server or scheduler.
func runAppServerProbe(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	appServer, err := codex.StartAppServer(ctx, codex.AppServerOptions{
		Bin:           cfg.CodexBin,
		ClientVersion: version,
		Log:           log.With("component", "app-server"),
	})
	if err != nil {
		return err
	}
	defer appServer.Close()

	type call struct {
		name string
		fn   func(context.Context) (json.RawMessage, error)
	}
	calls := []call{
		{"account/read", appServer.AccountRead},
		{"account/rateLimits/read", appServer.AccountRateLimitsRead},
		{"account/usage/read", appServer.AccountUsageRead},
	}

	var failed int
	for _, c := range calls {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		raw, err := c.fn(callCtx)
		cancel()

		fmt.Printf("=== %s ===\n", c.name)
		if err != nil {
			failed++
			fmt.Printf("error: %v\n\n", err)
			continue
		}
		pretty, indentErr := json.MarshalIndent(raw, "", "  ")
		if indentErr != nil {
			fmt.Printf("%s\n\n", raw)
			continue
		}
		fmt.Printf("%s\n\n", pretty)
	}
	if failed > 0 {
		return fmt.Errorf("%d account method(s) failed", failed)
	}
	return nil
}

// staticFS exposes the embedded web UI as a filesystem rooted at the build
// output directory. A missing build is reported once at startup rather than as
// a confusing 404 later.
func staticFS() (fs.FS, error) {
	sub, err := fs.Sub(webAssets, staticRoot)
	if err != nil {
		return nil, fmt.Errorf("locate embedded web assets: %w", err)
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, fmt.Errorf("the embedded web UI has no index.html; build it with `npm run build` in web/ and rebuild the binary: %w", err)
	}
	return sub, nil
}

// newLogger builds a text logger at the configured level.
func newLogger(level string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
}
