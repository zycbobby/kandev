package clarification

// Coverage for the merged Needs-you Inbox read (needs-you-inbox-waiting-rows
// design D5): question bundles and manual-step waiting rows merge into one
// response whose count, list, hidden_count, and has_more all come from that
// single merged read; ordering is (reference_time, id) ascending and stable;
// hidden rows never consume page slots; and a failure of either local source
// fails the whole read rather than returning a partial list.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/kandev/kandev/internal/common/logger"
	taskmodels "github.com/kandev/kandev/internal/task/models"
	"github.com/kandev/kandev/internal/task/repository/repoerrors"
	"github.com/kandev/kandev/internal/task/service"
)

// fakeManualStepWaitStore is a ManualStepWaitStore double.
type fakeManualStepWaitStore struct {
	page    *taskmodels.ManualStepWaitPage
	listErr error

	lastOpts taskmodels.ManualStepWaitOptions
	calls    int
}

func (f *fakeManualStepWaitStore) ListManualStepWaits(
	_ context.Context, opts taskmodels.ManualStepWaitOptions,
) (*taskmodels.ManualStepWaitPage, error) {
	f.calls++
	f.lastOpts = opts
	if f.listErr != nil {
		return nil, f.listErr
	}
	if f.page == nil {
		return &taskmodels.ManualStepWaitPage{}, nil
	}
	return f.page, nil
}

func waitingRow(overrides taskmodels.ManualStepWaitRow) taskmodels.ManualStepWaitRow {
	base := taskmodels.ManualStepWaitRow{
		ID:            "mw:t-wait:step-manual:7",
		TaskID:        "t-wait",
		TaskTitle:     "Waiting task",
		WorkspaceID:   "w1",
		StepID:        "step-manual",
		StepName:      "Manual review",
		ReferenceTime: testInboxNow.Add(-30 * time.Minute),
	}
	if overrides.ID != "" {
		base.ID = overrides.ID
	}
	if overrides.TaskID != "" {
		base.TaskID = overrides.TaskID
	}
	if overrides.TaskTitle != "" {
		base.TaskTitle = overrides.TaskTitle
	}
	if overrides.WorkspaceID != "" {
		base.WorkspaceID = overrides.WorkspaceID
	}
	if overrides.StepID != "" {
		base.StepID = overrides.StepID
	}
	if overrides.StepName != "" {
		base.StepName = overrides.StepName
	}
	if !overrides.ReferenceTime.IsZero() {
		base.ReferenceTime = overrides.ReferenceTime
	}
	base.SidecarState = overrides.SidecarState
	base.SnoozeUntil = overrides.SnoozeUntil
	return base
}

// newNeedsYouTestHandler wires a handler with both local sources.
func newNeedsYouTestHandler(
	t *testing.T,
	msgs map[string][]*taskmodels.Message,
	tasks *fakeInboxTasks,
	bundles *fakeInboxBundleStore,
	waits *fakeManualStepWaitStore,
) *Handlers {
	t.Helper()
	h := newInboxTestHandler(t, msgs, alwaysAllowAuthorizer{}, tasks, bundles)
	h.inboxWaits = waits
	return h
}

// needsYouRow is the decoded wire shape of one merged row.
type needsYouRow struct {
	Kind string `json:"kind"`

	PendingID    string           `json:"pending_id"`
	TaskID       string           `json:"task_id"`
	SessionID    string           `json:"session_id"`
	SessionState string           `json:"session_state"`
	TaskTitle    string           `json:"task_title"`
	CreatedAt    string           `json:"created_at"`
	Context      string           `json:"context"`
	Messages     []map[string]any `json:"messages"`

	ID            string `json:"id"`
	WorkspaceID   string `json:"workspace_id"`
	StepID        string `json:"step_id"`
	StepName      string `json:"step_name"`
	ReferenceTime string `json:"reference_time"`

	State       string  `json:"state"`
	SnoozeUntil *string `json:"snooze_until"`
}

