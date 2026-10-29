package clarification

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/authz"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"go.uber.org/zap"
)

// ManualStepWaitStore is the derived waiting-row surface the merged
// Needs-you Inbox read needs. Exported because RegisterRoutes and
// InboxBootSummary take it as the waiting source behind the merged read; it
// is satisfied by the task repository.
type ManualStepWaitStore interface {
	ListManualStepWaits(ctx context.Context, opts taskmodels.ManualStepWaitOptions) (*taskmodels.ManualStepWaitPage, error)
}

const (
	needsYouRowKindQuestion = "question"
	needsYouRowKindWaiting  = "waiting"

	errNeedsYouRowNotFound = "needs-you inbox row not found"
)

// needsYouQuestionRowView is one merged question row: the existing bundle
// view plus the kind discriminant.
type needsYouQuestionRowView struct {
	inboxBundleView
	Kind string `json:"kind"`
}

// needsYouWaitingRowView is one merged manual-step waiting row. It carries no
// messages and no answer surface: the row's only affordances are opening the
// task and the dismiss/snooze menu.
type needsYouWaitingRowView struct {
	Kind          string `json:"kind"`
	ID            string `json:"id"`
	TaskID        string `json:"task_id"`
	TaskTitle     string `json:"task_title"`
	WorkspaceID   string `json:"workspace_id"`
	StepID        string `json:"step_id"`
	StepName      string `json:"step_name"`
	ReferenceTime string `json:"reference_time"`
}

// needsYouHiddenQuestionRowView is one hidden question row: the bundle view
// plus the kind discriminant and why it is hidden.
type needsYouHiddenQuestionRowView struct {
	inboxBundleView
	Kind        string  `json:"kind"`
	State       string  `json:"state"`
	SnoozeUntil *string `json:"snooze_until"`
}

// needsYouHiddenWaitingRowView is one hidden waiting row.
type needsYouHiddenWaitingRowView struct {
	needsYouWaitingRowView
	State       string  `json:"state"`
	SnoozeUntil *string `json:"snooze_until"`
}

// needsYouListResponse is GET /api/v1/needs-you-inbox's response envelope:
// one merged read's rows, count, hidden count, truncation flag, and snooze
// timer, all from the same source (needs-you-inbox-waiting-rows REQ-005).
type needsYouListResponse struct {
	Rows             []interface{} `json:"rows"`
	Count            int           `json:"count"`
	HiddenCount      int           `json:"hidden_count"`
	HasMore          bool          `json:"has_more"`
	NextSnoozeExpiry *string       `json:"next_snooze_expiry"`
}

// needsYouHiddenListResponse is GET /api/v1/needs-you-inbox/hidden's response
// envelope. total is the merged workspace-wide hidden count; count is
// page-scoped.
type needsYouHiddenListResponse struct {
	Rows  []interface{} `json:"rows"`
	Count int           `json:"count"`
	Total int           `json:"total"`
}

// needsYouMergeEntry is one row candidate in the merged ordering: the union
// key (reference time, then id) plus the wire view. The same merge runs for
// the list endpoint and the boot summary (design D5), so count, truncation,
// and ordering can never drift between them.
type needsYouMergeEntry struct {
	id        string
	reference time.Time
	view      interface{}
}

// mergeNeedsYouEntries merges question and waiting entries into the
// (reference_time, id) ascending order, truncates to limit, and reports
// has_more: true when the merged set overflows the limit or any source itself
// reported truncation.
func mergeNeedsYouEntries(entries []needsYouMergeEntry, limit int, sourceHasMore ...bool) ([]needsYouMergeEntry, bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		if !entries[i].reference.Equal(entries[j].reference) {
			return entries[i].reference.Before(entries[j].reference)
		}
		return entries[i].id < entries[j].id
	})
	hasMore := len(entries) > limit
	for _, source := range sourceHasMore {
		hasMore = hasMore || source
	}
	if len(entries) > limit {
		entries = entries[:limit]
	}
	return entries, hasMore
}

// needsYouMergedRead is the outcome of one merged local read.
type needsYouMergedRead struct {
	rows             []interface{}
	count            int
	hiddenCount      int
	hasMore          bool
	nextSnoozeExpiry *time.Time
}

