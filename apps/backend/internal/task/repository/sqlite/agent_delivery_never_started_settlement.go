package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// SettleNeverStartedAgentDeliverySubmission settles one submission whose prompt
// generation produced zero turn events with the historical owner proven
// terminated. The failed state, its idempotency effect, and the recovery blocks
// bound to that exact submission commit together or not at all.
func (r *Repository) SettleNeverStartedAgentDeliverySubmission(
	ctx context.Context,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) (*models.AgentDeliveryNeverStartedSettlementResult, error) {
	if err := validateNeverStartedSettlementRequest(request); err != nil {
		return nil, err
	}
	settledAt := request.SettledAt
	if settledAt.IsZero() {
		settledAt = r.nowUTC()
	}
	tx, err := r.beginNeverStartedSettlementTx(ctx, request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	submission, err := r.loadNeverStartedSubmissionTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if submission.State == models.DeliverySubmissionFailed {
		return r.replayNeverStartedSettlementTx(ctx, tx, submission, request, settledAt)
	}
	resolved, err := r.applyNeverStartedSettlementTx(ctx, tx, submission, request, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return neverStartedSettlementResult(submission, resolved, settledAt), nil
}

// beginNeverStartedSettlementTx opens the settlement transaction with the
// session-turn and agent-delivery write locks held.
func (r *Repository) beginNeverStartedSettlementTx(
	ctx context.Context,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) (*sqlx.Tx, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), request.SessionID); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := lockAgentDeliveryWrites(ctx, tx, r.db.DriverName(), []string{request.SessionID}, nil); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func neverStartedSettlementResult(
	submission *models.AgentDeliverySubmission,
	resolved []string,
	settledAt time.Time,
) *models.AgentDeliveryNeverStartedSettlementResult {
	submission.State = models.DeliverySubmissionFailed
	submission.Outcome = string(models.DeliverySubmissionFailed)
	submission.UpdatedAt = settledAt
	return &models.AgentDeliveryNeverStartedSettlementResult{
		Settled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}
}

func validateNeverStartedSettlementRequest(request *models.AgentDeliveryNeverStartedSettlementRequest) error {
	if request == nil || request.SessionID == "" || request.IncarnationID == "" ||
		request.HarnessGeneration <= 0 || request.SubmissionID == "" ||
		request.ExpectedState == "" || request.ExpectedOutcome == "" {
		return fmt.Errorf("never-started settlement identity, expected state, and expected outcome are required")
	}
	return nil
}

func (r *Repository) loadNeverStartedSubmissionTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) (*models.AgentDeliverySubmission, error) {
	var submission models.AgentDeliverySubmission
	var fullySettledAt sql.NullTime
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT id, session_id, incarnation_id, harness_generation, state, outcome,
		       terminal_sequence, fully_settled_at
		FROM agent_delivery_submissions WHERE id = ?`), request.SubmissionID).Scan(
		&submission.ID, &submission.SessionID, &submission.IncarnationID,
		&submission.HarnessGeneration, &submission.State, &submission.Outcome,
		&submission.TerminalSequence, &fullySettledAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, repoerrors.ErrAgentDeliveryNeverStartedSettlementStale
	case err != nil:
		return nil, err
	}
	if err := verifyNeverStartedSubmissionOwner(&submission, request); err != nil {
		return nil, err
	}
	if fullySettledAt.Valid {
		at := fullySettledAt.Time
		submission.FullySettledAt = &at
	}
	return &submission, nil
}

// verifyNeverStartedSubmissionOwner fails stale when the locked row belongs to
// another session, incarnation, or harness generation.
func verifyNeverStartedSubmissionOwner(
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) error {
	if submission.SessionID != request.SessionID || submission.IncarnationID != request.IncarnationID ||
		submission.HarnessGeneration != request.HarnessGeneration {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementStale
	}
	return nil
}

// neverStartedAcceptedSourceState is the closed evidence set a never-started
// settlement may consume: a still-live dispatch label, or the uncertain outcome
// the prompt error path records after the forced stop.
func neverStartedAcceptedSourceState(state models.DeliverySubmissionState, outcome string) bool {
	return (state == models.DeliverySubmissionDispatching && outcome == "dispatching") ||
		(state == models.DeliverySubmissionInterruptedUnknown && outcome == "prompt_dispatch_failed")
}

func validateNeverStartedSubmissionState(
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) error {
	if submission.State != request.ExpectedState || submission.Outcome != request.ExpectedOutcome {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	if !neverStartedAcceptedSourceState(submission.State, submission.Outcome) {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	if submission.TerminalSequence != 0 || submission.FullySettledAt != nil {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	return nil
}

// applyNeverStartedSettlementTx performs the writes of a fresh settlement.
func (r *Repository) applyNeverStartedSettlementTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	settledAt time.Time,
) ([]string, error) {
	if err := validateNeverStartedSubmissionState(submission, request); err != nil {
		return nil, err
	}
	if err := r.markNeverStartedSubmissionFailedTx(ctx, tx, submission, request, settledAt); err != nil {
		return nil, err
	}
	if err := r.insertNeverStartedSettlementEffectTx(ctx, tx, request, settledAt); err != nil {
		return nil, err
	}
	return r.resolveNeverStartedDeliveryBlocksTx(ctx, tx, request, settledAt)
}

func (r *Repository) markNeverStartedSubmissionFailedTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	settledAt time.Time,
) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE agent_delivery_submissions SET state = ?, outcome = ?, updated_at = ?
		WHERE id = ? AND session_id = ? AND incarnation_id = ? AND harness_generation = ?
		  AND state = ? AND outcome = ? AND terminal_sequence = 0 AND fully_settled_at IS NULL`),
		models.DeliverySubmissionFailed, string(models.DeliverySubmissionFailed), settledAt,
		request.SubmissionID, request.SessionID, request.IncarnationID, request.HarnessGeneration,
		submission.State, submission.Outcome)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	return nil
}