type needsYouListBody struct {
	Rows             []needsYouRow `json:"rows"`
	Count            int           `json:"count"`
	HiddenCount      int           `json:"hidden_count"`
	HasMore          bool          `json:"has_more"`
	NextSnoozeExpiry *string       `json:"next_snooze_expiry"`
}

type needsYouHiddenBody struct {
	Rows  []needsYouRow `json:"rows"`
	Count int           `json:"count"`
	Total int           `json:"total"`
}

func runNeedsYouGet(h *Handlers, target string, handler gin.HandlerFunc) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = req
	handler(c)
	return rec
}

func TestHttpListNeedsYouInbox_MergesQuestionsAndWaitingRows(t *testing.T) {
	bundleCreatedAt := testInboxNow.Add(-time.Hour)
	msgs := map[string][]*taskmodels.Message{
		"p1": {{ID: "m1", TaskSessionID: "s1", TaskID: "t1", CreatedAt: bundleCreatedAt,
			Metadata: map[string]any{"pending_id": "p1", "question_id": "q1"}}},
	}
	bundles := &fakeInboxBundleStore{
		page: &taskmodels.ClarificationBundlePage{
			Bundles: []taskmodels.ClarificationBundleSummary{
				{PendingID: "p1", TaskID: "t1", SessionID: "s1", CreatedAt: bundleCreatedAt},
			},
		},
		summary: taskmodels.ClarificationInboxHiddenSummary{HiddenCount: 1},
	}
	tasks := &fakeInboxTasks{
		tasks:    map[string]*taskmodels.Task{"t1": {ID: "t1", Title: "Question task"}},
		sessions: map[string]*taskmodels.TaskSession{"s1": {ID: "s1", State: "WAITING_FOR_INPUT"}},
	}
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows:        []taskmodels.ManualStepWaitRow{waitingRow(taskmodels.ManualStepWaitRow{})},
			HiddenCount: 2,
		},
	}
	h := newNeedsYouTestHandler(t, msgs, tasks, bundles, waits)

	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp needsYouListBody
	decodeInboxBody(t, rec, &resp)

	// The waiting row's reference time is 30 minutes newer than the bundle's
	// creation, so the merged order is question first, waiting second.
	if len(resp.Rows) != 2 {
		t.Fatalf("rows = %d, want 2: %s", len(resp.Rows), rec.Body.String())
	}
	if resp.Rows[0].Kind != "question" || resp.Rows[0].PendingID != "p1" {
		t.Fatalf("first row = %+v, want the question row p1", resp.Rows[0])
	}
	second := resp.Rows[1]
	if second.Kind != "waiting" || second.ID != "mw:t-wait:step-manual:7" {
		t.Fatalf("second row = %+v, want the waiting row", second)
	}
	if second.TaskID != "t-wait" || second.TaskTitle != "Waiting task" || second.WorkspaceID != "w1" {
		t.Fatalf("waiting row task fields = %+v", second)
	}
	if second.StepID != "step-manual" || second.StepName != "Manual review" {
		t.Fatalf("waiting row step fields = %+v", second)
	}
	if second.ReferenceTime == "" {
		t.Fatal("waiting row reference_time empty, want an RFC3339 instant")
	}
	if second.Messages != nil {
		t.Fatalf("waiting row carries messages = %v, want none (no inline answer)", second.Messages)
	}

	// count/list/hidden_count/has_more all come from this one response.
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2 (len(rows) of the merged page)", resp.Count)
	}
	if resp.HiddenCount != 3 {
		t.Errorf("hidden_count = %d, want 3 (1 question + 2 waiting)", resp.HiddenCount)
	}
	if resp.HasMore {
		t.Error("has_more = true, want false")
	}
	if resp.NextSnoozeExpiry != nil {
		t.Errorf("next_snooze_expiry = %v, want nil", *resp.NextSnoozeExpiry)
	}
}

