package remoteinbox

import (
	"sort"
	"time"
)

// aggregatePages merges the per-workspace pages of one instance into a single
// instance page: rows tagged with instance and origin-workspace identity,
// ordered created_at ascending with pending_id as the tie-break, capped at
// limit. HasMore is the disjunction of every workspace's own truncation
// (next_cursor) and the merge overflow. HiddenCount and Total sum each
// workspace's own counts; NextSnoozeExpiry is the earliest expiry seen.
func aggregatePages(instanceID string, pages []workspacePage, limit int) *Page {
	merged := make([]Bundle, 0, len(pages))
	for _, wp := range pages {
		for _, row := range wp.page.Bundles {
			merged = append(merged, Bundle{
				PendingID:    row.PendingID,
				TaskID:       row.TaskID,
				SessionID:    row.SessionID,
				SessionState: row.SessionState,
				TaskTitle:    row.TaskTitle,
				CreatedAt:    row.CreatedAt,
				Context:      row.Context,
				Messages:     row.Messages,
				State:        row.State,
				SnoozeUntil:  row.SnoozeUntil,
				InstanceID:   instanceID,
				WorkspaceID:  wp.workspaceID,
			})
		}
	}
	sortBundles(merged)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	page := &Page{Bundles: merged, Count: len(merged)}
	for _, wp := range pages {
		page.HiddenCount += wp.page.HiddenCount
		page.Total += wp.page.Total
		if wp.page.NextCursor != "" {
			page.HasMore = true
		}
		if expiry := parseOptionalTime(wp.page.NextSnoozeExpiry); expiry != nil {
			if page.NextSnoozeExpiry == nil || expiry.Before(*page.NextSnoozeExpiry) {
				page.NextSnoozeExpiry = expiry
			}
		}
	}
	if limit > 0 {
		// The merge only sees one page per workspace, so overflow past the
		// cap proves at least one workspace held more rows than the page
		// carried; recompute against the pre-cap sum is impossible after
		// truncation, so the overflow flag is derived before it.
		page.HasMore = page.HasMore || mergeOverflow(pages, limit)
	}
	return page
}

// mergeOverflow reports whether the untruncated merge would have exceeded
// limit. It runs over the source pages because aggregatePages truncates its
// own copy first.
func mergeOverflow(pages []workspacePage, limit int) bool {
	total := 0
	for _, wp := range pages {
		total += len(wp.page.Bundles)
		if total > limit {
			return true
		}
	}
	return false
}

// aggregateNeedsYouPages merges the per-workspace merged needs-you pages of
// one instance into a single instance page: rows tagged with instance and
// origin-workspace identity, ordered by the row's own reference time (a
// question row's created_at, a waiting row's reference_time) ascending with
// the row id as the tie-break, capped at limit. HasMore is the disjunction of
// every workspace's own truncation and the merge overflow. HiddenCount and
// Total sum each workspace's own counts; NextSnoozeExpiry is the earliest
// expiry seen.
func aggregateNeedsYouPages(instanceID string, pages []needsYouWorkspacePage, limit int) *NeedsYouPage {
	merged := make([]NeedsYouRow, 0, len(pages))
	for _, wp := range pages {
		for _, row := range wp.page.Rows {
			row.InstanceID = instanceID
			row.WorkspaceID = wp.workspaceID
			merged = append(merged, row)
		}
	}
	sortNeedsYouRows(merged)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	page := &NeedsYouPage{Rows: merged, Count: len(merged)}
	for _, wp := range pages {
		page.HiddenCount += wp.page.HiddenCount
		page.Total += wp.page.Total
		if wp.page.HasMore {
			page.HasMore = true
		}
		if expiry := parseOptionalTime(wp.page.NextSnoozeExpiry); expiry != nil {
			if page.NextSnoozeExpiry == nil || expiry.Before(*page.NextSnoozeExpiry) {
				page.NextSnoozeExpiry = expiry
			}
		}
	}
	if limit > 0 {
		// Same derivation as the clarification merge: each workspace carried
		// one page, so an overflow past the cap proves at least one workspace
		// held more rows than its page carried.
		page.HasMore = page.HasMore || needsYouMergeOverflow(pages, limit)
	}
	return page
}

// needsYouMergeOverflow reports whether the untruncated merged needs-you read
// would have exceeded limit.
func needsYouMergeOverflow(pages []needsYouWorkspacePage, limit int) bool {
	total := 0
	for _, wp := range pages {
		total += len(wp.page.Rows)
		if total > limit {
			return true
		}
	}
	return false
}

// sortNeedsYouRows orders rows by their own reference time ascending with the
// row id as the tie-break, mirroring the local merged read's ordering
// contract. Unparseable reference values sort first (zero time) rather than
// dropping or failing the page.
func sortNeedsYouRows(rows []NeedsYouRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := needsYouRowReference(rows[i]), needsYouRowReference(rows[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return needsYouRowID(rows[i]) < needsYouRowID(rows[j])
	})
}

// needsYouRowReference is the row's merge key: the waiting row's
// reference_time, the question row's created_at.
func needsYouRowReference(row NeedsYouRow) time.Time {
	if row.Kind == RowKindWaiting {
		return parseBundleTime(row.ReferenceTime)
	}
	return parseBundleTime(row.CreatedAt)
}

// needsYouRowID is the row's tie-break key: the waiting row's mw identity,
// the question row's pending id.
func needsYouRowID(row NeedsYouRow) string {
	if row.Kind == RowKindWaiting {
		return row.ID
	}
	return row.PendingID
}

// sortBundles orders rows created_at ascending with pending_id as the
// tie-break. Unparseable created_at values sort first (zero time) rather
// than dropping or failing the page.
func sortBundles(bundles []Bundle) {
	sort.SliceStable(bundles, func(i, j int) bool {
		ti, tj := parseBundleTime(bundles[i].CreatedAt), parseBundleTime(bundles[j].CreatedAt)
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return bundles[i].PendingID < bundles[j].PendingID
	})
}

func parseBundleTime(raw string) time.Time {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseOptionalTime(raw *string) *time.Time {
	if raw == nil {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil
	}
	return &parsed
}
