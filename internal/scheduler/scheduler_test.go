package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"codeck/internal/codex"
	"codeck/internal/store"
)

// fakeRunner records every invocation and returns a canned result, so the
// scheduler can be exercised without spending real API calls.
type fakeRunner struct {
	mu    sync.Mutex
	calls []codex.RunOptions
	// result and err are returned for every call.
	result codex.Result
	err    error
	// block, when non-nil, holds each call until it is closed.
	block chan struct{}
	// started receives a signal each time a call begins.
	started chan struct{}
}

func (f *fakeRunner) Run(ctx context.Context, opts codex.RunOptions) (codex.Result, error) {
	f.mu.Lock()
	f.calls = append(f.calls, opts)
	f.mu.Unlock()

	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return f.result, ctx.Err()
		}
	}
	return f.result, f.err
}

func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) lastCall(t *testing.T) codex.RunOptions {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		t.Fatal("runner was never called")
	}
	return f.calls[len(f.calls)-1]
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestScheduler(t *testing.T, runner Runner) (*Scheduler, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	s := New(db, runner, time.Second, 2, testLogger())
	t.Cleanup(s.Stop)
	return s, db
}

func mustProfile(t *testing.T, db *store.DB) store.Profile {
	t.Helper()
	p, err := db.CreateProfile(store.Profile{
		Name: "p1", SandboxMode: "read-only", IsMinimal: true, ApprovalPolicy: "never",
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	return p
}

// waitFor polls until cond is true, failing the test on timeout.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestParseAcceptsStandardSyntaxAndRejectsSeconds(t *testing.T) {
	for _, expr := range []string{"*/5 * * * *", "0 9 * * 1-5", "@daily", "@every 1h", "0 0 1 * *"} {
		if err := Validate(expr); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", expr, err)
		}
	}
	for _, expr := range []string{"", "   ", "not a cron", "* * * * * *", "*/0 * * * *"} {
		if err := Validate(expr); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", expr)
		}
	}
}

func TestNextRunIsAfterTheGivenTime(t *testing.T) {
	from := time.Date(2026, 3, 1, 10, 30, 0, 0, time.UTC)
	next, err := NextRun("0 * * * *", from)
	if err != nil {
		t.Fatalf("NextRun: %v", err)
	}
	want := time.Date(2026, 3, 1, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Errorf("NextRun = %s, want %s", next, want)
	}
}

