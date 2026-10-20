package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/analytics/models"
	"github.com/kandev/kandev/internal/statsync"
)

// TestStatsSectionsMergeMirroredRows is the ingest-to-analytics parity
// oracle: a statsync payload (one name-matched repository, one remote-only,
// completed by flag / by archive) is pushed through the real ingest service
// and every /stats section must count local plus mirrored rows under the
// same inclusion rules.
func TestStatsSectionsMergeMirroredRows(t *testing.T) {
	dbConn := createTestDB(t)
	seedLocalStatsFixture(t, dbConn)
	pushMirrorStatsFixture(t, dbConn)

	repo, err := NewWithDB(dbConn, dbConn)
	if err != nil {
		t.Fatalf("NewWithDB failed: %v", err)
	}
	ctx := context.Background()

	t.Run("global", func(t *testing.T) {
		stats, err := repo.GetGlobalStats(ctx, "ws-local", nil)
		if err != nil {
			t.Fatalf("GetGlobalStats: %v", err)
		}
		if stats.TotalTasks != 3 {
			t.Errorf("TotalTasks = %d, want 3 (local + 2 mirrored)", stats.TotalTasks)
		}
		if stats.CompletedTasks != 2 {
			t.Errorf("CompletedTasks = %d, want 2 (remote-completed + archived; the last-step local task is not committed state)", stats.CompletedTasks)
		}
		if stats.InProgressTasks != 1 {
			t.Errorf("InProgressTasks = %d, want 1", stats.InProgressTasks)
		}
		if stats.TotalSessions != 3 {
			t.Errorf("TotalSessions = %d, want 3", stats.TotalSessions)
		}
		if stats.TotalTurns != 4 {
			t.Errorf("TotalTurns = %d, want 4", stats.TotalTurns)
		}
		if stats.TotalMessages != 6 {
			t.Errorf("TotalMessages = %d, want 6", stats.TotalMessages)
		}
		if stats.TotalUserMessages != 2 {
			t.Errorf("TotalUserMessages = %d, want 2 (one local, one mirrored)", stats.TotalUserMessages)
		}
		if stats.TotalToolCalls != 2 {
			t.Errorf("TotalToolCalls = %d, want 2", stats.TotalToolCalls)
		}
		if stats.TotalDurationMs < 29000 || stats.TotalDurationMs > 31000 {
			t.Errorf("TotalDurationMs = %d, want ~30000 (10s local + 20s mirrored turn)", stats.TotalDurationMs)
		}
	})

	t.Run("tasks", func(t *testing.T) {
		stats, err := repo.GetTaskStats(ctx, "ws-local", nil, 10)
		if err != nil {
			t.Fatalf("GetTaskStats: %v", err)
		}
		if len(stats) != 3 {
			t.Fatalf("len(GetTaskStats) = %d, want 3", len(stats))
		}
		byTask := map[string]*models.TaskStats{}
		for _, stat := range stats {
			byTask[stat.TaskID] = stat
		}
		merged := byTask["task-local-1"]
		if merged == nil || merged.SessionCount != 1 || merged.TurnCount != 2 || merged.MessageCount != 3 {
			t.Errorf("local task aggregate = %+v, want 1 session / 2 turns / 3 messages", merged)
		}
		mirrored := byTask["remote-1:t1"]
		if mirrored == nil || mirrored.SessionCount != 1 || mirrored.TurnCount != 1 || mirrored.MessageCount != 2 {
			t.Errorf("mirrored task aggregate = %+v, want 1 session / 1 turn / 2 messages", mirrored)
		}
	})

	t.Run("daily-activity", func(t *testing.T) {
		daily, err := repo.GetDailyActivity(ctx, "ws-local", 7)
		if err != nil {
			t.Fatalf("GetDailyActivity: %v", err)
		}
		last := daily[len(daily)-1]
		if last.TurnCount != 4 || last.MessageCount != 6 || last.TaskCount != 3 {
			t.Errorf("today's activity = turns %d / messages %d / tasks %d, want 4 / 6 / 3",
				last.TurnCount, last.MessageCount, last.TaskCount)
		}
	})

	t.Run("completed-activity", func(t *testing.T) {
		activity, err := repo.GetCompletedTaskActivity(ctx, "ws-local", 7)
		if err != nil {
			t.Fatalf("GetCompletedTaskActivity: %v", err)
		}
		if got := activity[len(activity)-1].CompletedTasks; got != 2 {
			t.Errorf("today's completed tasks = %d, want 2 (both mirrored completions)", got)
		}
	})

	t.Run("model-usage", func(t *testing.T) {
		usage, err := repo.GetModelUsage(ctx, "ws-local", 10, nil)
		if err != nil {
			t.Fatalf("GetModelUsage: %v", err)
		}
		byModel := map[string]*models.ModelUsage{}
		for _, row := range usage {
			byModel[row.Model] = row
		}
		if len(byModel) != 2 {
			t.Fatalf("models = %v, want local-model and remote-model", byModel)
		}
		if byModel["remote-model"].SessionCount != 2 {
			t.Errorf("remote-model sessions = %d, want 2", byModel["remote-model"].SessionCount)
		}
		if byModel["local-model"].SessionCount != 1 {
			t.Errorf("local-model sessions = %d, want 1", byModel["local-model"].SessionCount)
		}
	})

	t.Run("repositories", func(t *testing.T) {
		stats, err := repo.GetRepositoryStats(ctx, "ws-local", nil)
		if err != nil {
			t.Fatalf("GetRepositoryStats: %v", err)
		}
		byName := map[string]*models.RepositoryStats{}
		for _, row := range stats {
			byName[row.RepositoryName] = row
		}
		if len(byName) != 2 {
			t.Fatalf("repository rows = %d (%v), want 2 (merged local + remote-only)", len(byName), byName)
		}
		merged := byName["shared-repo"]
		if merged == nil {
			t.Fatal("name-matched repository missing from merged view")
		}
		if merged.RepositoryID != "repo-local" {
			t.Errorf("merged repository id = %q, want the local repo id", merged.RepositoryID)
		}
		if merged.TotalTasks != 2 || merged.CompletedTasks != 1 || merged.InProgressTasks != 1 {
			t.Errorf("merged tasks = %d/%d/%d, want 2/1/1 (total/completed/in-progress)",
				merged.TotalTasks, merged.CompletedTasks, merged.InProgressTasks)
		}
		// Turns: the local session contributes two (one still open) and the
		// mirrored one one.
		if merged.SessionCount != 2 || merged.TurnCount != 3 || merged.MessageCount != 5 {
			t.Errorf("merged sessions/turns/messages = %d/%d/%d, want 2/3/5",
				merged.SessionCount, merged.TurnCount, merged.MessageCount)
		}
		if merged.ToolCallCount != 2 || merged.UserMessageCount != 2 {
			t.Errorf("merged tool/user = %d/%d, want 2/2", merged.ToolCallCount, merged.UserMessageCount)
		}
		if merged.TotalCommits != 2 || merged.TotalFilesChanged != 3 || merged.TotalInsertions != 17 || merged.TotalDeletions != 7 {
			t.Errorf("merged git = commits %d files %d +%d/-%d, want 2/3/+17/-7",
				merged.TotalCommits, merged.TotalFilesChanged, merged.TotalInsertions, merged.TotalDeletions)
		}
		if merged.TotalDurationMs < 29000 || merged.TotalDurationMs > 31000 {
			t.Errorf("merged duration = %d, want ~30000", merged.TotalDurationMs)
		}
		remoteOnly := byName["remote-only"]
		if remoteOnly == nil {
			t.Fatal("remote-only repository missing from merged view")
		}
		if remoteOnly.RepositoryID != "remote-1:repoB" {
			t.Errorf("remote-only repository id = %q, want the mirror uid", remoteOnly.RepositoryID)
		}
		if remoteOnly.TotalTasks != 1 || remoteOnly.CompletedTasks != 1 || remoteOnly.InProgressTasks != 0 {
			t.Errorf("remote-only tasks = %d/%d/%d, want 1/1/0",
				remoteOnly.TotalTasks, remoteOnly.CompletedTasks, remoteOnly.InProgressTasks)
		}
		if remoteOnly.SessionCount != 1 || remoteOnly.TurnCount != 1 || remoteOnly.MessageCount != 1 {
			t.Errorf("remote-only sessions/turns/messages = %d/%d/%d, want 1/1/1",
				remoteOnly.SessionCount, remoteOnly.TurnCount, remoteOnly.MessageCount)
		}
		if remoteOnly.TotalCommits != 0 {
			t.Errorf("remote-only commits = %d, want 0", remoteOnly.TotalCommits)
		}
	})

	t.Run("git", func(t *testing.T) {
		stats, err := repo.GetGitStats(ctx, "ws-local", nil)
		if err != nil {
			t.Fatalf("GetGitStats: %v", err)
		}
		if stats.TotalCommits != 2 || stats.TotalFilesChanged != 3 || stats.TotalInsertions != 17 || stats.TotalDeletions != 7 {
			t.Errorf("merged git stats = %d commits / %d files / +%d/-%d, want 2/3/+17/-7",
				stats.TotalCommits, stats.TotalFilesChanged, stats.TotalInsertions, stats.TotalDeletions)
		}
	})
}