// needsYouSources is the bounded raw read both the row-building read and the
// count-only boot read derive from.
type needsYouSources struct {
	bundlePage     *taskmodels.ClarificationBundlePage
	waitPage       *taskmodels.ManualStepWaitPage
	questionHidden taskmodels.ClarificationInboxHiddenSummary
}

// readNeedsYouSources performs the two bounded page reads and the question
// hidden summary behind every merged read. A failure of either source fails
// the whole read.
func (h *Handlers) readNeedsYouSources(
	ctx context.Context, userID, workspaceID string, limit int, onlyHidden bool,
) (*needsYouSources, error) {
	now := h.now()
	sidecar := &taskmodels.ClarificationSidecarFilter{UserID: userID, Only: onlyHidden, Now: now}

	bundlePage, err := h.inboxBundles.ListUnresolvedClarificationBundles(ctx, taskmodels.ListClarificationBundlesOptions{
		Unscoped:    true,
		WorkspaceID: workspaceID,
		Limit:       limit,
		Sidecar:     sidecar,
	})
	if err != nil {
		return nil, err
	}
	waitPage, err := h.inboxWaits.ListManualStepWaits(ctx, taskmodels.ManualStepWaitOptions{
		WorkspaceID: workspaceID,
		Limit:       limit,
		Sidecar:     sidecar,
		OnlyHidden:  onlyHidden,
	})
	if err != nil {
		return nil, err
	}
	questionHidden, err := h.inboxBundles.CountHiddenClarificationBundles(ctx, taskmodels.ListClarificationBundlesOptions{
		Unscoped: true, WorkspaceID: workspaceID, Limit: 1,
		Sidecar: &taskmodels.ClarificationSidecarFilter{UserID: userID, Only: true, Now: now},
	})
	if err != nil {
		return nil, err
	}
	return &needsYouSources{bundlePage: bundlePage, waitPage: waitPage, questionHidden: questionHidden}, nil
}

// readNeedsYouInbox performs the one merged local read behind the list and
// hidden endpoints: hydrated question views and waiting rows, merged by
// (reference_time, id). A failure of either source fails the whole read; a
// partial list is never returned.
func (h *Handlers) readNeedsYouInbox(
	ctx context.Context, userID, workspaceID string, limit int, onlyHidden bool,
) (*needsYouMergedRead, error) {
	sources, err := h.readNeedsYouSources(ctx, userID, workspaceID, limit, onlyHidden)
	if err != nil {
		return nil, err
	}

	entries := make([]needsYouMergeEntry, 0, len(sources.bundlePage.Bundles)+len(sources.waitPage.Rows))
	if onlyHidden {
		hiddenViews, err := h.buildInboxHiddenResponse(ctx, userID, sources.bundlePage)
		if err != nil {
			return nil, err
		}
		for _, view := range hiddenViews.Bundles {
			entries = append(entries, needsYouMergeEntry{
				id: view.PendingID, reference: parsedInboxViewTime(view.CreatedAt),
				view: needsYouHiddenQuestionRowView{
					inboxBundleView: view.inboxBundleView,
					Kind:            needsYouRowKindQuestion,
					State:           view.State,
					SnoozeUntil:     view.SnoozeUntil,
				},
			})
		}
	} else {
		questionViews, err := h.buildInboxBundleViews(ctx, sources.bundlePage.Bundles)
		if err != nil {
			return nil, err
		}
		for _, view := range questionViews {
			entries = append(entries, needsYouMergeEntry{
				id: view.PendingID, reference: parsedInboxViewTime(view.CreatedAt),
				view: needsYouQuestionRowView{inboxBundleView: view, Kind: needsYouRowKindQuestion},
			})
		}
	}
	for _, row := range sources.waitPage.Rows {
		if onlyHidden {
			entries = append(entries, needsYouMergeEntry{id: row.ID, reference: row.ReferenceTime, view: hiddenWaitingView(row)})
		} else {
			entries = append(entries, needsYouMergeEntry{id: row.ID, reference: row.ReferenceTime, view: waitingView(row)})
		}
	}
	return h.finishNeedsYouRead(sources, entries, limit), nil
}

