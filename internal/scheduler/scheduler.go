// Package scheduler owns everything about running Codex on a schedule: cron
// expression handling, deciding what is due, executing it and recording the
// outcome. It is self-contained; the HTTP layer only validates expressions and
// asks for manual runs through it.
package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"codeck/internal/codex"
	"codeck/internal/store"
)

// parser accepts the standard five-field cron syntax plus @descriptors such as
// @daily and @every 1h. It rejects six-field (seconds) expressions.
var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// NextRun returns the first time after `from` at which expr fires.
func NextRun(expr string, from time.Time) (time.Time, error) {
	schedule, err := Parse(expr)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// Parse validates a cron expression and returns its schedule.
func Parse(expr string) (cron.Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return nil, fmt.Errorf("cron expression is required")
	}
	schedule, err := parser.Parse(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression %q: %w", expr, err)
	}
	return schedule, nil
}

// Validate reports whether a cron expression is usable.
func Validate(expr string) error {
	_, err := Parse(expr)
	return err
}

// Runner executes a single Codex turn. *codex.Service satisfies it; the
// interface exists so the scheduler can be tested without invoking the CLI.
type Runner interface {
	Run(ctx context.Context, opts codex.RunOptions) (codex.Result, error)
}

// Scheduler polls the database for due tasks and runs them.
//
// Scheduling state lives in the database rather than in memory so that a
// restart resumes correctly and the web UI can show the next run time. That is
// why the cron library is used only as an expression parser: the loop below
// decides what to run.
type Scheduler struct {
	db            *store.DB
	codex         Runner
	interval      time.Duration
	maxConcurrent int
	log           *slog.Logger

	// now is injectable so tests can control the clock.
	now func() time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// slots bounds how many Codex processes run at once.
	slots chan struct{}
	// running guards against a task overlapping with itself.
	running sync.Map
}

// New constructs a Scheduler. interval is the due-task poll period.
func New(db *store.DB, svc Runner, interval time.Duration, maxConcurrent int, log *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	if maxConcurrent < 1 {
		maxConcurrent = 1
	}
	return &Scheduler{
		db:            db,
		codex:         svc,
		interval:      interval,
		maxConcurrent: maxConcurrent,
		log:           log,
		now:           time.Now,
		slots:         make(chan struct{}, maxConcurrent),
		// A usable context before Start so dispatch can be driven directly in
		// tests and so Stop is safe to call unconditionally.
		ctx: context.Background(),
	}
}

// Start begins polling. It returns immediately; Stop waits for in-flight runs.
func (s *Scheduler) Start(ctx context.Context) {
	s.ctx, s.cancel = context.WithCancel(ctx)
	s.wg.Add(1)
	go s.loop()
	s.log.Info("scheduler started", "interval", s.interval, "max_concurrent", s.maxConcurrent)
}

// Stop cancels the loop and waits for running tasks to finish. It is safe to
// call when the scheduler was never started.
func (s *Scheduler) Stop() {
	if s.cancel == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
	s.log.Info("scheduler stopped")
}

// RunNow starts an immediate manual run and returns the id of the new run
// record. The work itself proceeds in the background; its outcome appears in
// the task's run history.
func (s *Scheduler) RunNow(taskID int64) (int64, error) {
	task, err := s.db.GetTask(taskID)
	if err != nil {
		return 0, err
	}
	// A manual run deliberately leaves next_run_at untouched so the cron
	// schedule is unaffected.
	return s.spawn(task, "manual")
}

// Running reports how many Codex processes the scheduler is currently running.
func (s *Scheduler) Running() int { return len(s.slots) }

func (s *Scheduler) loop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	// Run an initial pass so due work is picked up without waiting a full tick.
	s.dispatchDue()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.dispatchDue()
		}
	}
}

// dispatchDue claims every task whose scheduled time has arrived.
//
// A task's next_run_at is advanced before its run starts. That reservation is
// what stops a task that takes longer than its interval from being launched
// again on the next tick.
func (s *Scheduler) dispatchDue() {
	now := s.now()
	due, err := s.db.DueTasks(now)
	if err != nil {
		s.log.Error("failed to load due tasks", "error", err)
		return
	}

	for _, task := range due {
		trigger := "schedule"
		if task.IsOneShot() {
			trigger = "once"
		}
		if err := s.reserveNext(task, now); err != nil {
			s.log.Error("cannot compute next run; parking this task",
				"task", task.ID, "name", task.Name, "cron", task.CronExpr, "error", err)
			continue
		}
		s.spawn(task, trigger)
	}
}