func seedLocalStatsFixture(t *testing.T, dbConn *sqlx.DB) {
	t.Helper()
	now := time.Now().UTC()
	hourAgo := now.Add(-time.Hour)
	twoHoursAgo := now.Add(-2 * time.Hour)
	statsExec(t, dbConn, `INSERT INTO workspaces (id, name, created_at, updated_at) VALUES ('ws-local', 'Local', ?, ?)`, twoHoursAgo, twoHoursAgo)
	statsExec(t, dbConn, `INSERT INTO workflow_steps (id, workflow_id, name, position, created_at, updated_at)
		VALUES ('wf1-step0', 'wf-1', 'Start', 0, ?, ?), ('wf1-step1', 'wf-1', 'Done', 1, ?, ?)`,
		twoHoursAgo, twoHoursAgo, twoHoursAgo, twoHoursAgo)
	statsExec(t, dbConn, `INSERT INTO repositories (id, workspace_id, name, created_at, updated_at)
		VALUES ('repo-local', 'ws-local', 'shared-repo', ?, ?)`, twoHoursAgo, twoHoursAgo)
	// The local task sits on the workflow's last step: completed by the
	// last-step arm of the view's completed rule, not by archiving.
	statsExec(t, dbConn, `INSERT INTO tasks (id, workspace_id, board_id, workflow_id, workflow_step_id, title, state, is_ephemeral, origin, created_at, updated_at)
		VALUES ('task-local-1', 'ws-local', '', 'wf-1', 'wf1-step1', 'Local task', 'IN_PROGRESS', 0, 'manual', ?, ?)`,
		twoHoursAgo, hourAgo)
	statsExec(t, dbConn, `INSERT INTO task_repositories (id, task_id, repository_id, created_at, updated_at)
		VALUES ('tr-local-1', 'task-local-1', 'repo-local', ?, ?)`, twoHoursAgo, twoHoursAgo)
	statsExec(t, dbConn, `INSERT INTO task_sessions (id, task_id, agent_profile_id, agent_profile_snapshot, repository_id, state, started_at, completed_at, updated_at)
		VALUES ('sess-local-1', 'task-local-1', '', '{"model":"local-model","model_name":"Local","llm":"claude"}', 'repo-local', 'COMPLETED', ?, ?, ?)`,
		twoHoursAgo, hourAgo, hourAgo)
	statsExec(t, dbConn, `INSERT INTO task_session_turns (id, task_session_id, task_id, started_at, completed_at, created_at, updated_at)
		VALUES ('turn-local-1', 'sess-local-1', 'task-local-1', ?, ?, ?, ?),
		       ('turn-local-2', 'sess-local-1', 'task-local-1', ?, NULL, ?, ?)`,
		twoHoursAgo, twoHoursAgo.Add(10*time.Second), twoHoursAgo, hourAgo,
		hourAgo, hourAgo, hourAgo)
	statsExec(t, dbConn, `INSERT INTO task_session_messages (id, task_session_id, turn_id, author_type, type, content, created_at)
		VALUES ('msg-local-1', 'sess-local-1', 'turn-local-1', 'user', 'message', 'x', ?),
		       ('msg-local-2', 'sess-local-1', 'turn-local-1', 'assistant', 'tool_use', 'x', ?),
		       ('msg-local-3', 'sess-local-1', 'turn-local-2', 'assistant', 'message', 'x', ?)`,
		twoHoursAgo, twoHoursAgo, hourAgo)
	statsExec(t, dbConn, `INSERT INTO task_session_commits (id, session_id, commit_sha, committed_at, files_changed, insertions, deletions, created_at)
		VALUES ('commit-local-1', 'sess-local-1', 'sha-local', ?, 2, 10, 5, ?)`, hourAgo, hourAgo)
}

