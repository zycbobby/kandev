package remoteinbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func pageFixture(workspaceID string, pendingIDs []string, createdAt string, hiddenCount int, nextCursor string, snoozeExpiry *string) workspacePage {
	bundles := make([]remoteBundle, 0, len(pendingIDs))
	for _, id := range pendingIDs {
		bundles = append(bundles, remoteBundle{PendingID: id, CreatedAt: createdAt})
	}
	return workspacePage{
		workspaceID: workspaceID,
		page: remoteInboxPage{
			Bundles: bundles, HiddenCount: hiddenCount, NextCursor: nextCursor,
			NextSnoozeExpiry: snoozeExpiry,
		},
	}
}

func TestAggregateInboxPage(t *testing.T) {
	t.Run("orders created_at asc then pending_id, sums hidden_count", func(t *testing.T) {
		pages := []workspacePage{
			pageFixture("ws-1", []string{"p-b", "p-a"}, "2026-10-28T10:00:00Z", 1, "", nil),
			pageFixture("ws-2", []string{"p-9"}, "2026-10-28T09:00:00Z", 2, "", nil),
			pageFixture("ws-3", nil, "", 0, "", nil),
		}
		page := aggregatePages(testInstanceID, pages, 50)
		var order []string
		for _, b := range page.Bundles {
			order = append(order, b.PendingID)
		}
		require.Equal(t, []string{"p-9", "p-a", "p-b"}, order)
		require.Equal(t, 3, page.Count)
		require.Equal(t, 3, page.HiddenCount)
		require.False(t, page.HasMore)
	})

	t.Run("unparseable created_at sorts first, not panicking", func(t *testing.T) {
		pages := []workspacePage{
			{workspaceID: "ws-1", page: remoteInboxPage{Bundles: []remoteBundle{
				{PendingID: "p-bad", CreatedAt: "not-a-time"},
				{PendingID: "p-ok", CreatedAt: "2026-10-28T10:00:00Z"},
			}}},
		}
		page := aggregatePages(testInstanceID, pages, 50)
		require.Equal(t, []string{"p-bad", "p-ok"}, []string{page.Bundles[0].PendingID, page.Bundles[1].PendingID})
	})

	t.Run("cap truncates and flags has_more", func(t *testing.T) {
		pages := []workspacePage{
			pageFixture("ws-1", []string{"p-1", "p-2", "p-3"}, "2026-10-28T10:00:00Z", 0, "", nil),
			pageFixture("ws-2", []string{"p-4"}, "2026-10-28T09:00:00Z", 0, "more", nil),
		}
		page := aggregatePages(testInstanceID, pages, 3)
		require.Len(t, page.Bundles, 3)
		require.Equal(t, []string{"p-4", "p-1", "p-2"}, []string{
			page.Bundles[0].PendingID, page.Bundles[1].PendingID, page.Bundles[2].PendingID})
		require.Equal(t, 3, page.Count)
		require.True(t, page.HasMore)
	})

	t.Run("next_snooze_expiry takes the earliest non-nil value", func(t *testing.T) {
		late := "2026-10-28T14:00:00Z"
		early := "2026-10-28T12:00:00Z"
		pages := []workspacePage{
			pageFixture("ws-1", []string{"p-1"}, "2026-10-28T10:00:00Z", 0, "", nil),
			pageFixture("ws-2", []string{"p-2"}, "2026-10-28T10:00:01Z", 0, "", &late),
			pageFixture("ws-3", []string{"p-3"}, "2026-10-28T10:00:02Z", 0, "", &early),
		}
		page := aggregatePages(testInstanceID, pages, 50)
		require.NotNil(t, page.NextSnoozeExpiry)
		require.Equal(t, time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), *page.NextSnoozeExpiry)
	})
}

func TestAggregateHiddenPage(t *testing.T) {
	snoozed := "2026-10-28T12:00:00Z"
	pages := []workspacePage{
		{workspaceID: "ws-1", page: remoteInboxPage{
			Bundles: []remoteBundle{{PendingID: "p-1", CreatedAt: "2026-10-28T10:00:00Z", State: "snoozed", SnoozeUntil: &snoozed}},
			Total:   5,
		}},
		{workspaceID: "ws-2", page: remoteInboxPage{
			Bundles: []remoteBundle{{PendingID: "p-2", CreatedAt: "2026-10-28T09:00:00Z", State: "dismissed"}},
			Total:   2,
		}},
	}
	page := aggregatePages(testInstanceID, pages, 50)
	require.Len(t, page.Bundles, 2)
	require.Equal(t, "p-2", page.Bundles[0].PendingID)
	require.Equal(t, 7, page.Total)
	require.Equal(t, "snoozed", page.Bundles[1].State)
	require.NotNil(t, page.Bundles[1].SnoozeUntil)
}

func TestAggregateTagsInstanceIdentity(t *testing.T) {
	pages := []workspacePage{pageFixture("ws-2", []string{"p-1"}, "2026-10-28T10:00:00Z", 0, "", nil)}
	page := aggregatePages(testInstanceID, pages, 50)
	require.Len(t, page.Bundles, 1)
	require.Equal(t, testInstanceID, page.Bundles[0].InstanceID)
	require.Equal(t, "ws-2", page.Bundles[0].WorkspaceID)
}

