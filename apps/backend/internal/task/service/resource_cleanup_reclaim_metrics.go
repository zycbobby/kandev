package service

import "expvar"

// archiveReclaimCounters counts archived-worktree reclaim outcomes per the
// closed outcome set (reclaimed, force_discarded, dirty, in_use, stale,
// failed), following the same expvar.NewMap convention as orphan_reap_total.
var archiveReclaimCounters = expvar.NewMap("archive_reclaim_total")

func recordArchiveReclaimOutcome(outcome string) {
	archiveReclaimCounters.Add(outcome, 1)
}
