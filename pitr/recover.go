package pitr

import (
	"context"
	"hash/fnv"
	"sort"
)

// payload renders the value replayed at (tli, pos). A nil segment value
// defaults to a deterministic per-position token so that states at the same
// position on different timelines differ after a fork.
func payload(tli TimelineID, pos Position, value any) any {
	if value != nil {
		return value
	}
	return pair{tli, pos}
}

type pair struct {
	tli TimelineID
	pos Position
}

// stateAt computes the state a direct replay to end would produce on
// targetTLI: restore from a virtual empty base at the start of the chain,
// then replay every position in [0, end) with the effective timeline.
// It assumes the archive fully covers the interval (planning already proved
// that for the post-backup range; the pre-backup range is supplied by the
// base backup).
func (r *Registry) stateAt(ch []chainEntry, cov map[TimelineID][]ReplayStep, end Position) uint64 {
	h := fnv.New64a()
	p := Position(0)
	for p < end {
		tli := effectiveTLI(ch, p)
		list := cov[tli]
		idx := sort.Search(len(list), func(i int) bool { return list[i].End > p })
		s := list[idx]
		hi := minPos(s.End, end)
		for pos := p; pos < hi; pos++ {
			v := payload(tli, pos, s.Seg.Value)
			h.Write([]byte(encode(v)))
		}
		p = hi
	}
	return h.Sum64()
}

// restoreAndReplay models a real recovery: restore the backup prefix on the
// backup's own ancestor chain, then apply each planned step in order.
func (r *Registry) restoreAndReplay(targetChain []chainEntry, p Plan) uint64 {
	backupChain, ok := r.chain(p.Backup.TLI)
	if !ok {
		backupChain = targetChain
	}
	bcov, _ := r.clippedCoverage(backupChain)
	h := fnv.New64a()

	pos := Position(0)
	for pos < p.Backup.Upper {
		tli := effectiveTLI(backupChain, pos)
		list := bcov[tli]
		idx := sort.Search(len(list), func(i int) bool { return list[i].End > pos })
		s := list[idx]
		hi := minPos(s.End, p.Backup.Upper)
		for q := pos; q < hi; q++ {
			h.Write([]byte(encode(payload(tli, q, s.Seg.Value))))
		}
		pos = hi
	}
	for _, step := range p.Steps {
		for q := step.Start; q < step.End; q++ {
			tli := effectiveTLI(targetChain, q)
			h.Write([]byte(encode(payload(tli, q, step.Seg.Value))))
		}
	}
	return h.Sum64()
}

// DirectState is the state produced by executing directly on tli up to end
// (exclusive). It is intended for tests asserting recovery equivalence; the
// archive must cover [0, end) on the effective timeline chain.
func (r *Registry) DirectState(ctx context.Context, tli TimelineID, end Position) (uint64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ch, ok := r.chain(tli)
	if !ok {
		return 0, ErrTimelineNotFound
	}
	cov, _ := r.clippedCoverage(ch)
	if gap := firstGap(ch, cov, 0, end); gap >= 0 {
		return 0, &GapError{Position: gap}
	}
	return r.stateAt(ch, cov, end), nil
}

func encode(v any) string {
	switch x := v.(type) {
	case pair:
		return "t" + itoa(x.tli) + "@p" + itoa(x.pos)
	case string:
		return x
	default:
		return stringEncode(v)
	}
}

func stringEncode(v any) string {
	switch x := v.(type) {
	case []byte:
		return string(x)
	case int:
		return itoa(int64(x))
	case int64:
		return itoa(x)
	}
	return ""
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// Recover plans and executes a recovery, then registers a new timeline whose
// parent is the target timeline and whose fork is the first not-yet-replayed
// position (the exclusive replay end).
func (r *Registry) Recover(ctx context.Context, targetTLI TimelineID, target Position, inclusive bool) (Result, error) {
	// Plan under the read lock, then upgrade atomically for execution and
	// timeline allocation so concurrent recoveries get distinct, consecutive
	// IDs.
	plan, err := r.PlanRecovery(ctx, targetTLI, target, inclusive)
	if err != nil {
		return Result{}, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-validate under the write lock: the catalog may have changed while
	// waiting for the lock. Errors must not mutate anything.
	ch, ok := r.chain(targetTLI)
	if !ok {
		return Result{}, ErrTimelineNotFound
	}
	cov, archivedEnd := r.clippedCoverage(ch)
	if plan.End > archivedEnd {
		return Result{}, ErrTargetBeyondArchive
	}
	backup, ok := chooseBackup(ch, r.backups, plan.End)
	if !ok || backup.ID != plan.Backup.ID || backup.Upper != plan.Backup.Upper || backup.TLI != plan.Backup.TLI {
		return Result{}, ErrNoBackup
	}
	if gap := firstGap(ch, cov, backup.Upper, plan.End); gap >= 0 {
		return Result{}, &GapError{Position: gap}
	}
	plan = Plan{
		TargetTLI: targetTLI, Target: target, Inclusive: inclusive,
		End: plan.End, Backup: backup,
		Steps: buildSteps(ch, cov, backup.Upper, plan.End),
	}

	state := r.restoreAndReplay(ch, plan)

	newID := r.maxTLI + 1
	r.timelines[newID] = Timeline{ID: newID, Parent: targetTLI, Fork: plan.End}
	r.maxTLI = newID

	r.emit("recover_ok", map[string]any{
		"new_tli": newID, "parent": targetTLI, "fork": plan.End, "state": state,
	})
	return Result{
		NewTLI: newID, Parent: targetTLI, Fork: plan.End, Plan: plan, State: state,
	}, nil
}
