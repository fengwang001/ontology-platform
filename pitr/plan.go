package pitr

import (
	"context"
	"sort"
)

// chainEntry binds a timeline to the half-open interval in which it is
// effective on the ancestor chain of some target timeline.
type chainEntry struct {
	tli TimelineID
	lo  Position // inclusive
	hi  Position // exclusive; maxInt64 for the target timeline itself
}

// chain builds the ancestor chain of target, ordered root -> target. Each
// timeline is effective on [fork, next fork); the target is effective from
// its own fork with no upper bound.
func (r *Registry) chain(target TimelineID) ([]chainEntry, bool) {
	var rev []Timeline
	cur := target
	for cur != 0 {
		t, ok := r.timelines[cur]
		if !ok {
			return nil, false
		}
		rev = append(rev, t)
		cur = t.Parent
	}
	out := make([]chainEntry, 0, len(rev))
	// rev is target -> root. Walking root -> target, each timeline's window
	// ends at the fork of the timeline immediately closer to the target.
	for i := len(rev) - 1; i >= 0; i-- {
		t := rev[i]
		hi := Position(1<<63 - 1)
		if i > 0 {
			hi = rev[i-1].Fork
		}
		out = append(out, chainEntry{tli: t.ID, lo: t.Fork, hi: hi})
	}
	return out, true
}

// effectiveTLI returns the timeline effective at position p on the chain.
func effectiveTLI(chain []chainEntry, p Position) TimelineID {
	for i := len(chain) - 1; i >= 0; i-- {
		e := chain[i]
		if p >= e.lo && p < e.hi {
			return e.tli
		}
	}
	return 0
}

// clip maps archived segments onto the chain and returns, per effective
// timeline, the intervals covered inside that timeline's effective window.
// It also returns the archived end: the maximum end of any clipped interval.
func (r *Registry) clippedCoverage(chain []chainEntry) (map[TimelineID][]ReplayStep, Position) {
	byTLI := map[TimelineID][]ReplayStep{}
	var archivedEnd Position
	for _, e := range chain {
		for _, s := range r.segments[e.tli] {
			lo := maxPos(s.Start, e.lo)
			hi := minPos(s.End, e.hi)
			if lo >= hi {
				continue
			}
			byTLI[e.tli] = append(byTLI[e.tli], ReplayStep{Seg: s, Start: lo, End: hi})
			if hi > archivedEnd {
				archivedEnd = hi
			}
		}
		sort.Slice(byTLI[e.tli], func(i, j int) bool {
			if byTLI[e.tli][i].Start != byTLI[e.tli][j].Start {
				return byTLI[e.tli][i].Start < byTLI[e.tli][j].Start
			}
			return byTLI[e.tli][i].Seg.TLI < byTLI[e.tli][j].Seg.TLI
		})
	}
	return byTLI, archivedEnd
}

// firstGap walks coverage from start and returns the first position with no
// covering interval of the effective timeline, or -1 when fully covered to
// end (exclusive).
func firstGap(chain []chainEntry, cov map[TimelineID][]ReplayStep, start, end Position) Position {
	p := start
	for p < end {
		tli := effectiveTLI(chain, p)
		steps := cov[tli]
		idx := sort.Search(len(steps), func(i int) bool { return steps[i].End > p })
		if idx >= len(steps) || steps[idx].Start > p {
			return p
		}
		p = steps[idx].End
	}
	return -1
}

// chooseBackup picks the usable backup with the greatest Upper, breaking ties
// by the greater timeline ID. Usable backups sit on an ancestor timeline with
// Upper inside that timeline's effective window and not past end.
func chooseBackup(chain []chainEntry, backups []Backup, end Position) (Backup, bool) {
	window := map[TimelineID]chainEntry{}
	for _, e := range chain {
		window[e.tli] = e
	}
	var best Backup
	found := false
	for _, b := range backups {
		e, ok := window[b.TLI]
		if !ok {
			continue
		}
		if b.Upper < e.lo || b.Upper > e.hi || b.Upper > end {
			continue
		}
		if !found || b.Upper > best.Upper || (b.Upper == best.Upper && b.TLI > best.TLI) {
			best, found = b, true
		}
	}
	return best, found
}

// buildSteps assembles the ordered replay steps covering [start, end).
func buildSteps(chain []chainEntry, cov map[TimelineID][]ReplayStep, start, end Position) []ReplayStep {
	var steps []ReplayStep
	p := start
	for p < end {
		tli := effectiveTLI(chain, p)
		list := cov[tli]
		idx := sort.Search(len(list), func(i int) bool { return list[i].End > p })
		s := list[idx]
		hi := minPos(s.End, end)
		steps = append(steps, ReplayStep{Seg: s.Seg, Start: p, End: hi})
		p = hi
	}
	return steps
}

func maxPos(a, b Position) Position {
	if a > b {
		return a
	}
	return b
}

func minPos(a, b Position) Position {
	if a < b {
		return a
	}
	return b
}

// PlanRecovery builds the deterministic recovery plan for
// (targetTLI, target, inclusive) without mutating the registry.
//
// Errors are reported in this fixed order:
//  1. target timeline missing
//  2. target beyond the archived end
//  3. no usable base backup
//  4. gap in the archived log (the first gap position is attached)
func (r *Registry) PlanRecovery(ctx context.Context, targetTLI TimelineID, target Position, inclusive bool) (Plan, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mode := "exclusive"
	if inclusive {
		mode = "inclusive"
	}
	r.emit("plan_input", map[string]any{
		"target_tli": targetTLI, "target": target, "mode": mode,
	})

	ch, ok := r.chain(targetTLI)
	if !ok {
		r.emit("plan_reject", map[string]any{"reason": "timeline_not_found", "target_tli": targetTLI})
		return Plan{}, ErrTimelineNotFound
	}

	end := target
	if inclusive {
		end = target + 1
	}

	cov, archivedEnd := r.clippedCoverage(ch)
	if end > archivedEnd {
		r.emit("plan_reject", map[string]any{
			"reason": "beyond_archive", "end": end, "archived_end": archivedEnd,
		})
		return Plan{}, ErrTargetBeyondArchive
	}

	backup, ok := chooseBackup(ch, r.backups, end)
	if !ok {
		r.emit("plan_reject", map[string]any{"reason": "no_backup", "end": end})
		return Plan{}, ErrNoBackup
	}

	if gap := firstGap(ch, cov, backup.Upper, end); gap >= 0 {
		r.emit("plan_reject", map[string]any{"reason": "gap", "position": gap})
		return Plan{}, &GapError{Position: gap}
	}

	steps := buildSteps(ch, cov, backup.Upper, end)
	plan := Plan{
		TargetTLI: targetTLI,
		Target:    target,
		Inclusive: inclusive,
		End:       end,
		Backup:    backup,
		Steps:     steps,
	}
	r.emit("plan_ok", map[string]any{
		"backup": backup.ID, "backup_tli": backup.TLI, "backup_upper": backup.Upper,
		"steps": len(steps), "fork": end,
	})
	return plan, nil
}
