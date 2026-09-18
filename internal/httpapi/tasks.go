package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"codeck/internal/scheduler"
	"codeck/internal/store"
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
	Name         string `json:"name"`
	Prompt       string `json:"prompt"`
	ProfileID    int64  `json:"profile_id"`
	CronExpr     string `json:"cron_expr"`
	ScheduleType string `json:"schedule_type"`
	RunAt        string `json:"run_at"`
	Enabled      bool   `json:"enabled"`
	WorkDir      string `json:"work_dir"`
	TimeoutSec   int    `json:"timeout_sec"`
}

// normalizeSchedule validates the schedule fields and returns the canonical
// schedule type plus the parsed one-shot time (nil for cron tasks).
func normalizeSchedule(req *taskRequest) (*time.Time, error) {
	req.ScheduleType = strings.TrimSpace(req.ScheduleType)
	if req.ScheduleType == "" {
		// Backward compatibility: old clients only send cron_expr.
		req.ScheduleType = store.ScheduleCron
	}
	switch req.ScheduleType {
	case store.ScheduleOnce:
		runAt, err := store.ParseRunAt(req.RunAt)
		if err != nil {
			return nil, err
		}
		req.CronExpr = ""
		return &runAt, nil
	case store.ScheduleCron:
		req.RunAt = ""
		// Validate the expression here so the operator gets an immediate,
		// specific error rather than discovering a broken task from a
		// scheduler log line.
		if err := scheduler.Validate(req.CronExpr); err != nil {
			return nil, err
		}
		return nil, nil
	default:
		return nil, errors.New(`schedule_type must be "cron" or "once"`)
	}
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req taskRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	runAt, err := normalizeSchedule(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}

	task := store.Task{
		Name:         req.Name,
		Prompt:       req.Prompt,
		ProfileID:    req.ProfileID,
		CronExpr:     req.CronExpr,
		ScheduleType: req.ScheduleType,
		RunAt:        runAt,
		Enabled:      req.Enabled,
		WorkDir:      req.WorkDir,
		TimeoutSec:   req.TimeoutSec,
	}
	if req.Enabled {
		next, err := nextRunForNewTask(req.ScheduleType, req.CronExpr, runAt, time.Now())
		if err != nil {
			writeError(w, http.StatusBadRequest, "%v", err)
			return
		}
		task.NextRunAt = next
	}

	created, err := s.db.CreateTask(task)
	if err != nil {
		writeProfileError(w, err, "create task")
		return
	}
	s.log.Info("task created", "task", created.ID, "name", created.Name,
		"schedule", created.ScheduleType, "cron", created.CronExpr, "enabled", created.Enabled)
	writeJSON(w, http.StatusCreated, created)
}

// nextRunForNewTask computes the initial next_run_at for an enabled task.
func nextRunForNewTask(scheduleType, cronExpr string, runAt *time.Time, now time.Time) (*time.Time, error) {
	if scheduleType == store.ScheduleOnce {
		if runAt == nil {
			return nil, errors.New("run_at is required for one-shot tasks")
		}
		if !runAt.After(now) {
			return nil, errors.New("一次性任务的执行时间必须是将来时间")
		}
		next := *runAt
		return &next, nil
	}
	next, err := scheduler.NextRun(cronExpr, now)
	if err != nil {
		return nil, err
	}
	return &next, nil
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
	runAt, err := normalizeSchedule(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}

	existing, err := s.db.GetTask(id)
	if err != nil {
		writeStoreError(w, err, "task")
		return
	}

	task := store.Task{
		ID:           id,
		Name:         req.Name,
		Prompt:       req.Prompt,
		ProfileID:    req.ProfileID,
		CronExpr:     req.CronExpr,
		ScheduleType: req.ScheduleType,
		RunAt:        runAt,
		Enabled:      req.Enabled,
		WorkDir:      req.WorkDir,
		TimeoutSec:   req.TimeoutSec,
	}
	next, err := s.nextRunAfterEdit(existing, req, runAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "%v", err)
		return
	}
	task.NextRunAt = next

	saved, err := s.db.UpdateTask(task)
	if err != nil {
		writeProfileError(w, err, "update task")
		return
	}
	s.log.Info("task updated", "task", saved.ID, "name", saved.Name,
		"schedule", saved.ScheduleType, "cron", saved.CronExpr, "enabled", saved.Enabled)
	writeJSON(w, http.StatusOK, saved)
}

// nextRunAfterEdit decides the next run time for an edited task.
//
// An unrelated edit (renaming a task, say) must not push the schedule forward,
// so an already-pending slot is kept. The slot is only recomputed when the
// schedule changed or the task is being switched back on.
func (s *Server) nextRunAfterEdit(existing store.Task, req taskRequest, runAt *time.Time) (*time.Time, error) {
	if !req.Enabled {
		return nil, nil
	}
	now := time.Now()
	if req.ScheduleType == store.ScheduleOnce {
		if runAt == nil {
			return nil, errors.New("run_at is required for one-shot tasks")
		}
		if !runAt.After(now) {
			return nil, errors.New("一次性任务的执行时间必须是将来时间（已过期的时间无法启用）")
		}
		if existing.Enabled && existing.ScheduleType == store.ScheduleOnce &&
			existing.RunAt != nil && existing.RunAt.Equal(*runAt) && existing.NextRunAt != nil {
			return existing.NextRunAt, nil
		}
		next := *runAt
		return &next, nil
	}
	if existing.Enabled && existing.ScheduleType == store.ScheduleCron &&
		existing.CronExpr == req.CronExpr && existing.NextRunAt != nil {
		return existing.NextRunAt, nil
	}
	next, err := scheduler.NextRun(req.CronExpr, now)
	if err != nil {
		// An enabled task must never be persisted without a concrete next slot.
		return nil, err
	}
	return &next, nil
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
	s.log.Info("manual task run triggered", "task", id, "run", runID,
		"scheduler_enabled", s.cfg.SchedulerEnabled)
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