func TestHttpListNeedsYouInbox_OrderByReferenceThenID(t *testing.T) {
	sameInstant := testInboxNow.Add(-time.Hour)
	// Two question rows and one waiting row share one reference instant: the
	// id tie-break decides, stably ("mw:..." < "p2" < "p9").
	msgs := map[string][]*taskmodels.Message{
		"p2": {{ID: "m2", TaskSessionID: "s2", TaskID: "t2",
			Metadata: map[string]any{"pending_id": "p2", "question_id": "q1"}}},
		"p9": {{ID: "m9", TaskSessionID: "s9", TaskID: "t9",
			Metadata: map[string]any{"pending_id": "p9", "question_id": "q1"}}},
	}
	bundles := &fakeInboxBundleStore{
		page: &taskmodels.ClarificationBundlePage{
			Bundles: []taskmodels.ClarificationBundleSummary{
				{PendingID: "p9", TaskID: "t9", SessionID: "s9", CreatedAt: sameInstant},
				{PendingID: "p2", TaskID: "t2", SessionID: "s2", CreatedAt: sameInstant},
			},
		},
	}
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows: []taskmodels.ManualStepWaitRow{
				waitingRow(taskmodels.ManualStepWaitRow{ID: "mw:t-wait:step-manual:9", ReferenceTime: sameInstant}),
			},
		},
	}
	h := newNeedsYouTestHandler(t, msgs, &fakeInboxTasks{tasks: map[string]*taskmodels.Task{
		"t9": {ID: "t9"}, "t2": {ID: "t2"},
	}}, bundles, waits)

	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp needsYouListBody
	decodeInboxBody(t, rec, &resp)
	if len(resp.Rows) != 3 {
		t.Fatalf("rows = %d, want 3: %s", len(resp.Rows), rec.Body.String())
	}
	if resp.Rows[0].Kind != "waiting" || resp.Rows[0].ID != "mw:t-wait:step-manual:9" {
		t.Fatalf("first row = %+v, want the waiting row (id tie-break)", resp.Rows[0])
	}
	if resp.Rows[1].PendingID != "p2" || resp.Rows[2].PendingID != "p9" {
		t.Fatalf("question order = [%s, %s], want [p2, p9] (pending_id tie-break)", resp.Rows[1].PendingID, resp.Rows[2].PendingID)
	}
}

func TestHttpListNeedsYouInbox_TruncationHasMoreAndHiddenExcluded(t *testing.T) {
	bundleCreatedAt := testInboxNow.Add(-3 * time.Hour)
	var summaries []taskmodels.ClarificationBundleSummary
	msgs := map[string][]*taskmodels.Message{}
	for i := 0; i < 2; i++ {
		pendingID := string(rune('a'+i)) + "1"
		summaries = append(summaries, taskmodels.ClarificationBundleSummary{
			PendingID: pendingID, TaskID: "t" + pendingID, SessionID: "s" + pendingID, CreatedAt: bundleCreatedAt.Add(time.Duration(i) * time.Minute),
		})
		msgs[pendingID] = []*taskmodels.Message{{
			ID: "m" + pendingID, TaskSessionID: "s" + pendingID, TaskID: "t" + pendingID,
			Metadata: map[string]any{"pending_id": pendingID, "question_id": "q1"},
		}}
	}
	bundles := &fakeInboxBundleStore{
		page:    &taskmodels.ClarificationBundlePage{Bundles: summaries, HasMore: true},
		summary: taskmodels.ClarificationInboxHiddenSummary{HiddenCount: 2},
	}
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows: []taskmodels.ManualStepWaitRow{
				waitingRow(taskmodels.ManualStepWaitRow{ReferenceTime: testInboxNow.Add(-time.Minute)}),
			},
			HiddenCount: 1,
		},
	}
	h := newNeedsYouTestHandler(t, msgs, &fakeInboxTasks{tasks: map[string]*taskmodels.Task{
		"ta1": {ID: "ta1"}, "tb1": {ID: "tb1"},
	}}, bundles, waits)

	// limit=2: the merged set is 3 rows, so the page truncates to 2 and
	// has_more reflects the merge overflow (and the bundle source's own
	// truncation).
	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1&limit=2", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp needsYouListBody
	decodeInboxBody(t, rec, &resp)
	if len(resp.Rows) != 2 || resp.Count != 2 {
		t.Fatalf("rows/count = %d/%d, want the limit 2 (hidden rows never consume page slots)", len(resp.Rows), resp.Count)
	}
	if !resp.HasMore {
		t.Error("has_more = false, want true (merged overflow plus bundle-source truncation)")
	}
	// The hidden rows are not in the page: the handler must ask both sources
	// with the main-read exclusion filter.
	if bundles.lastOpts.Sidecar == nil || bundles.lastOpts.Sidecar.Only {
		t.Errorf("bundle sidecar filter = %+v, want the main-read exclusion direction", bundles.lastOpts.Sidecar)
	}
	if waits.lastOpts.OnlyHidden || waits.lastOpts.Sidecar == nil || waits.lastOpts.Sidecar.Only {
		t.Errorf("waiting sidecar filter = %+v onlyHidden=%v, want the main-read exclusion direction", waits.lastOpts.Sidecar, waits.lastOpts.OnlyHidden)
	}
	if resp.HiddenCount != 3 {
		t.Errorf("hidden_count = %d, want 3 (2 question + 1 waiting)", resp.HiddenCount)
	}
}

