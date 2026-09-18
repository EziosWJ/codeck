package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Task is a scheduled Codex invocation.
type Task struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Prompt      string     `json:"prompt"`
	ProfileID   int64      `json:"profile_id"`
	ProfileName string     `json:"profile_name"`
	CronExpr    string     `json:"cron_expr"`
	// ScheduleType is "cron" (repeat) or "once" (fire a single time at RunAt).
	ScheduleType string     `json:"schedule_type"`
	// RunAt is the single fire time for "once" tasks, nil otherwise.
	RunAt       *time.Time `json:"run_at"`
	Enabled     bool       `json:"enabled"`
	WorkDir     string     `json:"work_dir"`
	TimeoutSec  int        `json:"timeout_sec"`
	LastRunAt   *time.Time `json:"last_run_at"`
	NextRunAt   *time.Time `json:"next_run_at"`
	LastStatus  string     `json:"last_status"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Schedule types accepted by Task.ScheduleType.
const (
	ScheduleCron = "cron"
	ScheduleOnce = "once"
)

// TaskRun is one execution attempt of a task.
type TaskRun struct {
	ID           int64      `json:"id"`
	TaskID       int64      `json:"task_id"`
	TaskName     string     `json:"task_name"`
	Status       string     `json:"status"`
	Trigger      string     `json:"trigger"`
	Output       string     `json:"output"`
	Error        string     `json:"error"`
	InputTokens  int        `json:"input_tokens"`
	OutputTokens int        `json:"output_tokens"`
	DurationMs   int64      `json:"duration_ms"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// Validate checks the fields a task cannot do without.
func (t *Task) Validate() error {
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(t.Prompt) == "" {
		return fmt.Errorf("prompt is required")
	}
	t.ScheduleType = strings.TrimSpace(t.ScheduleType)
	if t.ScheduleType == "" {
		t.ScheduleType = ScheduleCron
	}
	switch t.ScheduleType {
	case ScheduleOnce:
		if t.RunAt == nil {
			return fmt.Errorf("run_at is required for one-shot tasks")
		}
		t.CronExpr = ""
	case ScheduleCron:
		t.RunAt = nil
		if strings.TrimSpace(t.CronExpr) == "" {
			return fmt.Errorf("cron_expr is required")
		}
	default:
		return fmt.Errorf("schedule_type must be %q or %q", ScheduleCron, ScheduleOnce)
	}
	if t.TimeoutSec <= 0 {
		t.TimeoutSec = 300
	}
	return nil
}

// IsOneShot reports whether the task fires a single time instead of on a cron.
func (t *Task) IsOneShot() bool { return t.ScheduleType == ScheduleOnce }

// ParseRunAt parses the run_at value accepted by the API. RFC3339 (what the
// web UI sends) is preferred; bare "YYYY-MM-DD HH:MM:SS" / "YYYY-MM-DDTHH:MM:SS"
// values are interpreted in the server's local zone so curl users can pass the
// wall-clock time they see.
func ParseRunAt(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, fmt.Errorf("run_at is required")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid run_at %q: want RFC3339 or YYYY-MM-DD HH:MM:SS", s)
}

const taskCols = `t.id, t.name, t.prompt, t.profile_id, COALESCE(p.name, ''), t.cron_expr,
	t.schedule_type, t.run_at,
	t.enabled, t.work_dir, t.timeout_sec, t.last_run_at, t.next_run_at, t.last_status,
	t.created_at, t.updated_at`

func scanTask(sc interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var runAt, lastRun, nextRun sql.NullString
	var created, updated string
	err := sc.Scan(&t.ID, &t.Name, &t.Prompt, &t.ProfileID, &t.ProfileName, &t.CronExpr,
		&t.ScheduleType, &runAt,
		&t.Enabled, &t.WorkDir, &t.TimeoutSec, &lastRun, &nextRun, &t.LastStatus,
		&created, &updated)
	if err != nil {
		return t, err
	}
	if t.ScheduleType == "" {
		t.ScheduleType = ScheduleCron
	}
	t.RunAt = nullTime(runAt)
	t.LastRunAt = nullTime(lastRun)
	t.NextRunAt = nullTime(nextRun)
	t.CreatedAt, _ = parseTime(created)
	t.UpdatedAt, _ = parseTime(updated)
	return t, nil
}

