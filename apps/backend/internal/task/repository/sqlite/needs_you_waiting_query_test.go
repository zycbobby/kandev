package sqlite

// Coverage for the derived manual-step waiting read (needs-you-inbox-waiting-
// rows design D3/D4): SQL narrows candidates by workspace, non-terminal state,
// complete_task_on_enter=0, no STARTING/RUNNING session, and no queued
// message; the StepRunsOnEntry judgment runs once in Go over a bounded
// overscan; a task with no session still lists; one task yields one row whose
// identity changes per episode.

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/kandev/kandev/internal/task/models"
)

// waitFixture is one seeded task's shape. state "" seeds TODO; sessionState ""
// seeds no session at all.
type waitFixture struct {
	taskID              string
	workspaceID         string
	stepID              string
	stepName            string
	eventsJSON          string // "" seeds "{}" (manual step, no on_enter actions)
	completeTaskOnEnter bool
	state               string
	archived            bool
	sessionState        string
	queuedMessage       bool
	pullFromStepID      string
}

func newWaitingTestRepo(t *testing.T) *Repository {
	t.Helper()
	return newRepoForSessionTests(t)
}

// seedWaitingStep inserts one workflow_steps row for the fixture's step.
func seedWaitingStep(t *testing.T, repo *Repository, fx waitFixture) {
	t.Helper()
	events := fx.eventsJSON
	if events == "" {
		events = "{}"
	}
	completeOnEnter := 0
	if fx.completeTaskOnEnter {
		completeOnEnter = 1
	}
	if _, err := repo.db.Exec(repo.db.Rebind(`
		INSERT INTO workflow_steps (id, workflow_id, name, position, events, complete_task_on_enter, pull_from_step_id, created_at, updated_at)
		VALUES (?, 'wf-wait', ?, 0, ?, ?, ?, ?, ?)
	`), fx.stepID, fx.stepName, events, completeOnEnter, fx.pullFromStepID, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed workflow step %s: %v", fx.stepID, err)
	}
}

// seedWaitingTask inserts the task, and optionally its session and one queued
// message, for the fixture.
func seedWaitingTask(t *testing.T, repo *Repository, fx waitFixture) {
	t.Helper()
	state := fx.state
	if state == "" {
		state = "TODO"
	}
	archivedAt := interface{}(nil)
	if fx.archived {
		archivedAt = time.Now().UTC()
	}
	if _, err := repo.db.Exec(repo.db.Rebind(`
		INSERT INTO tasks (id, workspace_id, workflow_id, workflow_step_id, title, state, archived_at, created_at, updated_at)
		VALUES (?, ?, 'wf-wait', ?, ?, ?, ?, ?, ?)
	`), fx.taskID, fx.workspaceID, fx.stepID, "task "+fx.taskID, state, archivedAt, time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("seed task %s: %v", fx.taskID, err)
	}
	if fx.sessionState != "" {
		if _, err := repo.db.Exec(repo.db.Rebind(`
			INSERT INTO task_sessions (id, task_id, state, started_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
		`), "sess-"+fx.taskID, fx.taskID, fx.sessionState, time.Now().UTC(), time.Now().UTC()); err != nil {
			t.Fatalf("seed session for %s: %v", fx.taskID, err)
		}
	}
	if fx.queuedMessage {
		ensureQueuedMessagesTable(t, repo)
		if _, err := repo.db.Exec(repo.db.Rebind(`
			INSERT INTO queued_messages (id, session_id, task_id, position, queued_at)
			VALUES (?, ?, ?, 0, ?)
		`), "qm-"+fx.taskID, "sess-"+fx.taskID, fx.taskID, time.Now().UTC()); err != nil {
			t.Fatalf("seed queued message for %s: %v", fx.taskID, err)
		}
	}
}

// ensureQueuedMessagesTable creates the queue-owned table the production
// messagequeue repository owns, for tests that exercise the queued-turn
// predicate.
func ensureQueuedMessagesTable(t *testing.T, repo *Repository) {
	t.Helper()
	if _, err := repo.db.Exec(`
		CREATE TABLE IF NOT EXISTS queued_messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			task_id TEXT NOT NULL,
			position INTEGER NOT NULL,
			content TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			plan_mode INTEGER NOT NULL DEFAULT 0,
			attachments_json TEXT NOT NULL DEFAULT '[]',
			metadata_json TEXT NOT NULL DEFAULT '{}',
			queued_at TIMESTAMP NOT NULL,
			queued_by TEXT NOT NULL DEFAULT ''
		)
	`); err != nil {
		t.Fatalf("create queued_messages: %v", err)
	}
}

