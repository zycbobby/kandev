package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
)

// SettleAbandonedAgentDeliverySubmission settles one submission whose retained
// journal records were retired after the historical owner was proven dead. The
// failed state, its idempotency effect, and the exact stream-bound recovery
// block commit together or not at all.
func (r *Repository) SettleAbandonedAgentDeliverySubmission(
	ctx context.Context,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) (*models.AgentDeliveryAbandonedSettlementResult, error) {
	if err := validateAbandonedSettlementRequest(request); err != nil {
		return nil, err
	}
	settledAt := request.SettledAt
	if settledAt.IsZero() {
		settledAt = r.nowUTC()
	}
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockSessionTurnWrites(ctx, tx, r.db.DriverName(), request.SessionID); err != nil {
		return nil, err
	}
	if err := lockAgentDeliveryWrites(ctx, tx, r.db.DriverName(), []string{request.SessionID}, []string{request.DeliveryStreamID}); err != nil {
		return nil, err
	}
	submission, err := r.loadAbandonedSubmissionTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	openBlocks, err := r.loadAbandonedBlockBindingsTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if submission.State == models.DeliverySubmissionFailed {
		return r.replayAbandonedSettlementTx(ctx, tx, submission, request, openBlocks, settledAt)
	}
	if err := validateAbandonedSubmissionState(submission, request); err != nil {
		return nil, err
	}
	if err := r.markAbandonedSubmissionFailedTx(ctx, tx, submission, request, settledAt); err != nil {
		return nil, err
	}
	if err := r.insertAbandonedSettlementEffectTx(ctx, tx, request, settledAt); err != nil {
		return nil, err
	}
	resolved, err := r.resolveAbandonedDeliveryBlocksTx(ctx, tx, request, openBlocks, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	submission.State = models.DeliverySubmissionFailed
	submission.Outcome = string(models.DeliverySubmissionFailed)
	submission.UpdatedAt = settledAt
	return &models.AgentDeliveryAbandonedSettlementResult{
		Settled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}, nil
}

func validateAbandonedSettlementRequest(request *models.AgentDeliveryAbandonedSettlementRequest) error {
	if request == nil || request.SessionID == "" || request.IncarnationID == "" ||
		request.HarnessGeneration <= 0 || request.SubmissionID == "" ||
		request.DeliveryStreamID == "" || request.ExpectedPayloadHash == "" ||
		request.ExpectedState == "" {
		return fmt.Errorf("abandoned settlement identity, stream, hash, and expected state are required")
	}
	return nil
}

func (r *Repository) loadAbandonedSubmissionTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) (*models.AgentDeliverySubmission, error) {
	var submission models.AgentDeliverySubmission
	var fullySettledAt sql.NullTime
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT id, session_id, incarnation_id, harness_generation, state, outcome,
		       payload_hash, terminal_sequence, fully_settled_at
		FROM agent_delivery_submissions WHERE id = ?`), request.SubmissionID).Scan(
		&submission.ID, &submission.SessionID, &submission.IncarnationID,
		&submission.HarnessGeneration, &submission.State, &submission.Outcome,
		&submission.PayloadHash, &submission.TerminalSequence, &fullySettledAt)
	if err == sql.ErrNoRows {
		return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementStale
	}
	if err != nil {
		return nil, err
	}
	if submission.SessionID != request.SessionID || submission.IncarnationID != request.IncarnationID ||
		submission.HarnessGeneration != request.HarnessGeneration {
		return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementStale
	}
	if fullySettledAt.Valid {
		at := fullySettledAt.Time
		submission.FullySettledAt = &at
	}
	return &submission, nil
}

// loadAbandonedBlockBindingsTx requires every block named by the request to be
// bound to the exact submission and non-empty stream, and returns the open ones.
func (r *Repository) loadAbandonedBlockBindingsTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) ([]string, error) {
	query := `
		SELECT id, delivery_stream_id, state FROM session_recovery_blocks
		WHERE session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?`
	args := []any{
		request.SessionID, request.IncarnationID, request.HarnessGeneration, request.SubmissionID,
	}
	if request.BlockID != "" {
		query += ` AND id = ?`
		args = append(args, request.BlockID)
	}
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(query+` ORDER BY id`), args...)
	if err != nil {
		return nil, err
	}
	type blockBinding struct{ id, streamID, state string }
	var bindings []blockBinding
	for rows.Next() {
		var binding blockBinding
		if err := rows.Scan(&binding.id, &binding.streamID, &binding.state); err != nil {
			_ = rows.Close()
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(bindings) == 0 {
		return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	var open []string
	for _, binding := range bindings {
		if binding.streamID != request.DeliveryStreamID {
			return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
		}
		if binding.state == models.RecoveryBlockOpen {
			open = append(open, binding.id)
		}
	}
	return open, nil
}

func validateAbandonedSubmissionState(
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) error {
	if submission.State != request.ExpectedState ||
		abandonedTerminalSubmissionState(submission.State) ||
		submission.TerminalSequence != 0 || submission.FullySettledAt != nil ||
		submission.PayloadHash != request.ExpectedPayloadHash {
		return repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	return nil
}

func abandonedTerminalSubmissionState(state models.DeliverySubmissionState) bool {
	return state == models.DeliverySubmissionCompleted || state == models.DeliverySubmissionFailed ||
		state == models.DeliverySubmissionCancelled
}

func (r *Repository) markAbandonedSubmissionFailedTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryAbandonedSettlementRequest,
	settledAt time.Time,
) error {
	result, err := tx.ExecContext(ctx, r.db.Rebind(`
		UPDATE agent_delivery_submissions SET state = ?, outcome = ?, updated_at = ?
		WHERE id = ? AND session_id = ? AND incarnation_id = ? AND harness_generation = ?
		  AND state = ? AND outcome = ? AND payload_hash = ?
		  AND terminal_sequence = 0 AND fully_settled_at IS NULL`),
		models.DeliverySubmissionFailed, string(models.DeliverySubmissionFailed), settledAt,
		request.SubmissionID, request.SessionID, request.IncarnationID, request.HarnessGeneration,
		submission.State, submission.Outcome, request.ExpectedPayloadHash)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	return nil
}

func (r *Repository) insertAbandonedSettlementEffectTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryAbandonedSettlementRequest,
	settledAt time.Time,
) error {
	completedAt := settledAt
	inserted, err := insertDeliveryEffectTx(ctx, tx, r.db.Rebind, &models.AgentDeliveryEffect{
		EffectKey:         models.DeliveryAbandonedEffectKey(request.SubmissionID),
		EffectType:        models.DeliveryAbandonedEffectType,
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
		return repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	return nil
}

func (r *Repository) replayAbandonedSettlementTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryAbandonedSettlementRequest,
	openBlocks []string,
	settledAt time.Time,
) (*models.AgentDeliveryAbandonedSettlementResult, error) {
	present, err := r.abandonedSettlementEffectPresentTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	resolved, err := r.resolveAbandonedDeliveryBlocksTx(ctx, tx, request, openBlocks, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.AgentDeliveryAbandonedSettlementResult{
		Settled: true, AlreadySettled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}, nil
}

func (r *Repository) abandonedSettlementEffectPresentTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) (bool, error) {
	var effect models.AgentDeliveryEffect
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT stream_id, sequence, effect_type, state, session_id, incarnation_id,
		       harness_generation, submission_id, outcome
		FROM agent_delivery_effects WHERE effect_key = ?`),
		models.DeliveryAbandonedEffectKey(request.SubmissionID)).Scan(
		&effect.StreamID, &effect.Sequence, &effect.EffectType, &effect.State,
		&effect.SessionID, &effect.IncarnationID, &effect.HarnessGeneration,
		&effect.SubmissionID, &effect.Outcome)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !abandonedSettlementEffectMatchesRequest(&effect, request) {
		return false, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
	}
	return true, nil
}

