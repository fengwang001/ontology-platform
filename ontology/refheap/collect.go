package refheap

import "sort"

// Collect performs one atomic, fixed-order collection and returns its outcome.
//
//  1. Strong marking from the root set and objects waiting in the
//     finalization queue; fields of ordinary objects propagate the mark,
//     reference objects are marked but never traverse to their target.
//  2. Soft references marked at the end of phase 1 are processed in ascending
//     id order; free = C-used0 is computed once at phase start and a target is
//     retained iff now-timestamp <= floor(free/U)*M, otherwise cleared and
//     enqueued. Retained targets propagate marks through ordinary fields.
//  3. Marked weak references with unmarked targets clear and enqueue them.
//  4. Every unmarked, finalizable, not-yet-queued ordinary object is selected
//     simultaneously (ascending ids appended to the finalization queue), then
//     the whole selection and everything field-reachable is resurrected.
//  5. Any still-marked reference whose target is unmarked clears/enqueues it.
//  6. Sweep: unmarked objects are reclaimed and used drops by their sizes.
func (h *Heap) Collect(now int64) (CollectResult, error) {
	if !validNow(now) {
		return CollectResult{}, &HeapError{Code: ErrInvalidArg}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if now < h.maxNow {
		return CollectResult{}, &HeapError{Code: ErrClockBackward}
	}
	h.maxNow = now

	marked := make(map[int64]bool, len(h.objs))
	hits := 0
	var work []int64
	mark := func(id int64) {
		if id == 0 || marked[id] {
			return
		}
		if _, ok := h.objs[id]; !ok {
			return
		}
		marked[id] = true
		hits++
		work = append(work, id)
	}

	// drain propagates marks through fields of queued ordinary objects;
	// every object enters the work slice at most once thanks to mark().
	drain := func() {
		for head := 0; head < len(work); head++ {
			obj := h.objs[work[head]]
			if obj.isRef {
				continue
			}
			for _, f := range obj.fields {
				mark(f)
			}
		}
	}

	// Phase 1.
	for id := range h.roots {
		mark(id)
	}
	for _, id := range h.finalQueue {
		mark(id)
	}
	drain()

	markedRefIDs := func(k RefKind) []int64 {
		ids := make([]int64, 0)
		for id := range marked {
			if o := h.objs[id]; o.isRef && (k == 0 || o.kind == k) {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		return ids
	}
	enqueue := func(r *object) {
		if r.queue != 0 {
			h.queues[r.queue] = append(h.queues[r.queue], r.id)
		}
	}

	res := CollectResult{Reclaimed: []int64{}, SoftCleared: []int64{}, WeakCleared: []int64{}, CatchAll: []int64{}, Finalized: []int64{}}

	// used0 and free are frozen at the start of the soft phase.
	var used0 int64
	for id := range marked {
		used0 += h.objs[id].size
	}

	// Phase 2: candidate set is a snapshot of marked soft references, and
	// free never shrinks as targets get retained within this phase.
	softIDs := markedRefIDs(Soft)
	free := h.capacity - used0
	window := (free / h.softUnit) * h.softKeep
	for _, id := range softIDs {
		r := h.objs[id]
		if r.target == 0 || marked[r.target] {
			continue
		}
		if now-r.timestamp <= window {
			mark(r.target)
			drain()
		} else {
			r.target = 0
			enqueue(r)
			res.SoftCleared = append(res.SoftCleared, id)
		}
	}

	// Phase 3.
	for _, id := range markedRefIDs(Weak) {
		r := h.objs[id]
		if r.target != 0 && !marked[r.target] {
			r.target = 0
			enqueue(r)
			res.WeakCleared = append(res.WeakCleared, id)
		}
	}

	// Phase 4: the selection is computed in one pass before any resurrection,
	// so a finalizable object referenced by another selected object is chosen
	// in the same round regardless of scan order.
	var selected []int64
	for id, obj := range h.objs {
		if !marked[id] && obj.finalizable && !obj.isRef {
			if _, queued := h.finalQueued[id]; !queued {
				selected = append(selected, id)
			}
		}
	}
	sort.Slice(selected, func(a, b int) bool { return selected[a] < selected[b] })
	for _, id := range selected {
		h.finalQueue = append(h.finalQueue, id)
		h.finalQueued[id] = struct{}{}
		mark(id)
	}
	drain()
	res.Finalized = selected

	// Phase 5: catch-all for references of every kind.
	for _, id := range markedRefIDs(0) {
		r := h.objs[id]
		if r.target != 0 && !marked[r.target] {
			r.target = 0
			enqueue(r)
			res.CatchAll = append(res.CatchAll, id)
		}
	}

	// Phase 6.
	var reclaimed []int64
	for id := range h.objs {
		if !marked[id] {
			reclaimed = append(reclaimed, id)
		}
	}
	sort.Slice(reclaimed, func(a, b int) bool { return reclaimed[a] < reclaimed[b] })
	for _, id := range reclaimed {
		h.used -= h.objs[id].size
		delete(h.objs, id)
		delete(h.roots, id)
		// A reclaimed object can never still sit in finalQueued: pending
		// finalization objects are phase-1 roots; freshly selected ones were
		// marked in phase 4.
	}
	res.Reclaimed = reclaimed
	res.Used = h.used
	h.lastHits = hits
	return res, nil
}