func TestDispatchRunsDueTaskAndAdvancesSchedule(t *testing.T) {
	runner := &fakeRunner{
		result:  codex.Result{ThreadID: "t1", Output: "task output", Usage: codex.Usage{InputTokens: 50, OutputTokens: 9}},
		started: make(chan struct{}, 1),
	}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	// A task that came due in the past.
	past := time.Now().Add(-time.Minute)
	task, err := db.CreateTask(store.Task{
		Name: "due", Prompt: "do the thing", ProfileID: p.ID,
		CronExpr: "*/5 * * * *", Enabled: true, TimeoutSec: 60, NextRunAt: &past,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	s.dispatchDue()
	<-runner.started

	waitFor(t, "run to be recorded", func() bool {
		runs, _ := db.ListTaskRuns(task.ID, 10)
		return len(runs) == 1 && runs[0].Status != "running"
	})

	runs, _ := db.ListTaskRuns(task.ID, 10)
	if runs[0].Status != "success" {
		t.Errorf("run status = %q, want success", runs[0].Status)
	}
	if runs[0].Output != "task output" {
		t.Errorf("run output = %q", runs[0].Output)
	}
	if runs[0].Trigger != "schedule" {
		t.Errorf("run trigger = %q, want schedule", runs[0].Trigger)
	}
	if runs[0].InputTokens != 50 || runs[0].OutputTokens != 9 {
		t.Errorf("token usage not recorded: %+v", runs[0])
	}

	// The prompt and profile must reach the runner.
	call := runner.lastCall(t)
	if call.Prompt != "do the thing" {
		t.Errorf("prompt = %q", call.Prompt)
	}
	if !call.Spec.IsMinimal || call.Spec.Name != "p1" {
		t.Errorf("spec not mapped from profile: %+v", call.Spec)
	}
	if call.Timeout != 60*time.Second {
		t.Errorf("timeout = %s, want 60s", call.Timeout)
	}

	// The schedule must have advanced into the future so the task is not due again.
	updated, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(time.Now()) {
		t.Errorf("NextRunAt = %v, want a future time", updated.NextRunAt)
	}
	if updated.LastStatus != "success" || updated.LastRunAt == nil {
		t.Errorf("last run not recorded: %+v", updated)
	}
}

func TestInFlightTaskIsNotRunTwice(t *testing.T) {
	release := make(chan struct{})
	runner := &fakeRunner{
		result:  codex.Result{Output: "slow"},
		block:   release,
		started: make(chan struct{}, 4),
	}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	past := time.Now().Add(-time.Minute)
	task, err := db.CreateTask(store.Task{
		Name: "slow", Prompt: "x", ProfileID: p.ID,
		CronExpr: "* * * * *", Enabled: true, TimeoutSec: 60, NextRunAt: &past,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	s.dispatchDue()
	<-runner.started

	// Make it due again while the first run is still blocked.
	if err := db.SetTaskNextRun(task.ID, &past); err != nil {
		t.Fatalf("SetTaskNextRun: %v", err)
	}
	s.dispatchDue()

	close(release)
	waitFor(t, "run to be recorded", func() bool {
		runs, _ := db.ListTaskRuns(task.ID, 10)
		return len(runs) == 1 && runs[0].Status != "running"
	})

	if n := runner.callCount(); n != 1 {
		t.Errorf("runner called %d times, want 1: a task must not overlap itself", n)
	}
}

func TestManualRunDoesNotDisturbSchedule(t *testing.T) {
	runner := &fakeRunner{
		result:  codex.Result{Output: "manual"},
		started: make(chan struct{}, 1),
	}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	future := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	task, err := db.CreateTask(store.Task{
		Name: "later", Prompt: "x", ProfileID: p.ID,
		CronExpr: "0 0 * * *", Enabled: true, TimeoutSec: 60, NextRunAt: &future,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	runID, err := s.RunNow(task.ID)
	if err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	if runID == 0 {
		t.Fatal("RunNow must return the new run id")
	}
	<-runner.started

	waitFor(t, "manual run to finish", func() bool {
		runs, _ := db.ListTaskRuns(task.ID, 10)
		return len(runs) == 1 && runs[0].Status != "running"
	})

	runs, _ := db.ListTaskRuns(task.ID, 10)
	if runs[0].Trigger != "manual" {
		t.Errorf("trigger = %q, want manual", runs[0].Trigger)
	}

	updated, _ := db.GetTask(task.ID)
	if updated.NextRunAt == nil || !updated.NextRunAt.Equal(future) {
		t.Errorf("NextRunAt = %v, want it unchanged at %v", updated.NextRunAt, future)
	}
}

func TestFailedRunIsRecordedWithError(t *testing.T) {
	runner := &fakeRunner{
		result:  codex.Result{Output: "partial text", Usage: codex.Usage{InputTokens: 5}},
		err:     errors.New("codex failed: model unavailable"),
		started: make(chan struct{}, 1),
	}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	past := time.Now().Add(-time.Minute)
	task, _ := db.CreateTask(store.Task{
		Name: "failing", Prompt: "x", ProfileID: p.ID,
		CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &past,
	})

	s.dispatchDue()
	<-runner.started
	waitFor(t, "failed run to be recorded", func() bool {
		runs, _ := db.ListTaskRuns(task.ID, 10)
		return len(runs) == 1 && runs[0].Status == "failed"
	})

	runs, _ := db.ListTaskRuns(task.ID, 10)
	if runs[0].Error == "" {
		t.Error("error message was not stored")
	}
	// Output produced before the failure is still kept.
	if runs[0].Output != "partial text" {
		t.Errorf("output = %q, want the partial text to be preserved", runs[0].Output)
	}

	updated, _ := db.GetTask(task.ID)
	if updated.LastStatus != "failed" {
		t.Errorf("LastStatus = %q, want failed", updated.LastStatus)
	}
	if updated.NextRunAt == nil || !updated.NextRunAt.After(time.Now()) {
		t.Errorf("a failed run must still advance the schedule, got %v", updated.NextRunAt)
	}
}

func TestBrokenCronParksTaskInsteadOfSpinning(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1)}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	// The HTTP layer validates expressions on write, but a row can still end up
	// with an unparseable one (hand-edited database, or cron semantics changing
	// between releases). The scheduler must park it rather than retry forever.
	past := time.Now().Add(-time.Minute)
	task, err := db.CreateTask(store.Task{
		Name: "broken", Prompt: "x", ProfileID: p.ID,
		CronExpr: "not a cron", Enabled: true, NextRunAt: &past,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	s.dispatchDue()

	if n := runner.callCount(); n != 0 {
		t.Errorf("runner called %d times for an unparseable cron, want 0", n)
	}
	updated, _ := db.GetTask(task.ID)
	if updated.NextRunAt != nil {
		t.Errorf("NextRunAt = %v, want nil so the task is parked", updated.NextRunAt)
	}
	if updated.Enabled != true {
		t.Error("parking must not silently disable the task")
	}
}

func TestDisabledTaskIsNeverDispatched(t *testing.T) {
	runner := &fakeRunner{started: make(chan struct{}, 1)}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	past := time.Now().Add(-time.Minute)
	if _, err := db.CreateTask(store.Task{
		Name: "off", Prompt: "x", ProfileID: p.ID,
		CronExpr: "* * * * *", Enabled: false, NextRunAt: &past,
	}); err != nil {
		t.Fatal(err)
	}

	s.dispatchDue()
	if n := runner.callCount(); n != 0 {
		t.Errorf("runner called %d times for a disabled task, want 0", n)
	}
}

func TestRunNowRejectsUnknownTask(t *testing.T) {
	s, _ := newTestScheduler(t, &fakeRunner{})
	if _, err := s.RunNow(4242); err == nil {
		t.Error("RunNow for a missing task should fail")
	}
}

func TestRunNowRefusesToOverlapARunningTask(t *testing.T) {
	release := make(chan struct{})
	runner := &fakeRunner{
		result:  codex.Result{Output: "slow"},
		block:   release,
		started: make(chan struct{}, 2),
	}
	s, db := newTestScheduler(t, runner)
	p := mustProfile(t, db)

	task, err := db.CreateTask(store.Task{
		Name: "t", Prompt: "x", ProfileID: p.ID, CronExpr: "* * * * *",
		Enabled: true, TimeoutSec: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.RunNow(task.ID); err != nil {
		t.Fatalf("first RunNow: %v", err)
	}
	<-runner.started

	if _, err := s.RunNow(task.ID); err == nil {
		t.Error("a second concurrent manual run should be refused")
	}

	close(release)
	waitFor(t, "first run to finish", func() bool {
		runs, _ := db.ListTaskRuns(task.ID, 10)
		return len(runs) == 1 && runs[0].Status != "running"
	})
	if n := runner.callCount(); n != 1 {
		t.Errorf("runner called %d times, want 1", n)
	}
}


func TestDisabledSchedulerStillOwnsManualRunLifecycle(t *testing.T) {
	runner := &fakeRunner{
		block:   make(chan struct{}),
		started: make(chan struct{}, 1),
	}
	s, db := newTestScheduler(t, runner)
	ctx, cancel := context.WithCancel(context.Background())
	s.Start(ctx, false)

	p := mustProfile(t, db)
	task, err := db.CreateTask(store.Task{
		Name: "manual-only", Prompt: "x", ProfileID: p.ID,
		CronExpr: "* * * * *", Enabled: true, TimeoutSec: 60,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RunNow(task.ID); err != nil {
		t.Fatalf("RunNow: %v", err)
	}
	<-runner.started

	cancel()
	stopped := make(chan struct{})
	go func() {
		s.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not wait for/cancel the manual worker")
	}

	runs, err := db.ListTaskRuns(task.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Status != "failed" {
		t.Fatalf("cancelled manual run = %+v, want one failed run", runs)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCount())
	}
}