func pushMirrorStatsFixture(t *testing.T, dbConn *sqlx.DB) {
	t.Helper()
	mirrorRepo, err := statsync.NewWithDB(dbConn, dbConn)
	if err != nil {
		t.Fatalf("statsync NewWithDB: %v", err)
	}
	if err := statsync.EnsureViews(dbConn); err != nil {
		t.Fatalf("statsync EnsureViews: %v", err)
	}
	svc := statsync.NewService(mirrorRepo, dbConn, "parity-secret",
		map[string]string{"remote-1": "ws-local"}, nil)

	now := time.Now().UTC()
	completed := true
	rfc := func(offset time.Duration) string {
		return now.Add(offset).UTC().Format(time.RFC3339Nano)
	}
	snapshot, err := json.Marshal(map[string]string{"model": "remote-model", "model_name": "Remote", "llm": "claude"})
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	env := &statsync.Envelope{
		SchemaVersion:  statsync.SchemaVersion,
		SourceInstance: "remote-1",
		Mode:           statsync.ModeDelta,
		Repositories: []statsync.RepositoryRow{
			{UID: "remote-1:repoA", Name: "shared-repo"},
			{UID: "remote-1:repoB", Name: "remote-only"},
		},
		Tasks: []statsync.TaskRow{
			{
				UID: "remote-1:t1", OrigWorkspaceID: "ws-remote", Title: "Remote done",
				State: "DONE", Completed: &completed,
				CreatedAt: rfc(-2 * time.Hour), UpdatedAt: rfc(-time.Hour),
			},
			{
				UID: "remote-1:t2", OrigWorkspaceID: "ws-remote", Title: "Remote archived",
				State: "DONE", ArchivedAt: strPtr(rfc(-30 * time.Minute)),
				CreatedAt: rfc(-2 * time.Hour), UpdatedAt: rfc(-30 * time.Minute),
			},
		},
		TaskRepositories: []statsync.TaskRepositoryRow{
			{UID: "remote-1:tr1", TaskUID: "remote-1:t1", RepositoryID: "remote-1:repoA"},
			{UID: "remote-1:tr2", TaskUID: "remote-1:t2", RepositoryID: "remote-1:repoB"},
		},
		Sessions: []statsync.SessionRow{
			{
				UID: "remote-1:s1", TaskUID: "remote-1:t1",
				StartedAt: rfc(-90 * time.Minute), CompletedAt: strPtr(rfc(-80 * time.Minute)),
				RepositoryID: "remote-1:repoA", AgentProfileSnapshot: snapshot,
			},
			{
				UID: "remote-1:s2", TaskUID: "remote-1:t2",
				StartedAt: rfc(-50 * time.Minute), RepositoryID: "remote-1:repoB",
				AgentProfileSnapshot: snapshot,
			},
		},
		Turns: []statsync.TurnRow{
			{UID: "remote-1:turn1", SessionUID: "remote-1:s1", StartedAt: rfc(-90 * time.Minute), CompletedAt: strPtr(rfc(-90*time.Minute + 20*time.Second))},
			{UID: "remote-1:turn2", SessionUID: "remote-1:s2", StartedAt: rfc(-50 * time.Minute)},
		},
		Messages: []statsync.MessageRow{
			{UID: "remote-1:m1", SessionUID: "remote-1:s1", TurnUID: "remote-1:turn1", AuthorType: "user", Type: "message", CreatedAt: rfc(-90 * time.Minute)},
			{UID: "remote-1:m2", SessionUID: "remote-1:s1", TurnUID: "remote-1:turn1", AuthorType: "assistant", Type: "tool_use", CreatedAt: rfc(-89 * time.Minute)},
			{UID: "remote-1:m3", SessionUID: "remote-1:s2", TurnUID: "remote-1:turn2", AuthorType: "assistant", Type: "message", CreatedAt: rfc(-49 * time.Minute)},
		},
		Commits: []statsync.CommitRow{
			{UID: "remote-1:c1", SessionUID: "remote-1:s1", CommitSHA: "sha-remote", CommittedAt: rfc(-70 * time.Minute), FilesChanged: int64Ptr(1), Insertions: int64Ptr(7), Deletions: int64Ptr(2)},
		},
	}
	if _, err := svc.Ingest(context.Background(), env); err != nil {
		t.Fatalf("statsync ingest: %v", err)
	}
}

func strPtr(value string) *string { return &value }

func int64Ptr(value int64) *int64 { return &value }
