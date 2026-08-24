package orchestrator

import (
	"context"
	"testing"

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