func TestHttpListNeedsYouInbox_EitherLocalReadFails_500NeverPartial(t *testing.T) {
	msgs := map[string][]*taskmodels.Message{}
	bundles := &fakeInboxBundleStore{
		page: &taskmodels.ClarificationBundlePage{
			Bundles: []taskmodels.ClarificationBundleSummary{{PendingID: "p1", TaskID: "t1", SessionID: "s1"}},
		},
		summary: taskmodels.ClarificationInboxHiddenSummary{},
	}
	// Message hydration fails: the merged read must fail rather than return
	// only the waiting rows.
	tasks := &fakeInboxTasks{tasks: map[string]*taskmodels.Task{"t1": {ID: "t1"}}}
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows: []taskmodels.ManualStepWaitRow{waitingRow(taskmodels.ManualStepWaitRow{})},
		},
	}
	h := newNeedsYouTestHandler(t, msgs, tasks, bundles, waits)
	bundles.findErr = errors.New("messages boom")
	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the question source fails", rec.Code)
	}
	var body map[string]any
	decodeInboxBody(t, rec, &body)
	if _, hasRows := body["rows"]; hasRows {
		t.Fatalf("response carried rows alongside the error: %s", rec.Body.String())
	}

	// The waiting source fails: no partial list of question rows either.
	bundles.findErr = nil
	msgs["p1"] = []*taskmodels.Message{{ID: "m1", TaskSessionID: "s1", TaskID: "t1",
		Metadata: map[string]any{"pending_id": "p1", "question_id": "q1"}}}
	waits.listErr = errors.New("waits boom")
	h = newNeedsYouTestHandler(t, msgs, tasks, bundles, waits)
	rec = runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 when the waiting source fails", rec.Code)
	}
	body = map[string]any{}
	decodeInboxBody(t, rec, &body)
	if _, hasRows := body["rows"]; hasRows {
		t.Fatalf("response carried rows alongside the error: %s", rec.Body.String())
	}
}

func TestHttpListNeedsYouInbox_EmptyResult_NeverNullRows(t *testing.T) {
	h := newNeedsYouTestHandler(t, map[string][]*taskmodels.Message{}, &fakeInboxTasks{}, &fakeInboxBundleStore{}, &fakeManualStepWaitStore{})
	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &struct{}{}); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	var raw map[string]json.RawMessage
	decodeInboxBody(t, rec, &raw)
	if string(raw["rows"]) == "null" {
		t.Fatal("rows = null, want []")
	}
}

func TestHttpListNeedsYouInbox_ValidationAndAuthz(t *testing.T) {
	h := newNeedsYouTestHandler(t, map[string][]*taskmodels.Message{}, &fakeInboxTasks{}, &fakeInboxBundleStore{}, &fakeManualStepWaitStore{})

	if rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox", h.httpListNeedsYouInbox); rec.Code != http.StatusBadRequest {
		t.Errorf("missing workspace_id status = %d, want 400", rec.Code)
	}
	if rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1&limit=0", h.httpListNeedsYouInbox); rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0 status = %d, want 400", rec.Code)
	}

	missing := &fakeInboxTasks{authzErr: repoerrors.ErrWorkspaceNotFound}
	h = newNeedsYouTestHandler(t, map[string][]*taskmodels.Message{}, missing, &fakeInboxBundleStore{}, &fakeManualStepWaitStore{})
	if rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox?workspace_id=w1", h.httpListNeedsYouInbox); rec.Code != http.StatusNotFound {
		t.Errorf("unknown workspace status = %d, want 404", rec.Code)
	}
}