// abandonedSettlementEffectMatchesRequest verifies a replayed settlement effect
// binds the exact submission identity with no stream or sequence claim.
func abandonedSettlementEffectMatchesRequest(
	effect *models.AgentDeliveryEffect,
	request *models.AgentDeliveryAbandonedSettlementRequest,
) bool {
	if effect.EffectType != models.DeliveryAbandonedEffectType || effect.StreamID != "" ||
		effect.Sequence != 0 || effect.State != models.DeliveryEffectCompleted {
		return false
	}
	return effect.SessionID == request.SessionID && effect.IncarnationID == request.IncarnationID &&
		effect.HarnessGeneration == request.HarnessGeneration && effect.SubmissionID == request.SubmissionID &&
		effect.Outcome == string(models.DeliverySubmissionFailed)
}

func (r *Repository) resolveAbandonedDeliveryBlocksTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryAbandonedSettlementRequest,
	openBlocks []string,
	settledAt time.Time,
) ([]string, error) {
	if len(openBlocks) == 0 {
		return nil, nil
	}
	if err := r.advanceRecoverySessionRevisionTx(ctx, tx, request.SessionID); err != nil {
		return nil, err
	}
	resolved := make([]string, 0, len(openBlocks))
	for _, blockID := range openBlocks {
		updated, err := resolveAbandonedBlockTx(ctx, tx, r.db.Rebind, request, blockID, settledAt)
		if err != nil {
			return nil, err
		}
		if !updated {
			return nil, repoerrors.ErrAgentDeliveryAbandonedSettlementConflict
		}
		resolved = append(resolved, blockID)
	}
	return resolved, nil
}

func resolveAbandonedBlockTx(
	ctx context.Context,
	tx *sqlx.Tx,
	rebind func(string) string,
	request *models.AgentDeliveryAbandonedSettlementRequest,
	blockID string,
	settledAt time.Time,
) (bool, error) {
	result, err := tx.ExecContext(ctx, rebind(`
		UPDATE session_recovery_blocks
		SET state = ?, delivery_stream_id = ?, delivery_sequence = 0, delivery_turn_id = '',
		    delivery_outcome = ?, authorized_action = ?, updated_at = ?, resolved_at = ?
		WHERE id = ? AND session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?
		  AND delivery_stream_id = ? AND state = ?`),
		models.RecoveryBlockResolved, request.DeliveryStreamID, string(models.DeliverySubmissionFailed),
		models.DeliveryAbandonedAuthorizedAction, settledAt, settledAt, blockID,
		request.SessionID, request.IncarnationID, request.HarnessGeneration,
		request.SubmissionID, request.DeliveryStreamID, models.RecoveryBlockOpen)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}