// onEnterEvents builds a workflow_steps.events document with one on_enter
// action of the given type.
func onEnterEvents(actionType string) string {
	doc := map[string]any{
		"on_enter": []map[string]any{{"type": actionType}},
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// manualStepFixture is the control shape that must always list.
func manualStepFixture(taskID string) waitFixture {
	return waitFixture{taskID: taskID, workspaceID: "ws-wait", stepID: "step-manual", stepName: "Manual review"}
}

func listWaits(t *testing.T, repo *Repository, opts models.ManualStepWaitOptions) *models.ManualStepWaitPage {
	t.Helper()
	page, err := repo.ListManualStepWaits(context.Background(), opts)
	if err != nil {
		t.Fatalf("ListManualStepWaits: %v", err)
	}
	return page
}

func waitRowTaskIDs(page *models.ManualStepWaitPage) []string {
	ids := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		ids = append(ids, row.TaskID)
	}
	return ids
}

func TestListManualStepWaits_ListsSessionlessTaskAtManualStep(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	seedWaitingTask(t, repo, manualStepFixture("t1"))

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (sessionless manual-step task lists)", len(page.Rows))
	}
	row := page.Rows[0]
	if row.TaskID != "t1" || row.StepID != "step-manual" || row.StepName != "Manual review" {
		t.Fatalf("row = %+v, want task t1 at step-manual", row)
	}
	if row.TaskTitle == "" {
		t.Error("TaskTitle empty, want the task's title")
	}
	if row.WorkspaceID != "ws-wait" {
		t.Errorf("WorkspaceID = %q, want ws-wait", row.WorkspaceID)
	}
	if page.HasMore {
		t.Error("HasMore = true, want false below the bound")
	}
}

func TestListManualStepWaits_SQLNarrowing(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	seedWaitingStep(t, repo, waitFixture{stepID: "step-auto", stepName: "Auto", eventsJSON: onEnterEvents("auto_start_agent")})
	seedWaitingStep(t, repo, waitFixture{stepID: "step-complete", stepName: "Complete", completeTaskOnEnter: true})

	cases := []struct {
		name string
		fx   waitFixture
	}{
		{"other workspace", waitFixture{taskID: "t-other-ws", workspaceID: "ws-other", stepID: "step-manual"}},
		{"terminal state", waitFixture{taskID: "t-done", workspaceID: "ws-wait", stepID: "step-manual", state: "COMPLETED"}},
		{"failed state", waitFixture{taskID: "t-failed", workspaceID: "ws-wait", stepID: "step-manual", state: "FAILED"}},
		{"cancelled state", waitFixture{taskID: "t-cancelled", workspaceID: "ws-wait", stepID: "step-manual", state: "CANCELLED"}},
		{"archived", waitFixture{taskID: "t-archived", workspaceID: "ws-wait", stepID: "step-manual", archived: true}},
		{"complete-on-enter step", waitFixture{taskID: "t-complete", workspaceID: "ws-wait", stepID: "step-complete"}},
		{"running session", waitFixture{taskID: "t-running", workspaceID: "ws-wait", stepID: "step-manual", sessionState: "RUNNING"}},
		{"starting session", waitFixture{taskID: "t-starting", workspaceID: "ws-wait", stepID: "step-manual", sessionState: "STARTING"}},
		{"queued message", waitFixture{taskID: "t-queued", workspaceID: "ws-wait", stepID: "step-manual", queuedMessage: true}},
		{"auto-start on_enter step", waitFixture{taskID: "t-auto", workspaceID: "ws-wait", stepID: "step-auto"}},
	}
	for _, tc := range cases {
		seedWaitingTask(t, repo, tc.fx)
	}
	seedWaitingTask(t, repo, manualStepFixture("t-listed"))

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if ids := waitRowTaskIDs(page); len(ids) != 1 || ids[0] != "t-listed" {
		t.Fatalf("rows = %v, want exactly [t-listed]", ids)
	}
}