func TestHttpListNeedsYouInboxHidden_MergesBothKindsWithStateAndTotal(t *testing.T) {
	bundleCreatedAt := testInboxNow.Add(-time.Hour)
	pendingID := "p-hidden"
	msgs := map[string][]*taskmodels.Message{
		pendingID: {{ID: "m1", TaskSessionID: "s1", TaskID: "t1", CreatedAt: bundleCreatedAt,
			Metadata: map[string]any{"pending_id": pendingID, "question_id": "q1"}}},
	}
	bundles := &fakeInboxBundleStore{
		page: &taskmodels.ClarificationBundlePage{
			Bundles: []taskmodels.ClarificationBundleSummary{
				{PendingID: pendingID, TaskID: "t1", SessionID: "s1", CreatedAt: bundleCreatedAt},
			},
		},
		summary: taskmodels.ClarificationInboxHiddenSummary{HiddenCount: 1},
		states: map[string]taskmodels.ClarificationInboxHiddenBundle{
			pendingID: {PendingID: pendingID, State: taskmodels.ClarificationSidecarSnoozed, SnoozeUntil: &testInboxNow},
		},
	}
	snoozeUntil := testInboxNow.Add(2 * time.Hour)
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows: []taskmodels.ManualStepWaitRow{
				waitingRow(taskmodels.ManualStepWaitRow{
					SidecarState: taskmodels.ClarificationSidecarDismissed,
				}),
				waitingRow(taskmodels.ManualStepWaitRow{
					ID: "mw:t-wait2:step-manual:8", TaskID: "t-wait2", ReferenceTime: testInboxNow.Add(-time.Minute),
					SidecarState: taskmodels.ClarificationSidecarSnoozed, SnoozeUntil: &snoozeUntil,
				}),
			},
			HiddenCount:      2,
			NextSnoozeExpiry: &snoozeUntil,
		},
	}
	h := newNeedsYouTestHandler(t, msgs, &fakeInboxTasks{tasks: map[string]*taskmodels.Task{"t1": {ID: "t1"}}}, bundles, waits)

	rec := runNeedsYouGet(h, "/api/v1/needs-you-inbox/hidden?workspace_id=w1", h.httpListNeedsYouInboxHidden)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var resp needsYouHiddenBody
	decodeInboxBody(t, rec, &resp)
	if len(resp.Rows) != 3 {
		t.Fatalf("rows = %d, want 3 (one question + two waiting): %s", len(resp.Rows), rec.Body.String())
	}
	if resp.Total != 3 {
		t.Errorf("total = %d, want 3 (1 question + 2 waiting hidden)", resp.Total)
	}
	if resp.Count != 3 {
		t.Errorf("count = %d, want 3", resp.Count)
	}
	// Order: the question's reference instant is oldest, then the two waiting
	// rows by reference time.
	if resp.Rows[0].Kind != "question" {
		t.Fatalf("first row = %+v, want the question", resp.Rows[0])
	}
	if resp.Rows[0].State != "snoozed" || resp.Rows[0].SnoozeUntil == nil {
		t.Errorf("hidden question row = %+v, want snoozed with an expiry", resp.Rows[0])
	}
	if resp.Rows[1].Kind != "waiting" || resp.Rows[1].State != "dismissed" {
		t.Errorf("first hidden waiting row = %+v, want dismissed", resp.Rows[1])
	}
	if resp.Rows[2].Kind != "waiting" || resp.Rows[2].State != "snoozed" || resp.Rows[2].SnoozeUntil == nil {
		t.Errorf("second hidden waiting row = %+v, want snoozed with an expiry", resp.Rows[2])
	}
	// The hidden enumeration must use the hidden-only direction on both sides.
	if bundles.lastOpts.Sidecar == nil || !bundles.lastOpts.Sidecar.Only {
		t.Errorf("bundle sidecar filter = %+v, want the hidden-only direction", bundles.lastOpts.Sidecar)
	}
	if !waits.lastOpts.OnlyHidden || waits.lastOpts.Sidecar == nil || !waits.lastOpts.Sidecar.Only {
		t.Errorf("waiting sidecar filter = %+v onlyHidden=%v, want the hidden-only direction", waits.lastOpts.Sidecar, waits.lastOpts.OnlyHidden)
	}
}

