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

// SettlePreIntakeAgentDeliverySubmission settles one submission that never
// entered the retained agentctl journal. The failed state, its idempotency
// effect, and the bound recovery blocks commit together or not at all.
func (r *Repository) SettlePreIntakeAgentDeliverySubmission(
	ctx context.Context,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
) (*models.AgentDeliveryPreIntakeSettlementResult, error) {
	if err := validatePreIntakeSettlementRequest(request); err != nil {
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
	if err := lockAgentDeliveryWrites(ctx, tx, r.db.DriverName(), []string{request.SessionID}, nil); err != nil {
		return nil, err
	}
	submission, err := r.loadPreIntakeSubmissionTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if submission.State == models.DeliverySubmissionFailed {
		return r.replayPreIntakeSettlementTx(ctx, tx, submission, request, settledAt)
	}
	if err := validatePreIntakeSubmissionState(submission, request); err != nil {
		return nil, err
	}
	if err := r.markPreIntakeSubmissionFailedTx(ctx, tx, submission, request, settledAt); err != nil {
		return nil, err
	}
	if err := r.insertPreIntakeSettlementEffectTx(ctx, tx, request, settledAt); err != nil {
		return nil, err
	}
	resolved, err := r.resolvePreIntakeDeliveryBlocksTx(ctx, tx, request, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	submission.State = models.DeliverySubmissionFailed
	submission.Outcome = string(models.DeliverySubmissionFailed)
	submission.UpdatedAt = settledAt
	return &models.AgentDeliveryPreIntakeSettlementResult{
		Settled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}, nil
}

func validatePreIntakeSettlementRequest(request *models.AgentDeliveryPreIntakeSettlementRequest) error {
	if request == nil || request.SessionID == "" || request.IncarnationID == "" ||
		request.HarnessGeneration <= 0 || request.SubmissionID == "" || request.ExpectedState == "" {
		return fmt.Errorf("pre-intake settlement identity and expected state are required")
	}
	return nil
}

func (r *Repository) loadPreIntakeSubmissionTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
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
	if err == sql.ErrNoRows {
		return nil, repoerrors.ErrAgentDeliveryPreIntakeSettlementStale
	}
	if err != nil {
		return nil, err
	}
	if submission.SessionID != request.SessionID || submission.IncarnationID != request.IncarnationID ||
		submission.HarnessGeneration != request.HarnessGeneration {
		return nil, repoerrors.ErrAgentDeliveryPreIntakeSettlementStale
	}
	if fullySettledAt.Valid {
		at := fullySettledAt.Time
		submission.FullySettledAt = &at
	}
	return &submission, nil
}

func validatePreIntakeSubmissionState(
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
) error {
	if submission.State != request.ExpectedState {
		return repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	if request.ExpectedOutcome != "" && submission.Outcome != request.ExpectedOutcome {
		return repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	if submission.TerminalSequence != 0 || submission.FullySettledAt != nil {
		return repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	return nil
}

func (r *Repository) markPreIntakeSubmissionFailedTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
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
		return repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	return nil
}

func (r *Repository) insertPreIntakeSettlementEffectTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
	settledAt time.Time,
) error {
	completedAt := settledAt
	inserted, err := insertDeliveryEffectTx(ctx, tx, r.db.Rebind, &models.AgentDeliveryEffect{
		EffectKey:         models.DeliveryPreIntakeEffectKey(request.SubmissionID),
		EffectType:        models.DeliveryPreIntakeEffectType,
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
		return repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	return nil
}

func (r *Repository) replayPreIntakeSettlementTx(
	ctx context.Context,
	tx *sqlx.Tx,
	submission *models.AgentDeliverySubmission,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
	settledAt time.Time,
) (*models.AgentDeliveryPreIntakeSettlementResult, error) {
	present, err := r.preIntakeSettlementEffectPresentTx(ctx, tx, request)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	resolved, err := r.resolvePreIntakeDeliveryBlocksTx(ctx, tx, request, settledAt)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &models.AgentDeliveryPreIntakeSettlementResult{
		Settled: true, AlreadySettled: true, Submission: submission, ResolvedBlockIDs: resolved,
	}, nil
}

func (r *Repository) preIntakeSettlementEffectPresentTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
) (bool, error) {
	var effect models.AgentDeliveryEffect
	err := tx.QueryRowxContext(ctx, r.db.Rebind(`
		SELECT stream_id, sequence, effect_type, state, session_id, incarnation_id,
		       harness_generation, submission_id, outcome
		FROM agent_delivery_effects WHERE effect_key = ?`),
		models.DeliveryPreIntakeEffectKey(request.SubmissionID)).Scan(
		&effect.StreamID, &effect.Sequence, &effect.EffectType, &effect.State,
		&effect.SessionID, &effect.IncarnationID, &effect.HarnessGeneration,
		&effect.SubmissionID, &effect.Outcome)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if effect.EffectType != models.DeliveryPreIntakeEffectType || effect.StreamID != "" ||
		effect.Sequence != 0 || effect.State != models.DeliveryEffectCompleted ||
		effect.SessionID != request.SessionID || effect.IncarnationID != request.IncarnationID ||
		effect.HarnessGeneration != request.HarnessGeneration || effect.SubmissionID != request.SubmissionID ||
		effect.Outcome != string(models.DeliverySubmissionFailed) {
		return false, repoerrors.ErrAgentDeliveryPreIntakeSettlementConflict
	}
	return true, nil
}

func (r *Repository) resolvePreIntakeDeliveryBlocksTx(
	ctx context.Context,
	tx *sqlx.Tx,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
	settledAt time.Time,
) ([]string, error) {
	query := `
		SELECT id FROM session_recovery_blocks
		WHERE session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?
		  AND delivery_stream_id = '' AND state = ?`
	args := []any{
		request.SessionID, request.IncarnationID, request.HarnessGeneration,
		request.SubmissionID, models.RecoveryBlockOpen,
	}
	if request.BlockID != "" {
		query += ` AND id = ?`
		args = append(args, request.BlockID)
	}
	rows, err := tx.QueryxContext(ctx, r.db.Rebind(query+` ORDER BY id`), args...)
	if err != nil {
		return nil, err
	}
	blockIDs, err := scanPreIntakeBlockIDs(rows)
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
		updated, err := resolvePreIntakeBlockTx(ctx, tx, r.db.Rebind, request, blockID, settledAt)
		if err != nil {
			return nil, err
		}
		if updated {
			resolved = append(resolved, blockID)
		}
	}
	return resolved, nil
}

func scanPreIntakeBlockIDs(rows *sqlx.Rows) ([]string, error) {
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

func resolvePreIntakeBlockTx(
	ctx context.Context,
	tx *sqlx.Tx,
	rebind func(string) string,
	request *models.AgentDeliveryPreIntakeSettlementRequest,
	blockID string,
	settledAt time.Time,
) (bool, error) {
	result, err := tx.ExecContext(ctx, rebind(`
		UPDATE session_recovery_blocks
		SET state = ?, delivery_stream_id = '', delivery_sequence = 0, delivery_turn_id = '',
		    delivery_outcome = ?, authorized_action = ?, updated_at = ?, resolved_at = ?
		WHERE id = ? AND session_id = ? AND incarnation_id = ? AND expected_generation = ?
		  AND consumer_reference = 'agent_delivery' AND delivery_submission_id = ?
		  AND delivery_stream_id = '' AND state = ?`),
		models.RecoveryBlockResolved, string(models.DeliverySubmissionFailed),
		"durable_delivery_not_delivered", settledAt, settledAt, blockID,
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