func TestListManualStepWaits_StepRunsOnEntryFilteredInGo(t *testing.T) {
	repo := newWaitingTestRepo(t)
	// reset_agent_context is a real non-starting on_enter action: the step is
	// still a manual step and must list.
	seedWaitingStep(t, repo, waitFixture{stepID: "step-reset", stepName: "Reset", eventsJSON: onEnterEvents("reset_agent_context")})
	seedWaitingStep(t, repo, waitFixture{stepID: "step-queue", stepName: "Queue", eventsJSON: onEnterEvents("queue_run")})
	seedWaitingStep(t, repo, waitFixture{stepID: "step-pull", stepName: "Pull", pullFromStepID: "step-other"})
	seedWaitingTask(t, repo, waitFixture{taskID: "t-reset", workspaceID: "ws-wait", stepID: "step-reset"})
	seedWaitingTask(t, repo, waitFixture{taskID: "t-queue", workspaceID: "ws-wait", stepID: "step-queue"})
	seedWaitingTask(t, repo, waitFixture{taskID: "t-pull", workspaceID: "ws-wait", stepID: "step-pull"})

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if ids := waitRowTaskIDs(page); len(ids) != 1 || ids[0] != "t-reset" {
		t.Fatalf("rows = %v, want exactly [t-reset] (reset_agent_context stays; queue_run and pull-from do not)", ids)
	}
}

// TestListManualStepWaits_AllCandidatesAutoStart_NoRowsAndNoFalseTruncation
// pins the design's overscan risk mitigation: a candidate set made only of
// steps that start work on entry yields zero rows and does not report
// has_more.
func TestListManualStepWaits_AllCandidatesAutoStart_NoRowsAndNoFalseTruncation(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, waitFixture{stepID: "step-auto", stepName: "Auto", eventsJSON: onEnterEvents("auto_start_agent")})
	for i := 0; i < 5; i++ {
		seedWaitingTask(t, repo, waitFixture{
			taskID: fmt.Sprintf("t-auto-%d", i), workspaceID: "ws-wait", stepID: "step-auto",
		})
	}

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 0 {
		t.Fatalf("rows = %d, want 0", len(page.Rows))
	}
	if page.HasMore {
		t.Error("HasMore = true, want false: the filtered-out candidate set is fully scanned and empty")
	}
}

func TestListManualStepWaits_OneRowPerTask_LatestEpisodeIdentity(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	seedWaitingStep(t, repo, waitFixture{stepID: "step-other", stepName: "Other"})

	if err := repo.CreateTask(context.Background(), &models.Task{
		ID: "t-episode", WorkspaceID: "ws-wait", WorkflowID: "wf-wait", WorkflowStepID: "step-other", Title: "Episode",
	}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	// Move into the manual step: the transition row that move writes is the
	// episode identity.
	moved, err := repo.GetTask(context.Background(), "t-episode")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	moved.WorkflowStepID = "step-manual"
	if err := repo.UpdateTask(context.Background(), moved); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want exactly one row for the task", len(page.Rows))
	}
	first := page.Rows[0]
	if first.ID != latestEpisodeRowID(t, repo, first.TaskID, first.StepID) {
		t.Fatalf("row id = %q, want the newest transition's mw identity", first.ID)
	}

	// Leave and re-enter: a new episode has a different identity.
	for _, step := range []string{"step-other", "step-manual"} {
		moved, err = repo.GetTask(context.Background(), "t-episode")
		if err != nil {
			t.Fatalf("GetTask: %v", err)
		}
		moved.WorkflowStepID = step
		if err := repo.UpdateTask(context.Background(), moved); err != nil {
			t.Fatalf("UpdateTask to %s: %v", step, err)
		}
	}

	page = listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 1 {
		t.Fatalf("rows after re-entry = %d, want exactly one", len(page.Rows))
	}
	second := page.Rows[0]
	if second.ID == first.ID {
		t.Fatalf("row id after re-entry = %q, want a different episode identity than %q", second.ID, first.ID)
	}
	if second.ID != latestEpisodeRowID(t, repo, second.TaskID, second.StepID) {
		t.Fatalf("row id after re-entry = %q, want the newest transition's mw identity", second.ID)
	}
}

// latestEpisodeRowID derives the expected row identity from the newest
// task_step_transitions row for (task, step), or the zero fallback when none
// exists.
func latestEpisodeRowID(t *testing.T, repo *Repository, taskID, stepID string) string {
	t.Helper()
	var transitionID int64
	err := repo.db.QueryRow(repo.db.Rebind(`
		SELECT id FROM task_step_transitions
		WHERE task_id = ? AND to_workflow_step_id = ?
		ORDER BY id DESC LIMIT 1
	`), taskID, stepID).Scan(&transitionID)
	if err != nil {
		t.Fatalf("read latest transition for (%s, %s): %v", taskID, stepID, err)
	}
	return models.ManualStepWaitRowID(taskID, stepID, transitionID)
}

