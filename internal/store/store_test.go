package store

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestMigrationsApplyOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	v1, err := db.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	db.Close()

	// Reopening must be idempotent.
	db2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db2.Close()
	v2, err := db2.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion after reopen: %v", err)
	}
	if v1 != v2 || v1 != len(migrations) {
		t.Fatalf("schema version changed: first=%d second=%d want=%d", v1, v2, len(migrations))
	}
}

func TestProfileCRUDAndValidation(t *testing.T) {
	db := newTestDB(t)

	p, err := db.CreateProfile(Profile{
		Name: "minimal", SandboxMode: "read-only", IsMinimal: true,
		ApprovalPolicy: "never", ReasoningEffort: "low",
	})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}
	if p.ID == 0 || !p.IsMinimal || p.CreatedAt.IsZero() {
		t.Fatalf("unexpected profile: %+v", p)
	}
	if p.IncludePermissionsInstructions || p.IncludeAppsInstructions ||
		p.IncludeCollaborationModeInstructions || p.IncludeEnvironmentContext {
		t.Fatalf("include flags should default off: %+v", p)
	}
	forced := Profile{
		Name: "forced-minimal", SandboxMode: "read-only", IsMinimal: true,
		IncludePermissionsInstructions: true, IncludeEnvironmentContext: true,
	}
	if err := forced.Validate(); err != nil {
		t.Fatalf("minimal profile: %v", err)
	}
	if forced.IncludePermissionsInstructions || forced.IncludeEnvironmentContext {
		t.Fatalf("minimal mode must clear include flags: %+v", forced)
	}

	if _, err := db.CreateProfile(Profile{Name: "minimal", SandboxMode: "read-only"}); err == nil {
		t.Fatal("expected duplicate name to fail")
	}
	if _, err := db.CreateProfile(Profile{Name: "../escape", SandboxMode: "read-only"}); err == nil {
		t.Fatal("expected path-traversal name to fail")
	}
	if _, err := db.CreateProfile(Profile{Name: "ok", SandboxMode: "bogus"}); err == nil {
		t.Fatal("expected invalid sandbox to fail")
	}
	filled := Profile{Name: "defaults"}
	if err := filled.Validate(); err != nil {
		t.Fatalf("empty sandbox/effort/approval must fill defaults: %v", err)
	}
	if filled.SandboxMode != DefaultSandboxMode || filled.ApprovalPolicy != DefaultApprovalPolicy ||
		filled.ReasoningEffort != DefaultReasoningEffort {
		t.Fatalf("defaults not applied: %+v", filled)
	}
	legacy := Profile{Name: "luna", SandboxMode: "read-only", ReasoningEffort: "minimal"}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy effort must remap: %v", err)
	}
	if legacy.ReasoningEffort != DefaultReasoningEffort {
		t.Fatalf("minimal remapped to %q, want %q", legacy.ReasoningEffort, DefaultReasoningEffort)
	}
	badEffort := Profile{Name: "bad-effort", SandboxMode: "read-only", ReasoningEffort: "ultra"}
	if err := badEffort.Validate(); err == nil {
		t.Fatal("expected invalid reasoning effort to fail")
	}

	p.Description = "updated"
	p.IsMinimal = false
	p.IncludeEnvironmentContext = true
	got, err := db.UpdateProfile(p)
	if err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
	if got.Description != "updated" || got.IsMinimal || !got.IncludeEnvironmentContext {
		t.Fatalf("update not applied: %+v", got)
	}

	list, err := db.ListProfiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListProfiles = %v, %v", list, err)
	}

	if err := db.DeleteProfile(p.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, err := db.GetProfile(p.ID); err != ErrNotFound {
		t.Fatalf("GetProfile after delete = %v, want ErrNotFound", err)
	}
}