func TestNeedsYouInboxBootSummary_MergesBothSourcesWithSameBound(t *testing.T) {
	bundleCreatedAt := testInboxNow.Add(-time.Hour)
	bundles := &fakeInboxBundleStore{
		page: &taskmodels.ClarificationBundlePage{
			Bundles: []taskmodels.ClarificationBundleSummary{
				{PendingID: "p1", TaskID: "t1", SessionID: "s1", CreatedAt: bundleCreatedAt},
				{PendingID: "p2", TaskID: "t2", SessionID: "s2", CreatedAt: bundleCreatedAt.Add(time.Minute)},
			},
		},
		summary: taskmodels.ClarificationInboxHiddenSummary{HiddenCount: 3, NextSnoozeExpiry: &testInboxNow},
	}
	waits := &fakeManualStepWaitStore{
		page: &taskmodels.ManualStepWaitPage{
			Rows:             []taskmodels.ManualStepWaitRow{waitingRow(taskmodels.ManualStepWaitRow{})},
			NextSnoozeExpiry: func() *time.Time { earlier := testInboxNow.Add(-time.Hour); return &earlier }(),
		},
	}

	count, hasMore, nextSnoozeExpiry, err := InboxBootSummary(context.Background(), bundles, "w1", "user-1", testInboxNow, waits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3 (2 questions + 1 waiting, one merged read)", count)
	}
	if hasMore {
		t.Error("hasMore = true, want false")
	}
	if nextSnoozeExpiry == nil || !nextSnoozeExpiry.Equal(testInboxNow.Add(-time.Hour)) {
		t.Errorf("nextSnoozeExpiry = %v, want the earliest expiry across both sources", nextSnoozeExpiry)
	}
	if bundles.lastOpts.Limit != defaultInboxLimit || waits.lastOpts.Limit != defaultInboxLimit {
		t.Errorf("limits = %d/%d, want the default limit on both sources (same bound as the list endpoint)", bundles.lastOpts.Limit, waits.lastOpts.Limit)
	}
}

func TestNeedsYouInboxBootSummary_WaitingReadErrorPropagates(t *testing.T) {
	bundles := &fakeInboxBundleStore{page: &taskmodels.ClarificationBundlePage{}}
	waits := &fakeManualStepWaitStore{listErr: errors.New("boom")}

	_, _, _, err := InboxBootSummary(context.Background(), bundles, "w1", "user-1", testInboxNow, waits)
	if err == nil {
		t.Fatal("expected the waiting-source error to propagate rather than a bundle-only summary")
	}
}

