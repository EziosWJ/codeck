package httpapi

import (
	"context"
	"net/http"
	"time"
)

// codexCacheTTL bounds how often the CLI is probed. The web UI polls /health
// and /dashboard every 10s from every open tab, and an uncached probe spawns a
// `codex --version` process; this TTL collapses that burst into one spawn. The
// cost is that a CLI installed after startup is not noticed for up to this long.
const codexCacheTTL = 30 * time.Second

// codexStatus reports whether the Codex CLI is usable, reusing a recent probe.
// Every caller goes through here so concurrent requests share one process.
func (s *Server) codexStatus(ctx context.Context) (bool, string) {
	s.codexMu.Lock()
	defer s.codexMu.Unlock()
	if !s.codexCachedAt.IsZero() && time.Since(s.codexCachedAt) < codexCacheTTL {
		return s.codexAvailable, s.codexVersion
	}

	available, version := s.codex.Available(ctx)
	s.codexAvailable = available
	s.codexVersion = version
	s.codexCachedAt = time.Now()
	return available, version
}

// handleHealth reports whether the service and the Codex CLI are usable.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	codexAvailable, codexVersion := s.codexStatus(ctx)
	schema, _ := s.db.SchemaVersion()

	writeJSON(w, http.StatusOK, map[string]any{
		"ok":                true,
		"codex_available":   codexAvailable,
		"codex_version":     codexVersion,
		"codex_bin":         s.codex.Bin(),
		"db_path":           s.cfg.DBPath,
		"version":           s.version,
		"schema_version":    schema,
		"uptime_seconds":    int64(time.Since(s.started).Seconds()),
		"running_tasks":     s.sched.Running(),
		"scheduler_enabled": s.cfg.SchedulerEnabled,
	})
}

// handleDashboard aggregates the numbers and recent activity the landing page
// shows, so the UI needs a single request to render.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	profiles, err := s.db.ListProfiles()
	if err != nil {
		writeStoreError(w, err, "list profiles")
		return
	}
	conversations, err := s.db.CountConversations()
	if err != nil {
		writeStoreError(w, err, "count conversations")
		return
	}
	totalTasks, enabledTasks, err := s.db.CountTasks()
	if err != nil {
		writeStoreError(w, err, "count tasks")
		return
	}

	startOfDay := time.Now().Truncate(24 * time.Hour)
	if local, err := time.LoadLocation("Local"); err == nil {
		now := time.Now().In(local)
		startOfDay = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, local)
	}
	runsToday, err := s.db.CountRunsSince(startOfDay)
	if err != nil {
		writeStoreError(w, err, "count runs")
		return
	}

	recentRuns, err := s.db.ListRecentRuns(10)
	if err != nil {
		writeStoreError(w, err, "list runs")
		return
	}
	recentConversations, err := s.db.ListConversations(10)
	if err != nil {
		writeStoreError(w, err, "list conversations")
		return
	}

	available, codexVersion := s.codexStatus(ctx)

	writeJSON(w, http.StatusOK, map[string]any{
		"counts": map[string]any{
			"profiles":        len(profiles),
			"conversations":   conversations,
			"tasks":           totalTasks,
			"enabled_tasks":   enabledTasks,
			"runs_today":      runsToday,
			"running_tasks":   s.sched.Running(),
			"codex_available": available,
		},
		"codex_version":        codexVersion,
		"recent_runs":          recentRuns,
		"recent_conversations": recentConversations,
	})
}

// handleListRuns returns recent task runs across every task.
func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := s.db.ListRecentRuns(queryLimit(r, 50, 200))
	if err != nil {
		writeStoreError(w, err, "list runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// handleHistory returns the combined chat and task-execution history.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	limit := queryLimit(r, 100, 500)

	messages, err := s.db.ListRecentMessages(limit)
	if err != nil {
		writeStoreError(w, err, "list messages")
		return
	}
	runs, err := s.db.ListRecentRuns(limit)
	if err != nil {
		writeStoreError(w, err, "list runs")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"messages": messages,
		"runs":     runs,
	})
}
