// Package reclaim advances the garbage-collection water mark and tracks
// reclaimable version candidates incrementally.
//
// A version becomes a reclaim *candidate* the instant it stops being a chain
// tip: a newer version on the same key then shadows it. Candidates live in a
// min-heap ordered by the creator of the shadowing ("cover") version. A
// reclaim run only inspects candidates whose cover sits behind the oldest
// open snapshot; keys that were never updated more than once never enter the
// heap, so the work is independent of the total number of keys.
package reclaim

import "ontology/txid"

// candidate records that version Shadowed on Key was shadowed by version
// Cover. Once Cover is visible to every open snapshot, Shadowed (and
// everything older on the chain) is dead to every reader.
type candidate struct {
	key      string
	shadowed txid.TxID
	cover    txid.TxID
}

// candidateHeap is a min-heap ordered by cover, then shadowed, then key for
// determinism.
type candidateHeap []candidate

func (h candidateHeap) Len() int { return len(h) }

func (h candidateHeap) Less(i, j int) bool {
	if h[i].cover != h[j].cover {
		return h[i].cover < h[j].cover
	}
	if h[i].shadowed != h[j].shadowed {
		return h[i].shadowed < h[j].shadowed
	}
	return h[i].key < h[j].key
}

func (h candidateHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *candidateHeap) Push(x any) { *h = append(*h, x.(candidate)) }

func (h *candidateHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	*h = old[:n-1]
	return item
}