// TestInboxSidecarWrite_AddressesWaitingRowsByMwIdentity pins the sidecar
// endpoints' second authorization path: a waiting row's mw identity is
// authorized through the task it names (workspace scope) and upserted into
// the same per-user sidecar the bundle writes use, so waiting rows reuse the
// existing dismiss/snooze/restore contract unchanged.
func TestInboxSidecarWrite_AddressesWaitingRowsByMwIdentity(t *testing.T) {
	msgs := map[string][]*taskmodels.Message{}
	tasks := &fakeInboxTasks{tasks: map[string]*taskmodels.Task{
		"t-wait": {ID: "t-wait", WorkspaceID: "w1"},
	}}
	bundles := &fakeInboxBundleStore{}
	waits := &fakeManualStepWaitStore{}
	h := newNeedsYouTestHandler(t, msgs, tasks, bundles, waits)

	rowID := "mw:t-wait:step-manual:7"
	rec := runInboxSidecarWrite(http.MethodPut, rowID, `{"state":"snoozed","snooze_duration":"1h"}`, h.httpUpsertInboxSidecar)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("snooze status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(bundles.upserted) != 1 {
		t.Fatalf("upserts = %d, want 1", len(bundles.upserted))
	}
	upsert := bundles.upserted[0]
	if upsert.pendingID != rowID || upsert.state != taskmodels.ClarificationSidecarSnoozed {
		t.Fatalf("upsert = %+v, want the mw identity snoozed", upsert)
	}
	if upsert.snoozeUntil == nil || !upsert.snoozeUntil.Equal(testInboxNow.Add(time.Hour)) {
		t.Fatalf("snoozeUntil = %v, want the handler clock plus 1h", upsert.snoozeUntil)
	}

	rec = runInboxSidecarWrite(http.MethodDelete, rowID, "", h.httpDeleteInboxSidecar)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("restore status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(bundles.deleted) != 1 || bundles.deleted[0] != rowID {
		t.Fatalf("deletes = %v, want [%s]", bundles.deleted, rowID)
	}
}

// TestInboxSidecarWrite_UnknownWaitingRowTask_404: an mw identity naming no
// task is indistinguishable from a foreign pending_id.
func TestInboxSidecarWrite_UnknownWaitingRowTask_404(t *testing.T) {
	h := newNeedsYouTestHandler(t, map[string][]*taskmodels.Message{}, &fakeInboxTasks{}, &fakeInboxBundleStore{}, &fakeManualStepWaitStore{})
	rec := runInboxSidecarWrite(http.MethodPut, "mw:no-such-task:step-manual:1", `{"state":"dismissed"}`, h.httpUpsertInboxSidecar)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

// TestInboxSidecarWrite_WaitingRowDeniedWorkspace_403: the mw path applies the
// same workspace-scope rule as the reads.
func TestInboxSidecarWrite_WaitingRowDeniedWorkspace_403(t *testing.T) {
	tasks := &fakeInboxTasks{
		tasks:    map[string]*taskmodels.Task{"t-wait": {ID: "t-wait", WorkspaceID: "w1"}},
		authzErr: service.ErrForbidden,
	}
	h := newNeedsYouTestHandler(t, map[string][]*taskmodels.Message{}, tasks, &fakeInboxBundleStore{}, &fakeManualStepWaitStore{})
	rec := runInboxSidecarWrite(http.MethodPut, "mw:t-wait:step-manual:7", `{"state":"dismissed"}`, h.httpUpsertInboxSidecar)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
}

// merged endpoints' registration: present with the flag on and a waiting
// store, absent otherwise, while the clarification-inbox routes keep their
// own contract.
func TestRegisterNeedsYouInboxRoutes_GatedByFlagAndWaitingStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	pathsFor := func(flag bool, waits ManualStepWaitStore) map[string]bool {
		router := gin.New()
		RegisterRoutes(router, nil, nil, nil, nil, nil, nil, logger.Default(), nil, nil, flag, waits)
		paths := map[string]bool{}
		for _, route := range router.Routes() {
			paths[route.Path] = true
		}
		return paths
	}

	on := pathsFor(true, &fakeManualStepWaitStore{})
	if !on["/api/v1/needs-you-inbox"] || !on["/api/v1/needs-you-inbox/hidden"] {
		t.Errorf("merged endpoints missing with the flag on: %v", on)
	}
	if !on["/api/v1/clarification-inbox"] {
		t.Error("clarification-inbox route missing: the question-row contract must be kept")
	}

	off := pathsFor(false, &fakeManualStepWaitStore{})
	if off["/api/v1/needs-you-inbox"] || off["/api/v1/needs-you-inbox/hidden"] {
		t.Errorf("merged endpoints registered with the flag off: %v", off)
	}

	noWaits := pathsFor(true, nil)
	if noWaits["/api/v1/needs-you-inbox"] {
		t.Error("merged endpoints registered without a waiting store")
	}
}
