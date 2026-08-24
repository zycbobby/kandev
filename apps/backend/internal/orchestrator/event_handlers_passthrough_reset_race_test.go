package orchestrator

import (
	"context"
	"testing"

	"github.com/kandev/kandev/internal/orchestrator/watcher"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
)

// Regression: a passthrough (CLI) workflow step that combines reset_agent_context
// and auto_start_agent used to write its auto-start prompt straight into the
// freshly-restarted CLI's PTY stdin before the CLI finished booting, losing the
// prompt and stalling the step. It must queue the prompt instead so the fresh
// CLI's first idle agent.ready delivers it via deliverPassthroughPrompt.
func TestPassthroughResetAutoStart_QueuesPromptInsteadOfInlineWrite(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")

	step := &wfmodels.WorkflowStep{
		ID: "step2", WorkflowID: "wf1", Name: "Apply", Position: 1,
		Prompt: "apply the change",
		Events: wfmodels.StepEvents{
			OnEnter: []wfmodels.OnEnterAction{
				{Type: wfmodels.OnEnterResetAgentContext},
				{Type: wfmodels.OnEnterAutoStartAgent},
			},
		},
	}
	stepGetter := newMockStepGetter()
	stepGetter.steps[step.ID] = step

	agentMgr := &mockAgentManager{isPassthrough: true}
	svc := createTestServiceWithAgent(repo, stepGetter, newMockTaskRepo(), agentMgr)

	session, err := repo.GetTaskSession(ctx, "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	svc.processOnEnter(ctx, "t1", session, step, "test task")

	if got := len(agentMgr.passthroughStdinCalls); got != 0 {
		t.Fatalf("expected prompt to be queued, not written inline; got %d stdin writes", got)
	}
	if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 1 {
		t.Fatalf("expected 1 queued prompt after reset, got %d", got)
	}
}

// The non-reset passthrough auto-start path must stay inline: the PTY is still
// idle from the previous turn, so the prompt is written directly.
func TestPassthroughAutoStartNoReset_WritesInline(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")

	step := &wfmodels.WorkflowStep{
		ID: "step2", WorkflowID: "wf1", Name: "Propose", Position: 1,
		Prompt: "propose a plan",
		Events: wfmodels.StepEvents{
			OnEnter: []wfmodels.OnEnterAction{
				{Type: wfmodels.OnEnterAutoStartAgent},
			},
		},
	}
	stepGetter := newMockStepGetter()
	stepGetter.steps[step.ID] = step

	agentMgr := &mockAgentManager{isPassthrough: true}
	svc := createTestServiceWithAgent(repo, stepGetter, newMockTaskRepo(), agentMgr)

	session, err := repo.GetTaskSession(ctx, "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	svc.processOnEnter(ctx, "t1", session, step, "test task")

	if got := len(agentMgr.passthroughStdinCalls); got == 0 {
		t.Fatal("expected prompt to be written inline for non-reset passthrough auto-start")
	}
	if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 0 {
		t.Fatalf("expected no queued prompt for non-reset passthrough auto-start, got %d", got)
	}
}

// Regression: task-01 queues the prompt, but a WAITING_FOR_INPUT passthrough
// session (an idle task manually moved into the step) never drained it because
// handleAgentReady drops agent.ready when the session is not RUNNING/STARTING.
// The freshly-restarted CLI's first idle is a boot signal, not a turn end, so
// the queued prompt must still be delivered exactly once.
func TestPassthroughResetAutoStart_DeliversQueuedPromptForWaitingSession(t *testing.T) {
	ctx := context.Background()
	repo := setupTestRepo(t)
	seedSession(t, repo, "t1", "s1", "step1")

	// An idle task manually moved into the step has a WAITING_FOR_INPUT session.
	session, err := repo.GetTaskSession(ctx, "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	session.State = models.TaskSessionStateWaitingForInput
	if err := repo.UpdateTaskSession(ctx, session); err != nil {
		t.Fatalf("update session state: %v", err)
	}

	step := &wfmodels.WorkflowStep{
		ID: "step2", WorkflowID: "wf1", Name: "Apply", Position: 1,
		Prompt: "apply the change",
		Events: wfmodels.StepEvents{
			OnEnter: []wfmodels.OnEnterAction{
				{Type: wfmodels.OnEnterResetAgentContext},
				{Type: wfmodels.OnEnterAutoStartAgent},
			},
		},
	}
	stepGetter := newMockStepGetter()
	stepGetter.steps[step.ID] = step

	agentMgr := &mockAgentManager{isPassthrough: true}
	svc := createTestServiceWithAgent(repo, stepGetter, newMockTaskRepo(), agentMgr)

	session, err = repo.GetTaskSession(ctx, "s1")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	svc.processOnEnter(ctx, "t1", session, step, "test task")

	// The prompt is queued, not written inline (task-01 behavior).
	if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 1 {
		t.Fatalf("expected 1 queued prompt after reset, got %d", got)
	}
	if got := len(agentMgr.passthroughStdinCalls); got != 0 {
		t.Fatalf("expected no inline stdin write, got %d", got)
	}

	// The freshly-restarted CLI reaches its first idle -> agent.ready.
	svc.handleAgentReady(ctx, watcher.AgentEventData{TaskID: "t1", SessionID: "s1"})

	if got := len(agentMgr.passthroughStdinCalls); got != 1 {
		t.Fatalf("expected queued prompt delivered to passthrough stdin once, got %d writes", got)
	}
	if got := svc.messageQueue.GetStatus(ctx, "s1").Count; got != 0 {
		t.Fatalf("expected queue drained after delivery, got %d", got)
	}
}