// readNeedsYouInboxCounts is the boot summary's read: the same bounded
// sources and the same merge helper as the list endpoint, reduced to counts
// without message hydration (the boot payload carries no rows). Like the
// bundle-only boot summary it has always produced, the count is the merged
// page's row count.
func (h *Handlers) readNeedsYouInboxCounts(
	ctx context.Context, userID, workspaceID string, limit int,
) (*needsYouMergedRead, error) {
	sources, err := h.readNeedsYouSources(ctx, userID, workspaceID, limit, false)
	if err != nil {
		return nil, err
	}
	entries := make([]needsYouMergeEntry, 0, len(sources.bundlePage.Bundles)+len(sources.waitPage.Rows))
	for _, bundle := range sources.bundlePage.Bundles {
		entries = append(entries, needsYouMergeEntry{id: bundle.PendingID, reference: bundle.CreatedAt})
	}
	for _, row := range sources.waitPage.Rows {
		entries = append(entries, needsYouMergeEntry{id: row.ID, reference: row.ReferenceTime})
	}
	return h.finishNeedsYouRead(sources, entries, limit), nil
}

// finishNeedsYouRead merges the entries and derives the response aggregates
// shared by every merged read.
func (h *Handlers) finishNeedsYouRead(sources *needsYouSources, entries []needsYouMergeEntry, limit int) *needsYouMergedRead {
	merged, hasMore := mergeNeedsYouEntries(entries, limit, sources.bundlePage.HasMore, sources.waitPage.HasMore)
	read := &needsYouMergedRead{
		count:            len(merged),
		hasMore:          hasMore,
		hiddenCount:      sources.questionHidden.HiddenCount + sources.waitPage.HiddenCount,
		nextSnoozeExpiry: earliestSnoozeExpiry(sources.questionHidden.NextSnoozeExpiry, sources.waitPage.NextSnoozeExpiry),
	}
	read.rows = make([]interface{}, 0, len(merged))
	for _, entry := range merged {
		read.rows = append(read.rows, entry.view)
	}
	return read
}

// waitingView shapes one waiting row for the main list.
func waitingView(row taskmodels.ManualStepWaitRow) needsYouWaitingRowView {
	return needsYouWaitingRowView{
		Kind:          needsYouRowKindWaiting,
		ID:            row.ID,
		TaskID:        row.TaskID,
		TaskTitle:     row.TaskTitle,
		WorkspaceID:   row.WorkspaceID,
		StepID:        row.StepID,
		StepName:      row.StepName,
		ReferenceTime: row.ReferenceTime.UTC().Format(time.RFC3339),
	}
}

// hiddenWaitingView shapes one hidden waiting row for the hidden enumeration.
func hiddenWaitingView(row taskmodels.ManualStepWaitRow) needsYouHiddenWaitingRowView {
	return needsYouHiddenWaitingRowView{
		needsYouWaitingRowView: waitingView(row),
		State:                  string(row.SidecarState),
		SnoozeUntil:            formatOptionalInboxTime(row.SnoozeUntil),
	}
}

// parsedInboxViewTime parses an inboxBundleView's RFC3339 created_at back
// into the merge key's instant.
func parsedInboxViewTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// earliestSnoozeExpiry picks the earliest of two optional expiries.
func earliestSnoozeExpiry(a, b *time.Time) *time.Time {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if b.Before(*a) {
		return b
	}
	return a
}

// parseNeedsYouQuery validates the merged endpoints' shared query table:
// workspace_id is required, limit defaults to 50 and clamps at 200.
func (h *Handlers) parseNeedsYouQuery(c *gin.Context) (workspaceID string, limit int, ok bool) {
	workspaceID = strings.TrimSpace(c.Query("workspace_id"))
	if workspaceID == "" {
		respondInboxError(c, http.StatusBadRequest, "workspace_id is required")
		return "", 0, false
	}
	limit, err := parseInboxLimit(c.Query("limit"))
	if err != nil {
		respondInboxError(c, http.StatusBadRequest, err.Error())
		return "", 0, false
	}
	return workspaceID, limit, true
}

// authorizeNeedsYouRead applies the same workspace-scope rule every other
// inbox read applies, writing the response itself on failure.
func (h *Handlers) authorizeNeedsYouRead(c *gin.Context, ctx context.Context, workspaceID string) bool {
	if err := h.inboxTasks.AuthorizeWorkspaceScope(ctx, workspaceID, authz.ScopeWorkspaceRead); err != nil {
		if h.respondInboxReadError(c, c.Request.Context(), err) {
			return false
		}
		h.respondInboxWorkspaceAuthzError(c, err)
		return false
	}
	return true
}