func needsYouRowFixture(kind, id, reference string) NeedsYouRow {
	if kind == RowKindWaiting {
		return NeedsYouRow{Kind: kind, ID: id, TaskID: "task-" + id, ReferenceTime: reference}
	}
	return NeedsYouRow{Kind: kind, PendingID: id, CreatedAt: reference}
}

func needsYouPageFixture(workspaceID string, rows []NeedsYouRow, hiddenCount, total int, hasMore bool, snoozeExpiry *string) needsYouWorkspacePage {
	return needsYouWorkspacePage{
		workspaceID: workspaceID,
		page: remoteNeedsYouPage{
			Rows: rows, HiddenCount: hiddenCount, Total: total, HasMore: hasMore,
			NextSnoozeExpiry: snoozeExpiry,
		},
	}
}

func TestAggregateNeedsYouPage(t *testing.T) {
	t.Run("orders by the row's own reference time then id, across kinds", func(t *testing.T) {
		pages := []needsYouWorkspacePage{
			needsYouPageFixture("ws-1", []NeedsYouRow{
				needsYouRowFixture(RowKindQuestion, "q-2", "2026-10-28T10:00:02Z"),
				needsYouRowFixture(RowKindWaiting, "mw-1", "2026-10-28T10:00:00Z"),
			}, 0, 0, false, nil),
			needsYouPageFixture("ws-2", []NeedsYouRow{
				needsYouRowFixture(RowKindQuestion, "q-0", "2026-10-28T10:00:00Z"),
				needsYouRowFixture(RowKindWaiting, "mw-9", "2026-10-28T09:59:00Z"),
			}, 0, 0, false, nil),
		}
		page := aggregateNeedsYouPages(testInstanceID, pages, 50)
		var ids []string
		for _, row := range page.Rows {
			ids = append(ids, needsYouRowDisplayID(row))
		}
		// A question's created_at and a waiting row's reference_time are the
		// same merge key; the shared instant falls back to the row id
		// ("mw-1" sorts before "q-0").
		require.Equal(t, []string{"mw-9", "mw-1", "q-0", "q-2"}, ids)
		require.Equal(t, 4, page.Count)
	})

	t.Run("cap truncates and flags has_more, keeping the earliest rows", func(t *testing.T) {
		pages := []needsYouWorkspacePage{
			needsYouPageFixture("ws-1", []NeedsYouRow{
				needsYouRowFixture(RowKindQuestion, "q-1", "2026-10-28T10:00:00Z"),
				needsYouRowFixture(RowKindWaiting, "mw-2", "2026-10-28T10:00:01Z"),
				needsYouRowFixture(RowKindQuestion, "q-3", "2026-10-28T10:00:02Z"),
			}, 0, 0, false, nil),
			needsYouPageFixture("ws-2", []NeedsYouRow{
				needsYouRowFixture(RowKindWaiting, "mw-4", "2026-10-28T09:00:00Z"),
			}, 0, 0, true, nil),
		}
		page := aggregateNeedsYouPages(testInstanceID, pages, 3)
		require.Len(t, page.Rows, 3)
		require.Equal(t, []string{"mw-4", "q-1", "mw-2"}, []string{
			needsYouRowDisplayID(page.Rows[0]),
			needsYouRowDisplayID(page.Rows[1]),
			needsYouRowDisplayID(page.Rows[2]),
		})
		require.Equal(t, 3, page.Count)
		require.True(t, page.HasMore)
	})

	t.Run("sums hidden_count and total, earliest snooze expiry wins", func(t *testing.T) {
		late := "2026-10-28T14:00:00Z"
		early := "2026-10-28T12:00:00Z"
		pages := []needsYouWorkspacePage{
			needsYouPageFixture("ws-1", []NeedsYouRow{
				needsYouRowFixture(RowKindQuestion, "q-1", "2026-10-28T10:00:00Z"),
			}, 1, 5, false, &late),
			needsYouPageFixture("ws-2", []NeedsYouRow{
				needsYouRowFixture(RowKindWaiting, "mw-2", "2026-10-28T10:00:01Z"),
			}, 2, 2, false, &early),
		}
		page := aggregateNeedsYouPages(testInstanceID, pages, 50)
		require.Equal(t, 3, page.HiddenCount)
		require.Equal(t, 7, page.Total)
		require.NotNil(t, page.NextSnoozeExpiry)
		require.Equal(t, time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC), *page.NextSnoozeExpiry)
		require.False(t, page.HasMore)
	})

	t.Run("stamps instance and origin-workspace identity on every row", func(t *testing.T) {
		pages := []needsYouWorkspacePage{
			needsYouPageFixture("ws-2", []NeedsYouRow{
				needsYouRowFixture(RowKindWaiting, "mw-1", "2026-10-28T10:00:00Z"),
				needsYouRowFixture(RowKindQuestion, "q-2", "2026-10-28T10:00:01Z"),
			}, 0, 0, false, nil),
		}
		page := aggregateNeedsYouPages(testInstanceID, pages, 50)
		require.Len(t, page.Rows, 2)
		for _, row := range page.Rows {
			require.Equal(t, testInstanceID, row.InstanceID)
			require.Equal(t, "ws-2", row.WorkspaceID)
		}
	})
}