func TestListManualStepWaits_NoTransitionRowFallsBackToZeroIdentity(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	// Insert the task with a raw INSERT so no genesis transition row exists.
	if _, err := repo.db.Exec(repo.db.Rebind(`
		INSERT INTO tasks (id, workspace_id, workflow_id, workflow_step_id, title, state, created_at, updated_at)
		VALUES ('t-noledger', 'ws-wait', 'wf-wait', 'step-manual', 'No ledger', 'TODO', ?, ?)
	`), time.Now().UTC(), time.Now().UTC()); err != nil {
		t.Fatalf("insert task: %v", err)
	}

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(page.Rows))
	}
	if want := models.ManualStepWaitRowID("t-noledger", "step-manual", 0); page.Rows[0].ID != want {
		t.Fatalf("row id = %q, want fallback %q", page.Rows[0].ID, want)
	}
}

func TestListManualStepWaits_ReferenceTimeOrdering(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	// CreateTask stamps its own now (the model's CreatedAt is overwritten), so
	// the reference times are controlled by rewriting each genesis transition's
	// occurred_at directly.
	for _, taskID := range []string{"t-newer", "t-older"} {
		if err := repo.CreateTask(context.Background(), &models.Task{
			ID: taskID, WorkspaceID: "ws-wait", WorkflowID: "wf-wait", WorkflowStepID: "step-manual", Title: taskID,
		}); err != nil {
			t.Fatalf("CreateTask %s: %v", taskID, err)
		}
	}
	older := time.Now().UTC().Add(-2 * time.Hour)
	newer := time.Now().UTC().Add(-1 * time.Hour)
	for _, tc := range []struct {
		taskID string
		at     time.Time
	}{
		{"t-newer", newer},
		{"t-older", older},
	} {
		if _, err := repo.db.Exec(repo.db.Rebind(`
			UPDATE task_step_transitions SET occurred_at = ?
			WHERE task_id = ? AND to_workflow_step_id = 'step-manual'
		`), tc.at, tc.taskID); err != nil {
			t.Fatalf("set occurred_at for %s: %v", tc.taskID, err)
		}
	}

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(page.Rows))
	}
	if page.Rows[0].TaskID != "t-older" || page.Rows[1].TaskID != "t-newer" {
		t.Fatalf("order = [%s, %s], want reference_time ascending", page.Rows[0].TaskID, page.Rows[1].TaskID)
	}
	if !page.Rows[0].ReferenceTime.Before(page.Rows[1].ReferenceTime) {
		t.Fatalf("reference times = %v, %v, want ascending", page.Rows[0].ReferenceTime, page.Rows[1].ReferenceTime)
	}
}

func TestListManualStepWaits_TruncationAndOverscan(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	// Four manual tasks (limit 2 -> overscan 9) plus one auto-start task that
	// the Go filter must remove without counting toward the page.
	seedWaitingStep(t, repo, waitFixture{stepID: "step-auto", stepName: "Auto", eventsJSON: onEnterEvents("auto_start_agent")})
	at := time.Now().UTC()
	for i := 0; i < 4; i++ {
		if err := repo.CreateTask(context.Background(), &models.Task{
			ID: fmt.Sprintf("t-manual-%d", i), WorkspaceID: "ws-wait", WorkflowID: "wf-wait", WorkflowStepID: "step-manual",
			Title: fmt.Sprintf("manual %d", i), CreatedAt: at, UpdatedAt: at,
		}); err != nil {
			t.Fatalf("CreateTask t-manual-%d: %v", i, err)
		}
	}
	if err := repo.CreateTask(context.Background(), &models.Task{
		ID: "t-auto", WorkspaceID: "ws-wait", WorkflowID: "wf-wait", WorkflowStepID: "step-auto",
		Title: "auto", CreatedAt: at, UpdatedAt: at,
	}); err != nil {
		t.Fatalf("CreateTask t-auto: %v", err)
	}

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 2})
	if len(page.Rows) != 2 {
		t.Fatalf("rows = %d, want the limit 2", len(page.Rows))
	}
	if !page.HasMore {
		t.Error("HasMore = false, want true: candidates remain beyond the limit")
	}
	for _, row := range page.Rows {
		if row.TaskID == "t-auto" {
			t.Fatalf("row %q leaked into the page: Go filtering must run before truncation", row.TaskID)
		}
	}
}