// ListTasks returns all tasks, newest first.
func (d *DB) ListTasks() ([]Task, error) {
	rows, err := d.sql.Query(`SELECT ` + taskCols + ` FROM tasks t
		LEFT JOIN profiles p ON p.id = t.profile_id ORDER BY t.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTask loads one task by id.
func (d *DB) GetTask(id int64) (Task, error) {
	row := d.sql.QueryRow(`SELECT `+taskCols+` FROM tasks t
		LEFT JOIN profiles p ON p.id = t.profile_id WHERE t.id = ?`, id)
	t, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// DueTasks returns enabled tasks whose next_run_at has passed. Tasks with a
// null next_run_at are included so a freshly created or edited task is picked
// up by the scheduler even before its first schedule is computed.
func (d *DB) DueTasks(now time.Time) ([]Task, error) {
	rows, err := d.sql.Query(`SELECT `+taskCols+` FROM tasks t
		LEFT JOIN profiles p ON p.id = t.profile_id
		WHERE t.enabled = 1 AND (t.next_run_at IS NULL OR t.next_run_at <= ?)
		ORDER BY t.next_run_at IS NULL DESC, t.next_run_at`, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateTask inserts a task.
func (d *DB) CreateTask(t Task) (Task, error) {
	if err := t.Validate(); err != nil {
		return Task{}, err
	}
	if _, err := d.GetProfile(t.ProfileID); err != nil {
		return Task{}, fmt.Errorf("profile %d: %w", t.ProfileID, err)
	}
	now := formatTime(time.Now())
	res, err := d.sql.Exec(`
INSERT INTO tasks (name, prompt, profile_id, cron_expr, schedule_type, run_at, enabled, work_dir,
    timeout_sec, next_run_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.Name, t.Prompt, t.ProfileID, t.CronExpr, t.ScheduleType, nullableTime(t.RunAt), boolToInt(t.Enabled), t.WorkDir,
		t.TimeoutSec, nullableTime(t.NextRunAt), now, now)
	if err != nil {
		return Task{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Task{}, err
	}
	return d.GetTask(id)
}

// UpdateTask replaces the mutable fields of a task.
func (d *DB) UpdateTask(t Task) (Task, error) {
	if err := t.Validate(); err != nil {
		return Task{}, err
	}
	if _, err := d.GetProfile(t.ProfileID); err != nil {
		return Task{}, fmt.Errorf("profile %d: %w", t.ProfileID, err)
	}
	res, err := d.sql.Exec(`
UPDATE tasks SET name = ?, prompt = ?, profile_id = ?, cron_expr = ?, schedule_type = ?, run_at = ?, enabled = ?,
    work_dir = ?, timeout_sec = ?, next_run_at = ?, updated_at = ?
WHERE id = ?`,
		t.Name, t.Prompt, t.ProfileID, t.CronExpr, t.ScheduleType, nullableTime(t.RunAt), boolToInt(t.Enabled), t.WorkDir,
		t.TimeoutSec, nullableTime(t.NextRunAt), formatTime(time.Now()), t.ID)
	if err != nil {
		return Task{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Task{}, ErrNotFound
	}
	return d.GetTask(t.ID)
}

// MarkTaskRun records the outcome of the most recent run. It deliberately does
// not touch next_run_at: the schedule is advanced separately, before a run
// starts, so that a slow task cannot be re-triggered on every scheduler tick.
func (d *DB) MarkTaskRun(id int64, lastRunAt time.Time, lastStatus string) error {
	_, err := d.sql.Exec(`UPDATE tasks SET last_run_at = ?, last_status = ? WHERE id = ?`,
		formatTime(lastRunAt), lastStatus, id)
	return err
}

// SetTaskNextRun reserves the next scheduled slot for a task. Passing nil
// clears the schedule, which parks the task until it is re-enabled or edited.
func (d *DB) SetTaskNextRun(id int64, nextRunAt *time.Time) error {
	_, err := d.sql.Exec(`UPDATE tasks SET next_run_at = ? WHERE id = ?`,
		nullableTime(nextRunAt), id)
	return err
}

// ConsumeOneShot retires a one-shot task after its single slot came due: the
// schedule is cleared and the task is disabled so DueTasks (which re-includes
// NULL next_run_at rows) never hands it out a second time. The in-flight run
// itself proceeds normally; only future automatic runs are stopped.
func (d *DB) ConsumeOneShot(id int64) error {
	_, err := d.sql.Exec(`UPDATE tasks SET enabled = 0, next_run_at = NULL WHERE id = ?`, id)
	return err
}

// DeleteTask removes a task and its run history.
func (d *DB) DeleteTask(id int64) error {
	res, err := d.sql.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountTasks returns total and enabled task counts.
func (d *DB) CountTasks() (total, enabled int, err error) {
	err = d.sql.QueryRow(`SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM tasks`).Scan(&total, &enabled)
	return total, enabled, err
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

const taskRunCols = `r.id, r.task_id, COALESCE(t.name, ''), r.status, r.trigger, r.output,
	r.error, r.input_tokens, r.output_tokens, r.duration_ms, r.started_at, r.finished_at`

func scanTaskRun(sc interface{ Scan(...any) error }) (TaskRun, error) {
	var r TaskRun
	var started string
	var finished sql.NullString
	err := sc.Scan(&r.ID, &r.TaskID, &r.TaskName, &r.Status, &r.Trigger, &r.Output,
		&r.Error, &r.InputTokens, &r.OutputTokens, &r.DurationMs, &started, &finished)
	if err != nil {
		return r, err
	}
	r.StartedAt, _ = parseTime(started)
	r.FinishedAt = nullTime(finished)
	return r, nil
}

// StartTaskRun opens a run row in the running state.
func (d *DB) StartTaskRun(taskID int64, trigger string) (int64, error) {
	res, err := d.sql.Exec(`
INSERT INTO task_runs (task_id, status, trigger, started_at) VALUES (?,?,?,?)`,
		taskID, "running", trigger, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishTaskRun closes a run row with its outcome.
func (d *DB) FinishTaskRun(id int64, status, output, errText string, inTok, outTok int, durationMs int64) error {
	_, err := d.sql.Exec(`
UPDATE task_runs SET status = ?, output = ?, error = ?, input_tokens = ?,
    output_tokens = ?, duration_ms = ?, finished_at = ? WHERE id = ?`,
		status, output, errText, inTok, outTok, durationMs, formatTime(time.Now()), id)
	return err
}

// ListTaskRuns returns the run history for one task.
func (d *DB) ListTaskRuns(taskID int64, limit int) ([]TaskRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.sql.Query(`SELECT `+taskRunCols+` FROM task_runs r
		LEFT JOIN tasks t ON t.id = r.task_id
		WHERE r.task_id = ? ORDER BY r.id DESC LIMIT ?`, taskID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTaskRuns(rows)
}

// ListRecentRuns returns the newest runs across all tasks.
func (d *DB) ListRecentRuns(limit int) ([]TaskRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := d.sql.Query(`SELECT `+taskRunCols+` FROM task_runs r
		LEFT JOIN tasks t ON t.id = r.task_id ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectTaskRuns(rows)
}

// CountRunsSince counts runs started at or after the given time, used for the
// dashboard's "runs today" figure.
func (d *DB) CountRunsSince(since time.Time) (int, error) {
	var n int
	err := d.sql.QueryRow(`SELECT COUNT(*) FROM task_runs WHERE started_at >= ?`,
		formatTime(since)).Scan(&n)
	return n, err
}

// MarkStaleRunsFailed closes runs left in the running state by a previous
// process, so the UI never shows a permanently spinning row.
func (d *DB) MarkStaleRunsFailed() (int64, error) {
	res, err := d.sql.Exec(`UPDATE task_runs SET status = 'failed',
		error = 'interrupted: the service restarted while this run was in progress',
		finished_at = ? WHERE status = 'running'`, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func collectTaskRuns(rows *sql.Rows) ([]TaskRun, error) {
	out := []TaskRun{}
	for rows.Next() {
		r, err := scanTaskRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