// httpListNeedsYouInbox backs GET /api/v1/needs-you-inbox: the merged
// question-plus-waiting page.
func (h *Handlers) httpListNeedsYouInbox(c *gin.Context) {
	requestCtx := c.Request.Context()
	workspaceID, limit, ok := h.parseNeedsYouQuery(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(requestCtx, inboxReadTimeout)
	defer cancel()
	if !h.authorizeNeedsYouRead(c, ctx, workspaceID) {
		return
	}

	read, err := h.readNeedsYouInbox(ctx, InboxUserID(ctx), workspaceID, limit, false)
	if err != nil {
		if h.respondInboxReadError(c, requestCtx, err) {
			return
		}
		h.logger.Error("failed to read merged needs-you inbox", zap.Error(err))
		respondInboxError(c, http.StatusInternalServerError, errInboxInternal)
		return
	}
	c.JSON(http.StatusOK, needsYouListResponse{
		Rows:             read.rows,
		Count:            read.count,
		HiddenCount:      read.hiddenCount,
		HasMore:          read.hasMore,
		NextSnoozeExpiry: formatOptionalInboxTime(read.nextSnoozeExpiry),
	})
}

// httpListNeedsYouInboxHidden backs GET /api/v1/needs-you-inbox/hidden: the
// merged hidden enumeration.
func (h *Handlers) httpListNeedsYouInboxHidden(c *gin.Context) {
	requestCtx := c.Request.Context()
	workspaceID, limit, ok := h.parseNeedsYouQuery(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(requestCtx, inboxReadTimeout)
	defer cancel()
	if !h.authorizeNeedsYouRead(c, ctx, workspaceID) {
		return
	}

	read, err := h.readNeedsYouInbox(ctx, InboxUserID(ctx), workspaceID, limit, true)
	if err != nil {
		if h.respondInboxReadError(c, requestCtx, err) {
			return
		}
		h.logger.Error("failed to read hidden merged needs-you inbox", zap.Error(err))
		respondInboxError(c, http.StatusInternalServerError, errInboxInternal)
		return
	}
	c.JSON(http.StatusOK, needsYouHiddenListResponse{
		Rows:  read.rows,
		Count: read.count,
		Total: read.hiddenCount,
	})
}

// registerNeedsYouInboxRoutes wires the merged endpoints. Called from
// RegisterRoutes with the flag on and a waiting store present.
func (h *Handlers) registerNeedsYouInboxRoutes(router *gin.Engine) {
	needsYou := router.Group("/api/v1/needs-you-inbox")
	needsYou.GET("", h.httpListNeedsYouInbox)
	needsYou.GET("/hidden", h.httpListNeedsYouInboxHidden)
}

// isManualStepWaitRowID reports whether pendingID names a manual-step waiting
// row (an mw:<task>:<step>:<transition> identity) rather than a clarification
// bundle.
func isManualStepWaitRowID(pendingID string) bool {
	_, _, _, ok := taskmodels.ParseManualStepWaitRowID(pendingID)
	return ok
}

// authorizeManualStepWaitAccessOrRespond authorizes a sidecar write that
// addresses a waiting row by its mw identity: resolve the task named in the
// identity, then apply the same workspace-scope rule the reads use. The
// sidecar row itself is per-user, so a workspace-read-authorized caller can
// only ever change their own view.
func (h *Handlers) authorizeManualStepWaitAccessOrRespond(c *gin.Context, pendingID string) bool {
	taskID, _, _, ok := taskmodels.ParseManualStepWaitRowID(pendingID)
	if !ok || h.inboxTasks == nil {
		respondInboxError(c, http.StatusNotFound, errNeedsYouRowNotFound)
		return false
	}
	tasks, err := h.inboxTasks.GetTasksByIDs(c.Request.Context(), []string{taskID})
	if err != nil || len(tasks) == 0 || tasks[0] == nil {
		respondInboxError(c, http.StatusNotFound, errNeedsYouRowNotFound)
		return false
	}
	if err := h.inboxTasks.AuthorizeWorkspaceScope(c.Request.Context(), tasks[0].WorkspaceID, authz.ScopeWorkspaceRead); err != nil {
		h.respondInboxWorkspaceAuthzError(c, err)
		return false
	}
	return true
}