func TestConversationAndMessages(t *testing.T) {
	db := newTestDB(t)
	p, err := db.CreateProfile(Profile{Name: "p1", SandboxMode: "read-only"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	c, err := db.CreateConversation(p.ID, "")
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}
	if c.Title != DefaultConversationTitle || c.ProfileName != "p1" {
		t.Fatalf("unexpected conversation: %+v", c)
	}

	if err := db.SetConversationThread(c.ID, "thread-abc"); err != nil {
		t.Fatalf("SetConversationThread: %v", err)
	}
	if _, err := db.AddMessage(Message{ConversationID: c.ID, Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("AddMessage: %v", err)
	}
	assistant, err := db.AddMessage(Message{ConversationID: c.ID, Role: "assistant", Content: "hello", Status: "ok"})
	if err != nil {
		t.Fatalf("AddMessage assistant: %v", err)
	}
	if err := db.FinalizeMessage(assistant.ID, "hello there", "ok", "", 100, 5, 250); err != nil {
		t.Fatalf("FinalizeMessage: %v", err)
	}

	msgs, err := db.ListMessages(c.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("ListMessages len = %d, want 2", len(msgs))
	}
	if msgs[1].Content != "hello there" || msgs[1].InputTokens != 100 || msgs[1].DurationMs != 250 {
		t.Fatalf("finalize not applied: %+v", msgs[1])
	}

	reloaded, err := db.GetConversation(c.ID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if reloaded.MessageCount != 2 || reloaded.ThreadID != "thread-abc" {
		t.Fatalf("unexpected conversation after messages: %+v", reloaded)
	}

	// Deleting the profile must cascade to conversations and messages.
	if err := db.DeleteProfile(p.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if _, err := db.GetConversation(c.ID); err != ErrNotFound {
		t.Fatalf("conversation survived profile delete: %v", err)
	}
	if msgs, _ := db.ListMessages(c.ID); len(msgs) != 0 {
		t.Fatalf("messages survived cascade delete: %d", len(msgs))
	}
}

func TestTaskLifecycleAndDueSelection(t *testing.T) {
	db := newTestDB(t)
	p, err := db.CreateProfile(Profile{Name: "p1", SandboxMode: "read-only"})
	if err != nil {
		t.Fatalf("CreateProfile: %v", err)
	}

	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(time.Hour)

	due, err := db.CreateTask(Task{
		Name: "due", Prompt: "hello", ProfileID: p.ID,
		CronExpr: "*/5 * * * *", Enabled: true, NextRunAt: &past,
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := db.CreateTask(Task{
		Name: "later", Prompt: "hello", ProfileID: p.ID,
		CronExpr: "0 0 * * *", Enabled: true, NextRunAt: &future,
	}); err != nil {
		t.Fatalf("CreateTask later: %v", err)
	}
	if _, err := db.CreateTask(Task{
		Name: "disabled", Prompt: "hello", ProfileID: p.ID,
		CronExpr: "*/5 * * * *", Enabled: false, NextRunAt: &past,
	}); err != nil {
		t.Fatalf("CreateTask disabled: %v", err)
	}

	got, err := db.DueTasks(time.Now())
	if err != nil {
		t.Fatalf("DueTasks: %v", err)
	}
	if len(got) != 1 || got[0].Name != "due" {
		t.Fatalf("DueTasks = %+v, want only the due task", got)
	}

	if _, err := db.CreateTask(Task{Name: "bad", Prompt: "x", ProfileID: p.ID, CronExpr: ""}); err == nil {
		t.Fatal("expected missing cron to fail validation")
	}
	if _, err := db.CreateTask(Task{Name: "no-profile", Prompt: "x", ProfileID: 9999, CronExpr: "* * * * *"}); err == nil {
		t.Fatal("expected unknown profile to fail")
	}

	// A run must be openable and closable.
	runID, err := db.StartTaskRun(due.ID, "schedule")
	if err != nil {
		t.Fatalf("StartTaskRun: %v", err)
	}
	if stale, err := db.MarkStaleRunsFailed(); err != nil || stale != 1 {
		t.Fatalf("MarkStaleRunsFailed = %d, %v; want 1", stale, err)
	}
	if err := db.FinishTaskRun(runID, "success", "output text", "", 10, 2, 1200); err != nil {
		t.Fatalf("FinishTaskRun: %v", err)
	}

	runs, err := db.ListTaskRuns(due.ID, 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListTaskRuns = %+v, %v", runs, err)
	}
	// MarkStaleRunsFailed ran first, so the row is failed then overwritten to success.
	if runs[0].Status != "success" || runs[0].Output != "output text" || runs[0].FinishedAt == nil {
		t.Fatalf("unexpected run: %+v", runs[0])
	}

	if err := db.MarkTaskRun(due.ID, time.Now(), "success"); err != nil {
		t.Fatalf("MarkTaskRun: %v", err)
	}
	if err := db.SetTaskNextRun(due.ID, &future); err != nil {
		t.Fatalf("SetTaskNextRun: %v", err)
	}
	updated, err := db.GetTask(due.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if updated.LastStatus != "success" || updated.LastRunAt == nil || updated.NextRunAt == nil {
		t.Fatalf("schedule not persisted: %+v", updated)
	}

	// Clearing the schedule must park the task.
	if err := db.SetTaskNextRun(due.ID, nil); err != nil {
		t.Fatalf("SetTaskNextRun(nil): %v", err)
	}
	if parked, _ := db.GetTask(due.ID); parked.NextRunAt != nil {
		t.Fatalf("NextRunAt = %v, want nil after clearing", parked.NextRunAt)
	}

	total, enabled, err := db.CountTasks()
	if err != nil || total != 3 || enabled != 2 {
		t.Fatalf("CountTasks = %d/%d, %v; want 3/2", total, enabled, err)
	}

	if n, err := db.CountRunsSince(time.Now().Add(-time.Hour)); err != nil || n != 1 {
		t.Fatalf("CountRunsSince = %d, %v; want 1", n, err)
	}

	if err := db.DeleteTask(due.ID); err != nil {
		t.Fatalf("DeleteTask: %v", err)
	}
	if runs, _ := db.ListTaskRuns(due.ID, 10); len(runs) != 0 {
		t.Fatalf("runs survived task delete: %d", len(runs))
	}
}
