// Package reclaim tracks shadowed versions and reclaims them incrementally.
//
// A committed version becomes a candidate the moment a newer committed
// version lands above it in its chain. Candidates live in a min-heap keyed
// by commit transaction, so a reclaim pass examines at most
// (reclaimed + 1) candidates and never scans all keys.
package reclaim

import (
	"container/heap"

	"ontology/snapshot"
	"ontology/txid"
)

// Candidate is a shadowed committed version.
type Candidate struct {
	Key   string
	Tx    txid.ID // commit transaction of the shadowed version
	Above txid.ID // commit transaction of the nearest committed version above
}

type item struct {
	c   Candidate
	idx int
}

type queue []*item

func (q queue) Len() int           { return len(q) }
func (q queue) Less(i, j int) bool { return q[i].c.Tx < q[j].c.Tx }
func (q queue) Swap(i, j int)      { q[i], q[j] = q[j], q[i]; q[i].idx, q[j].idx = i, j }
func (q *queue) Push(x any)        { it := x.(*item); it.idx = len(*q); *q = append(*q, it) }
func (q *queue) Pop() any {
	old := *q
	it := old[len(old)-1]
	*q = old[:len(old)-1]
	return it
}

// Reclaimer is not safe for concurrent use; callers must serialize.
type Reclaimer struct {
	h         queue
	pos       map[string]map[txid.ID]*item
	watermark txid.ID
	examined  int
}

// New returns an empty Reclaimer.
func New() *Reclaimer {
	return &Reclaimer{pos: map[string]map[txid.ID]*item{}}
}

// Add registers a candidate, or lowers the Above of an existing one.
func (r *Reclaimer) Add(c Candidate) {
	if byTx, ok := r.pos[c.Key]; ok {
		if it, ok := byTx[c.Tx]; ok {
			if c.Above < it.c.Above {
				it.c.Above = c.Above
				heap.Fix(&r.h, it.idx)
			}
			return
		}
	}
	it := &item{c: c}
	heap.Push(&r.h, it)
	if r.pos[c.Key] == nil {
		r.pos[c.Key] = map[txid.ID]*item{}
	}
	r.pos[c.Key][c.Tx] = it
}

// RemoveKey drops every candidate of key (used by crash recovery before
// the key's candidates are re-derived from its chain).
func (r *Reclaimer) RemoveKey(key string) {
	for _, it := range r.pos[key] {
		heap.Remove(&r.h, it.idx)
	}
	delete(r.pos, key)
}

// reclaimable reports whether no open snapshot can still select c.
func reclaimable(c Candidate, snaps []*snapshot.Snapshot) bool {
	for _, s := range snaps {
		if s.Eligible(c.Tx) && !s.Eligible(c.Above) {
			return false
		}
	}
	return true
}

// Reclaim pops every reclaimable candidate in commit order, calling drop
// for each, and advances the watermark. It returns the number of
// candidates examined during this pass.
func (r *Reclaimer) Reclaim(snaps []*snapshot.Snapshot, drop func(Candidate)) int {
	examined := 0
	for len(r.h) > 0 {
		c := r.h[0].c
		examined++
		if !reclaimable(c, snaps) {
			break
		}
		heap.Pop(&r.h)
		delete(r.pos[c.Key], c.Tx)
		if c.Tx > r.watermark {
			r.watermark = c.Tx
		}
		drop(c)
	}
	r.examined += examined
	return examined
}

// Watermark returns the highest commit transaction ever reclaimed.
// It never decreases.
func (r *Reclaimer) Watermark() txid.ID { return r.watermark }

// Examined returns the total number of candidates examined so far.
func (r *Reclaimer) Examined() int { return r.examined }

// Len returns the number of pending candidates.
func (r *Reclaimer) Len() int { return len(r.h) }