func (r *Repository) insertNeverStartedSettlementEffectTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	settledAt time.Time,
) error {
	completedAt := settledAt
	inserted, err := insertDeliveryEffectTx(ctx, tx, r.db.Rebind, &models.AgentDeliveryEffect{
		EffectKey:         models.DeliveryNeverStartedEffectKey(request.SubmissionID),
		EffectType:        models.DeliveryNeverStartedEffectType,
		State:             models.DeliveryEffectCompleted,
		SessionID:         request.SessionID,
		IncarnationID:     request.IncarnationID,
		HarnessGeneration: request.HarnessGeneration,
		SubmissionID:      request.SubmissionID,
		Outcome:           string(models.DeliverySubmissionFailed),
		CreatedAt:         settledAt,
		CompletedAt:       &completedAt,
	})
	if err != nil {
		return err
	}
	if !inserted {
		return repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	return nil
}

func (r *Repository) replayNeverStartedSettlementTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	settledAt time.Time,
) (*models.AgentDeliveryNeverStartedSettlementResult, error) {
	present, err := r.neverStartedSettlementEffectPresentTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	resolved, err := r.resolveNeverStartedDeliveryBlocksTx(ctx, tx, request, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.AgentDeliveryNeverStartedSettlementResult{
		Settled: true, AlreadySettled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}, nil
}

func (r *Repository) neverStartedSettlementEffectPresentTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) (bool, error) {
	var effect models.AgentDeliveryEffect
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT stream_id, sequence, effect_type, state, session_id, incarnation_id,
		       harness_generation, submission_id, outcome
		FROM agent_delivery_effects WHERE effect_key = ?`),
		models.DeliveryNeverStartedEffectKey(request.SubmissionID)).Scan(
		&effect.StreamID, &effect.Sequence, &effect.EffectType, &effect.State,
		&effect.SessionID, &effect.IncarnationID, &effect.HarnessGeneration,
		&effect.SubmissionID, &effect.Outcome)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, err
	}
	if !neverStartedEffectMatchesRequest(&effect, request) {
		return false, repoerrors.ErrAgentDeliveryNeverStartedSettlementConflict
	}
	return true, nil
}

// neverStartedEffectMatchesRequest verifies a replayed settlement effect binds
// the exact submission identity with no stream, sequence, or terminal claim.
func neverStartedEffectMatchesRequest(
	effect *models.AgentDeliveryEffect,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) bool {
	if effect.EffectType != models.DeliveryNeverStartedEffectType || effect.StreamID != "" ||
		effect.Sequence != 0 || effect.State != models.DeliveryEffectCompleted {
		return false
	}
	return effect.SessionID == request.SessionID && effect.IncarnationID == request.IncarnationID &&
		effect.HarnessGeneration == request.HarnessGeneration &&
		effect.SubmissionID == request.SubmissionID &&
		effect.Outcome == string(models.DeliverySubmissionFailed)
}

// resolveNeverStartedDeliveryBlocksTx resolves every open block bound to the
// exact submission regardless of the stream recorded on the block, and leaves
// blocks bound to other submissions or consumers untouched.
func (r *Repository) resolveNeverStartedDeliveryBlocksTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	settledAt time.Time,
) ([]string, error) {
	blockIDs, err := r.listNeverStartedOpenBlockIDsTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if len(blockIDs) == 0 {
		return nil, nil
	}
	if err := r.advanceRecoverySessionRevisionTx(ctx, tx, request.SessionID); err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(blockIDs))
	for _, blockID := range blockIDs {
		updated, err := resolveNeverStartedBlockTx(ctx, tx, r.db.Rebind, request, blockID, settledAt)
		if err != nil {
			return nil, err
		}
		if updated {
			resolved = append(resolved, blockID)
		}
	}
	return resolved, nil
}

// listNeverStartedOpenBlockIDsTx selects the open agent_delivery blocks bound
// to the exact submission, narrowed to one block when the request names it.
func (r *Repository) listNeverStartedOpenBlockIDsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
) ([]string, error) {
	query := `
		SELECT id FROM session_recovery_blocks
		WHERE session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?`
	args := []any{
		request.SessionID, request.IncarnationID, request.HarnessGeneration,
		request.SubmissionID,
	}
	if request.BlockID != "" {
		query += `
		  AND id = ?`
		args = append(args, request.BlockID)
	}
	query += `
		  AND state = ?
		 ORDER BY id`
	args = append(args, models.RecoveryBlockOpen)
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(query), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var blockIDs []string
	for rows.Next() {
		var blockID string
		if err := rows.Scan(&blockID); err != nil {
			return nil, err
		}
		blockIDs = append(blockIDs, blockID)
	}
	return blockIDs, rows.Err()
}

func resolveNeverStartedBlockTx(
	ctx context.Context,
	tx *sqlx.Tx,
	rebind func(string) string,
	request *models.AgentDeliveryNeverStartedSettlementRequest,
	blockID string,
	settledAt time.Time,
) (bool, error) {
	result, err := tx.ExecContext(ctx, rebind(`
		UPDATE session_recovery_blocks
		SET state = ?, delivery_sequence = 0, delivery_turn_id = '',
		    delivery_outcome = ?, authorized_action = ?, updated_at = ?, resolved_at = ?
		WHERE id = ? AND session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?
		  AND state = ?`),
		models.RecoveryBlockResolved, string(models.DeliverySubmissionFailed),
		models.DeliveryNeverStartedAuthorizedAction, settledAt, settledAt, blockID,
		request.SessionID, request.IncarnationID, request.HarnessGeneration,
		request.SubmissionID, models.RecoveryBlockOpen)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}
