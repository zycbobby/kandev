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
