package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"croncodex/internal/scheduler"
	"croncodex/internal/store"
)

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.db.ListTasks()
	if err != nil {
		writeStoreError(w, err, "list tasks")
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

// taskRequest is the accepted payload for creating and updating a task.
type taskRequest struct {
	Name       string `json:"name"`
	Prompt     string `json:"prompt"`
	ProfileID  int64  `json:"profile_id"`
	CronExpr   string `json:"cron_expr"`
	Enabled    bool   `json:"enabled"`
	WorkDir    string `json:"work_dir"`
	TimeoutSec int    `json:"timeout_sec"`
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	// Validate the expression here so the operator gets an immediate, specific
	// error rather than discovering a broken task from a scheduler log line.
	if err := scheduler.Validate(req.CronExpr); err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}

	task := store.Task{
		Name:       req.Name,
		Prompt:     req.Prompt,
		ProfileID:  req.ProfileID,
		CronExpr:   req.CronExpr,
		Enabled:    req.Enabled,
		WorkDir:    req.WorkDir,
		TimeoutSec: req.TimeoutSec,
	}
	if req.Enabled {
		next, err := scheduler.NextRun(req.CronExpr, time.Now())
		if err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		task.NextRunAt = &next
	}

	created, err := s.db.CreateTask(task)
	if err != nil {
		writeProfileError(w, err, "create task")
		return
	}
	s.log.Info("task created", "task", created.ID, "name", created.Name,
		"cron", created.CronExpr, "enabled", created.Enabled)
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var req taskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := scheduler.Validate(req.CronExpr); err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}

	existing, err := s.db.GetTask(id)
	if err != nil {
		writeStoreError(w, err, "task")
		return
	}

	task := store.Task{
		ID:         id,
		Name:       req.Name,
		Prompt:     req.Prompt,
		ProfileID:  req.ProfileID,
		CronExpr:   req.CronExpr,
		Enabled:    req.Enabled,
		WorkDir:    req.WorkDir,
		TimeoutSec: req.TimeoutSec,
	}
	task.NextRunAt = s.nextRunAfterEdit(existing, req)

	saved, err := s.db.UpdateTask(task)
	if err != nil {
		writeProfileError(w, err, "update task")
		return
	}
	s.log.Info("task updated", "task", saved.ID, "name", saved.Name,
		"cron", saved.CronExpr, "enabled", saved.Enabled)
	writeJSON(w, http.StatusOK, saved)
}

// nextRunAfterEdit decides the next run time for an edited task.
//
// An unrelated edit (renaming a task, say) must not push the schedule forward,
// so an already-pending slot is kept. The slot is only recomputed when the
// expression changed or the task is being switched back on.
func (s *Server) nextRunAfterEdit(existing store.Task, req taskRequest) *time.Time {
	if !req.Enabled {
		return nil
	}
	if existing.Enabled && existing.CronExpr == req.CronExpr && existing.NextRunAt != nil {
		return existing.NextRunAt
	}
	next, err := scheduler.NextRun(req.CronExpr, time.Now())
	if err != nil {
		// The expression was validated above, so this is unreachable in
		// practice; leaving the schedule empty parks the task safely.
		s.log.Error("could not compute next run for a validated expression",
			"cron", req.CronExpr, "error", err)
		return nil
	}
	return &next
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := s.db.DeleteTask(id); err != nil {
		writeStoreError(w, err, "delete task")
		return
	}
	s.log.Info("task deleted", "task", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRunTask triggers an immediate run outside the cron schedule.
func (s *Server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	runID, err := s.sched.RunNow(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "task not found")
			return
		}
		// The scheduler refuses a run while the previous one is still going.
		if strings.Contains(err.Error(), "already running") {
			writeError(w, http.StatusConflict, "%v", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "run task: %v", err)
		return
	}
	s.log.Info("manual task run triggered", "task", id, "run", runID)
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "run_id": runID})
}

func (s *Server) handleTaskRuns(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if _, err := s.db.GetTask(id); err != nil {
		writeStoreError(w, err, "task")
		return
	}
	runs, err := s.db.ListTaskRuns(id, queryLimit(r, 50, 200))
	if err != nil {
		writeStoreError(w, err, "list task runs")
		return
	}
	writeJSON(w, http.StatusOK, runs)
}
