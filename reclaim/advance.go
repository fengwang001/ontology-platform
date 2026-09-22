package reclaim

import (
	"ontology/txid"
)

// Report describes one reclaim pass.
type Report struct {
	Watermark  txid.ID
	Candidates int // keys popped from the queue
	Examined   int // chain versions actually visited
}

// Advance fixes the watermark, then drains the candidate queue incrementally,
// invoking prune per key. maxKeys <= 0 drains the whole queue in this pass.
//
// The watermark is computed via Registry.ComputeWatermark in the same
// critical section as snapshot.Open, which is what guarantees a newly opened
// snapshot can never have versions reclaimed out from under it.
func (r *Reclaimer) Advance(prune PruneFunc, maxKeys int) Report {
	wm, _, err := r.reg.ComputeWatermark()
	if err != nil {
		return Report{Watermark: r.Watermark()}
	}

	r.mu.Lock()
	// Monotonic: the watermark only rises.
	if r.watermark.Less(wm) {
		r.watermark = wm
	}
	wm = r.watermark
	total := len(r.queue)
	if maxKeys > 0 && total > maxKeys {
		total = maxKeys
	}
	batch := r.queue[:total]
	r.queue = r.queue[total:]
	examined := 0
	for _, key := range batch {
		delete(r.queued, key)
	}
	r.mu.Unlock()

	// Prune outside r.mu: the callback takes the store lock and must not
	// hold the reclaimer lock (lock order: store -> reclaim, never reverse).
	for _, key := range batch {
		examined += prune(key, wm)
	}

	r.mu.Lock()
	r.examinedLast = examined
	r.examinedTotal += examined
	rep := Report{Watermark: wm, Candidates: len(batch), Examined: examined}
	r.mu.Unlock()
	return rep
}

// ForceNotify is like Notify but bypasses the dedup check; tests use it to
// simulate repeated interest without needing real commits.
func (r *Reclaimer) ForceNotify(key string) {
	r.mu.Lock()
	r.queue = append(r.queue, key)
	r.queued[key] = struct{}{}
	r.mu.Unlock()
}