func TestListManualStepWaits_SidecarHidesAndHiddenEnumeration(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	seedWaitingStep(t, repo, waitFixture{stepID: "step-snoozed", stepName: "Snoozed"})
	for _, tc := range []struct {
		taskID string
		stepID string
	}{
		{"t-visible", "step-manual"},
		{"t-dismissed", "step-manual"},
		{"t-snoozed", "step-snoozed"},
	} {
		if err := repo.CreateTask(context.Background(), &models.Task{
			ID: tc.taskID, WorkspaceID: "ws-wait", WorkflowID: "wf-wait", WorkflowStepID: tc.stepID, Title: tc.taskID,
		}); err != nil {
			t.Fatalf("CreateTask %s: %v", tc.taskID, err)
		}
	}
	now := time.Now().UTC()
	snoozeUntil := now.Add(4 * time.Hour)
	dismissedID := latestEpisodeRowID(t, repo, "t-dismissed", "step-manual")
	snoozedID := latestEpisodeRowID(t, repo, "t-snoozed", "step-snoozed")
	if err := repo.UpsertClarificationInboxSidecar(context.Background(), "user-1", dismissedID, models.ClarificationSidecarDismissed, nil, now); err != nil {
		t.Fatalf("upsert dismissed sidecar: %v", err)
	}
	if err := repo.UpsertClarificationInboxSidecar(context.Background(), "user-1", snoozedID, models.ClarificationSidecarSnoozed, &snoozeUntil, now); err != nil {
		t.Fatalf("upsert snoozed sidecar: %v", err)
	}

	main := listWaits(t, repo, models.ManualStepWaitOptions{
		WorkspaceID: "ws-wait", Limit: 50,
		Sidecar: &models.ClarificationSidecarFilter{UserID: "user-1", Now: now},
	})
	if ids := waitRowTaskIDs(main); len(ids) != 1 || ids[0] != "t-visible" {
		t.Fatalf("main rows = %v, want exactly [t-visible]", ids)
	}
	if main.HiddenCount != 2 {
		t.Errorf("HiddenCount = %d, want 2", main.HiddenCount)
	}
	if main.NextSnoozeExpiry == nil || !main.NextSnoozeExpiry.Equal(snoozeUntil) {
		t.Errorf("NextSnoozeExpiry = %v, want the snoozed row's expiry %v", main.NextSnoozeExpiry, snoozeUntil)
	}

	hidden := listWaits(t, repo, models.ManualStepWaitOptions{
		WorkspaceID: "ws-wait", Limit: 50, OnlyHidden: true,
		Sidecar: &models.ClarificationSidecarFilter{UserID: "user-1", Only: true, Now: now},
	})
	if len(hidden.Rows) != 2 {
		t.Fatalf("hidden rows = %d, want 2", len(hidden.Rows))
	}
	byTask := map[string]models.ManualStepWaitRow{}
	for _, row := range hidden.Rows {
		byTask[row.TaskID] = row
	}
	if row, ok := byTask["t-dismissed"]; !ok || row.SidecarState != models.ClarificationSidecarDismissed {
		t.Errorf("dismissed row = %+v, ok=%v", row, ok)
	}
	if row, ok := byTask["t-snoozed"]; !ok || row.SidecarState != models.ClarificationSidecarSnoozed || row.SnoozeUntil == nil {
		t.Errorf("snoozed row = %+v, ok=%v", row, ok)
	}
}

// TestListManualStepWaits_MissingQueueTableOmitsPredicate proves the queued
// predicate degrades (rather than erroring) when the queue-owned table is
// absent, matching the attachment read's established probe pattern.
func TestListManualStepWaits_MissingQueueTableOmitsPredicate(t *testing.T) {
	repo := newWaitingTestRepo(t)
	seedWaitingStep(t, repo, manualStepFixture("t1"))
	seedWaitingTask(t, repo, manualStepFixture("t-no-queue"))

	page := listWaits(t, repo, models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 50})
	if len(page.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 without a queued_messages table", len(page.Rows))
	}
}

func TestListManualStepWaits_LimitMustBePositive(t *testing.T) {
	repo := newWaitingTestRepo(t)
	if _, err := repo.ListManualStepWaits(context.Background(), models.ManualStepWaitOptions{WorkspaceID: "ws-wait", Limit: 0}); err == nil {
		t.Fatal("expected an error for limit 0")
	}
}