// reserveNext advances the task's schedule so the slot it just came due for is
// not handed out a second time.
func (s *Scheduler) reserveNext(task store.Task, now time.Time) error {
	if task.IsOneShot() {
		// A one-shot task has no next slot: retire it (disable + clear the
		// schedule) so the NULL-next_run_at catch-all in DueTasks never
		// re-triggers it. The current run still proceeds with the copy of
		// the task already loaded above.
		if task.RunAt == nil {
			_ = s.db.ConsumeOneShot(task.ID)
			return fmt.Errorf("one-shot task %d has no run_at", task.ID)
		}
		if err := s.db.ConsumeOneShot(task.ID); err != nil {
			return err
		}
		return nil
	}
	next, err := NextRun(task.CronExpr, now)
	if err != nil {
		// Park the task so a broken expression cannot spin the scheduler.
		_ = s.db.SetTaskNextRun(task.ID, nil)
		return err
	}
	return s.db.SetTaskNextRun(task.ID, &next)
}

// spawn opens a run record and launches the work in the background, bounded by
// the concurrency slots. It returns the new run's id so a manual trigger can
// report it immediately.
//
// It fails if the task is already running, which is what stops a task whose
// duration exceeds its interval from overlapping with itself.
func (s *Scheduler) spawn(task store.Task, trigger string) (int64, error) {
	if _, loaded := s.running.LoadOrStore(task.ID, true); loaded {
		s.log.Warn("skipping run: the previous run of this task is still in progress",
			"task", task.ID, "name", task.Name, "trigger", trigger)
		return 0, fmt.Errorf("task %d is already running", task.ID)
	}

	runID, err := s.db.StartTaskRun(task.ID, trigger)
	if err != nil {
		s.running.Delete(task.ID)
		return 0, fmt.Errorf("open run record: %w", err)
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.running.Delete(task.ID)

		// Acquire a slot after the overlap check so queued work does not hold
		// a concurrency slot while it waits.
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		case <-s.ctx.Done():
			s.log.Info("cancelled before starting", "task", task.ID)
			// Close the record opened above so it does not linger as running.
			_ = s.db.FinishTaskRun(runID, "failed", "", "cancelled: the service is shutting down", 0, 0, 0)
			return
		}

		s.execute(task, runID, trigger)
	}()
	return runID, nil
}

// execute runs one task and persists everything about the attempt.
func (s *Scheduler) execute(task store.Task, runID int64, trigger string) {
	started := s.now()
	log := s.log.With("task", task.ID, "name", task.Name, "trigger", trigger)

	profile, err := s.db.GetProfile(task.ProfileID)
	if err != nil {
		s.finish(task, runID, started, "failed",
			"", fmt.Sprintf("profile %d is unavailable: %v", task.ProfileID, err))
		log.Error("task profile missing", "error", err)
		return
	}

	timeout := time.Duration(task.TimeoutSec) * time.Second
	log.Info("run started", "profile", profile.Name, "timeout", timeout)

	result, runErr := s.codex.Run(s.ctx, codex.RunOptions{
		Spec:    codex.SpecFromProfile(profile),
		Prompt:  task.Prompt,
		Timeout: timeout,
	})

	status := "success"
	errText := ""
	if runErr != nil {
		status = "failed"
		errText = runErr.Error()
	}

	// A run that produced text is worth keeping even if it then failed.
	output := result.Output
	if err := s.db.FinishTaskRun(runID, status, output, errText,
		result.Usage.InputTokens, result.Usage.OutputTokens, result.Duration.Milliseconds()); err != nil {
		log.Error("failed to record run result", "error", err)
	}
	if err := s.db.MarkTaskRun(task.ID, started, status); err != nil {
		log.Error("failed to update task state", "error", err)
	}

	if runErr != nil {
		log.Error("run failed", "error", runErr, "duration", result.Duration)
	} else {
		log.Info("run finished", "duration", result.Duration,
			"input_tokens", result.Usage.InputTokens, "output_tokens", result.Usage.OutputTokens)
	}
}

func (s *Scheduler) finish(task store.Task, runID int64, started time.Time, status, output, errText string) {
	if err := s.db.FinishTaskRun(runID, status, output, errText, 0, 0, 0); err != nil {
		s.log.Error("failed to record run result", "task", task.ID, "error", err)
	}
	if err := s.db.MarkTaskRun(task.ID, started, status); err != nil {
		s.log.Error("failed to update task state", "task", task.ID, "error", err)
	}
}
