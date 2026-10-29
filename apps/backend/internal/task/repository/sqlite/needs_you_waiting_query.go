package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kandev/kandev/internal/db/dialect"
	"github.com/kandev/kandev/internal/task/models"
	wfmodels "github.com/kandev/kandev/internal/workflow/models"
	v1 "github.com/kandev/kandev/pkg/api/v1"
	"go.uber.org/zap"
)

// manualStepWaitOverscanFactor is how many candidates per requested row the
// SQL narrowing fetches before the Go-side StepRunsOnEntry filter runs. Bounded
// by manualStepWaitMaxOverscan (a multiple of the handler layer's 200-row
// inbox cap) so a direct repository caller cannot request an unbounded scan.
const (
	manualStepWaitOverscanFactor = 4
	manualStepWaitMaxLimit       = 200
	manualStepWaitMaxOverscan    = manualStepWaitOverscanFactor*manualStepWaitMaxLimit + 1
)

// ListManualStepWaits returns one page of derived manual-step waiting rows
// (needs-you-inbox-waiting-rows design D3/D4). SQL narrows candidates by
// workspace, non-terminal state, complete_task_on_enter=0, no STARTING/RUNNING
// session, and no queued message; the StepRunsOnEntry judgment then runs once
// here in Go over a bounded overscan before truncating to opts.Limit. HasMore
// is true when candidates survive the filter beyond the limit or the overscan
// itself hit its cap, so a truncated candidate window can never masquerade as
// an exhaustive one. The hidden summary is computed with the same predicates
// in the same call, so the page's HiddenCount/NextSnoozeExpiry can never
// disagree with the row query about what counts as hidden.
func (r *Repository) ListManualStepWaits(
	ctx context.Context,
	opts models.ManualStepWaitOptions,
) (page *models.ManualStepWaitPage, err error) {
	if opts.Limit < 1 {
		return nil, fmt.Errorf("ListManualStepWaits: limit must be >= 1, got %d", opts.Limit)
	}
	operationCtx, release, wait, err := r.beginClarificationRead(ctx)
	if err != nil {
		r.logClarificationRead("list_manual_step_waits", wait, 0, err)
		return nil, err
	}
	started := time.Now()
	defer func() {
		err = normalizeClarificationReadError(ctx, operationCtx, err)
		release()
		r.logClarificationRead("list_manual_step_waits", wait, time.Since(started), err)
	}()
	if err := operationCtx.Err(); err != nil {
		return nil, err
	}

	// queued_messages is queue-owned and lives in another store's schema init;
	// probe it rather than assuming it exists (the attachment reads' pattern).
	// Without the table no task can have a queued turn, so the predicate is
	// simply omitted.
	queueTablePresent, err := r.tableExistsContext(operationCtx, "queued_messages")
	if err != nil {
		return nil, err
	}

	overscan := opts.Limit*manualStepWaitOverscanFactor + 1
	if overscan > manualStepWaitMaxOverscan {
		overscan = manualStepWaitMaxOverscan
	}

	// A nil Sidecar normalizes to an empty filter: the sidecar join stays in
	// the query (its columns are always selected) but addresses an empty user
	// id, so no sidecar row matches and no row is hidden.
	sidecar := opts.Sidecar
	if sidecar == nil {
		sidecar = &models.ClarificationSidecarFilter{}
	}

	drv := r.ro.DriverName()
	rows, err := r.ro.QueryContext(operationCtx, r.ro.Rebind(manualStepWaitRowsQuery(drv, queueTablePresent, sidecar)),
		manualStepWaitArgs(opts.WorkspaceID, sidecar, overscan)...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	candidates, err := scanManualStepWaitRows(rows, dialect.IsPostgres(drv))
	if err != nil {
		return nil, err
	}

	filtered := r.filterManualStepWaitCandidates(candidates)
	page = &models.ManualStepWaitPage{Rows: filtered}
	if len(filtered) > opts.Limit {
		page.Rows = filtered[:opts.Limit]
		page.HasMore = true
	} else if len(candidates) == overscan {
		// The overscan cap was reached, so more candidates may exist beyond the
		// window even when fewer than the limit survived the filter.
		page.HasMore = true
	}

	summary, err := r.countHiddenManualStepWaits(operationCtx, drv, queueTablePresent, opts)
	if err != nil {
		return nil, err
	}
	page.HiddenCount = summary.HiddenCount
	page.NextSnoozeExpiry = summary.NextSnoozeExpiry
	return page, nil
}

// manualStepWaitRowIDExpr builds the mw:<task>:<step>:<transition> identity in
// SQL, falling back to transition id 0 when the step entry has no ledger row.
func manualStepWaitRowIDExpr(drv string) string {
	if dialect.IsPostgres(drv) {
		return "'mw:' || t.id || ':' || t.workflow_step_id || ':' || CAST(COALESCE(tr.id, 0) AS TEXT)"
	}
	return "'mw:' || t.id || ':' || t.workflow_step_id || ':' || COALESCE(tr.id, 0)"
}

// manualStepWaitTableExpr is the shared FROM/JOIN/WHERE narrowing both the row
// query and the hidden-count aggregate select from. sidecar nil leaves the
// query without the per-user sidecar join and predicate entirely.
func manualStepWaitTableExpr(drv string, queueTablePresent bool, sidecar *models.ClarificationSidecarFilter) string {
	rowID := manualStepWaitRowIDExpr(drv)
	sidecarJoin := ""
	sidecarPredicate := ""
	if sidecar != nil {
		// The sidecar is addressed by the same computed mw identity the row
		// carries, so one episode's dismiss/snooze marker is keyed exactly like
		// the row the operator dismissed.
		sidecarJoin = fmt.Sprintf(`
		LEFT JOIN clarification_inbox_sidecar cs ON cs.pending_id = %s AND cs.user_id = ?`, rowID)
		predicate, _ := clarificationSidecarPredicate(sidecar)
		sidecarPredicate = "\n\t\t  AND " + predicate
	}
	queuedPredicate := ""
	if queueTablePresent {
		queuedPredicate = `
		  AND NOT EXISTS (SELECT 1 FROM queued_messages q WHERE q.task_id = t.id)`
	}
	return fmt.Sprintf(`
		FROM tasks t
		JOIN workflow_steps ws ON ws.id = t.workflow_step_id
		LEFT JOIN task_step_transitions tr ON tr.id = (
			SELECT tt.id FROM task_step_transitions tt
			WHERE tt.task_id = t.id AND tt.to_workflow_step_id = t.workflow_step_id
			ORDER BY tt.id DESC LIMIT 1
		)%s
		WHERE t.workspace_id = ?
		  AND t.state NOT IN (?, ?, ?)
		  AND t.archived_at IS NULL
		  AND ws.complete_task_on_enter = 0
		  AND NOT EXISTS (
			SELECT 1 FROM task_sessions s
			WHERE s.task_id = t.id AND s.state IN (?, ?)
		  )%s%s
	`, sidecarJoin, queuedPredicate, sidecarPredicate)
}

// manualStepWaitRowsQuery selects one candidate row per task: the task and
// step identity, the episode's reference time (the entry transition's
// occurred_at, falling back to the task's updated_at), the step configuration
// the Go-side filter needs, and this operator's sidecar state.
func manualStepWaitRowsQuery(drv string, queueTablePresent bool, sidecar *models.ClarificationSidecarFilter) string {
	return fmt.Sprintf(`
		SELECT
			%s AS row_id,
			t.id AS task_id,
			t.title AS task_title,
			t.workspace_id AS workspace_id,
			t.workflow_step_id AS step_id,
			ws.name AS step_name,
			COALESCE(tr.occurred_at, t.updated_at) AS reference_time,
			ws.events AS step_events,
			ws.pull_from_step_id AS pull_from_step_id,
			cs.state AS sidecar_state,
			cs.snooze_until AS snooze_until%s
		ORDER BY reference_time ASC, row_id ASC
		LIMIT ?
	`, manualStepWaitRowIDExpr(drv), manualStepWaitTableExpr(drv, queueTablePresent, sidecar))
}

// manualStepWaitArgs binds the arguments both queries share, in the order the
// table expression assembles them: the sidecar join's user id, then the
// narrowing predicates' workspace, terminal task states, executing session
// states, and the sidecar predicate's comparison instant. limit is the row
// query's overscan bound; the count query passes limit < 0 to omit it.
func manualStepWaitArgs(workspaceID string, sidecar *models.ClarificationSidecarFilter, limit int) []interface{} {
	args := []interface{}{}
	if sidecar != nil {
		args = append(args, sidecar.UserID)
	}
	args = append(args,
		workspaceID,
		string(v1.TaskStateCompleted), string(v1.TaskStateFailed), string(v1.TaskStateCancelled),
		string(models.TaskSessionStateStarting), string(models.TaskSessionStateRunning),
	)
	if sidecar != nil {
		args = append(args, sidecar.Now)
	}
	if limit >= 0 {
		args = append(args, limit)
	}
	return args
}

// manualStepWaitCandidate is one scanned candidate row, still carrying the
// step configuration the Go-side StepRunsOnEntry filter consumes.
type manualStepWaitCandidate struct {
	row      models.ManualStepWaitRow
	events   sql.NullString
	pullFrom string
}

func scanManualStepWaitRows(rows *sql.Rows, isPostgres bool) ([]manualStepWaitCandidate, error) {
	var out []manualStepWaitCandidate
	for rows.Next() {
		var candidate manualStepWaitCandidate
		var err error
		if isPostgres {
			candidate, err = scanManualStepWaitCandidatePostgres(rows)
		} else {
			candidate, err = scanManualStepWaitCandidateSQLite(rows)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// scanManualStepWaitCandidateSQLite scans one candidate row on SQLite, where
// COALESCE over a TIMESTAMP column loses the declared type the driver needs
// to auto-convert, so the instant arrives as text (the same gap
// scanClarificationBundleRows handles).
func scanManualStepWaitCandidateSQLite(rows *sql.Rows) (manualStepWaitCandidate, error) {
	var candidate manualStepWaitCandidate
	var stepName, pullFrom, sidecarState sql.NullString
	var referenceRaw sql.NullString
	var snoozeUntil sql.NullTime
	if err := rows.Scan(
		&candidate.row.ID, &candidate.row.TaskID, &candidate.row.TaskTitle, &candidate.row.WorkspaceID,
		&candidate.row.StepID, &stepName, &referenceRaw, &candidate.events, &pullFrom,
		&sidecarState, &snoozeUntil,
	); err != nil {
		return candidate, err
	}
	if referenceRaw.Valid && referenceRaw.String != "" {
		candidate.row.ReferenceTime = parseLegacyTimestamp(referenceRaw.String)
	}
	finishManualStepWaitScan(&candidate, stepName, pullFrom, sidecarState, snoozeUntil)
	return candidate, nil
}

func scanManualStepWaitCandidatePostgres(rows *sql.Rows) (manualStepWaitCandidate, error) {
	var candidate manualStepWaitCandidate
	var stepName, pullFrom, sidecarState sql.NullString
	var referenceTime sql.NullTime
	var snoozeUntil sql.NullTime
	if err := rows.Scan(
		&candidate.row.ID, &candidate.row.TaskID, &candidate.row.TaskTitle, &candidate.row.WorkspaceID,
		&candidate.row.StepID, &stepName, &referenceTime, &candidate.events, &pullFrom,
		&sidecarState, &snoozeUntil,
	); err != nil {
		return candidate, err
	}
	if referenceTime.Valid {
		candidate.row.ReferenceTime = referenceTime.Time.UTC()
	}
	finishManualStepWaitScan(&candidate, stepName, pullFrom, sidecarState, snoozeUntil)
	return candidate, nil
}

// finishManualStepWaitScan assembles the fields both dialect scans share.
func finishManualStepWaitScan(
	candidate *manualStepWaitCandidate,
	stepName, pullFrom, sidecarState sql.NullString,
	snoozeUntil sql.NullTime,
) {
	candidate.row.StepName = stepName.String
	candidate.pullFrom = pullFrom.String
	candidate.row.SidecarState = models.ClarificationSidecarState(sidecarState.String)
	if snoozeUntil.Valid {
		until := snoozeUntil.Time
		candidate.row.SnoozeUntil = &until
	}
}

// filterManualStepWaitCandidates applies the one manual-step judgment in Go
// (design D4): a candidate whose step starts work on entry is not waiting for
// the operator. A step whose events document cannot be unmarshaled is skipped
// with a warning rather than failing the whole page or silently listing.
func (r *Repository) filterManualStepWaitCandidates(candidates []manualStepWaitCandidate) []models.ManualStepWaitRow {
	rows := make([]models.ManualStepWaitRow, 0, len(candidates))
	for _, candidate := range candidates {
		runs, ok := stepRunsOnEntryFromStored(candidate.events, candidate.pullFrom)
		if !ok {
			if r.log != nil {
				r.log.Warn("manual step wait: unreadable workflow step events, omitting candidate",
					zap.String("task_id", candidate.row.TaskID),
					zap.String("step_id", candidate.row.StepID))
			}
			continue
		}
		if runs {
			continue
		}
		rows = append(rows, candidate.row)
	}
	return rows
}

// stepRunsOnEntryFromStored evaluates wfmodels.StepRunsOnEntry over the stored
// events JSON and pull_from_step_id. ok is false when the events document is
// present but not valid JSON.
func stepRunsOnEntryFromStored(events sql.NullString, pullFrom string) (runs, ok bool) {
	step := &wfmodels.WorkflowStep{PullFromStepID: pullFrom}
	if events.Valid && events.String != "" {
		var parsed wfmodels.StepEvents
		if err := json.Unmarshal([]byte(events.String), &parsed); err != nil {
			return false, false
		}
		step.Events = parsed
	}
	return wfmodels.StepRunsOnEntry(step), true
}

// countHiddenManualStepWaits aggregates the workspace-wide hidden summary for
// the same operator and predicates as the row query: how many waiting rows the
// sidecar is hiding right now, and the earliest snooze expiry among them. The
// summary always uses the hidden-only direction, mirroring
// CountHiddenClarificationBundles, regardless of the page's own direction.
func (r *Repository) countHiddenManualStepWaits(
	ctx context.Context, drv string, queueTablePresent bool, opts models.ManualStepWaitOptions,
) (summary models.ClarificationInboxHiddenSummary, err error) {
	sidecar := &models.ClarificationSidecarFilter{Only: true}
	if opts.Sidecar != nil {
		*sidecar = *opts.Sidecar
		sidecar.Only = true
	}
	query := fmt.Sprintf(`SELECT COUNT(*), MIN(cs.snooze_until)%s`,
		manualStepWaitTableExpr(drv, queueTablePresent, sidecar))

	row := r.ro.QueryRowContext(ctx, r.ro.Rebind(query),
		manualStepWaitArgs(opts.WorkspaceID, sidecar, -1)...)
	if dialect.IsPostgres(drv) {
		var nextExpiry sql.NullTime
		if err := row.Scan(&summary.HiddenCount, &nextExpiry); err != nil {
			return models.ClarificationInboxHiddenSummary{}, err
		}
		return summaryFromScan(summary.HiddenCount, nextExpiry.Valid, nextExpiry.Time), nil
	}
	var nextExpiryRaw sql.NullString
	if err := row.Scan(&summary.HiddenCount, &nextExpiryRaw); err != nil {
		return models.ClarificationInboxHiddenSummary{}, err
	}
	if !nextExpiryRaw.Valid || nextExpiryRaw.String == "" {
		return summaryFromScan(summary.HiddenCount, false, time.Time{}), nil
	}
	return summaryFromScan(summary.HiddenCount, true, parseLegacyTimestamp(nextExpiryRaw.String)), nil
}
