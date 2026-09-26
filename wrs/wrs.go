// Package wrs implements a single weighted reservoir used by A-Res
// (Efraimidis–Spirakis) weighted sampling without replacement.
package wrs

import (
	"math"
	"sort"
)

// Slot is one retained element together with its positive weight and the
// ranking key that decided its retention.
type Slot struct {
	Val    string
	Weight float64
	Key    float64
}

// Key returns the ranking key u^(1/w) for u in the open interval (0,1) and a
// strictly positive weight w.
func Key(u, w float64) float64 { return math.Pow(u, 1.0/w) }

// Reservoir retains at most k of the offered elements: the k with the largest
// keys. It is not safe for concurrent use on its own.
type Reservoir struct {
	k     int
	slots []Slot
}

// New creates an empty reservoir of the given positive capacity.
func New(k int) *Reservoir {
	return &Reservoir{k: k, slots: make([]Slot, 0, k)}
}

// Offer presents one element with a precomputed ranking key. While fewer than
// k elements are held the offer is always retained. Afterwards it is retained
// only when its key is strictly larger than the smallest key held, which it
// replaces. It reports whether the element is retained and, on eviction, the
// value of the evicted element.
func (r *Reservoir) Offer(val string, w, key float64) (retained bool, evicted string) {
	if len(r.slots) < r.k {
		r.slots = append(r.slots, Slot{Val: val, Weight: w, Key: key})
		return true, ""
	}
	j := r.minIndex()
	if key <= r.slots[j].Key {
		return false, ""
	}
	evicted = r.slots[j].Val
	r.slots[j] = Slot{Val: val, Weight: w, Key: key}
	return true, evicted
}

// minIndex returns the index of the slot with the smallest key.
func (r *Reservoir) minIndex() int {
	j := 0
	for i := 1; i < len(r.slots); i++ {
		if r.slots[i].Key < r.slots[j].Key {
			j = i
		}
	}
	return j
}

// Len reports the number of currently retained elements, always <= k.
func (r *Reservoir) Len() int { return len(r.slots) }

// Snapshot returns a copy of the retained slots ordered by descending key,
// with ties broken by ascending value so the order is deterministic.
func (r *Reservoir) Snapshot() []Slot {
	out := append([]Slot(nil), r.slots...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key > out[j].Key
		}
		return out[i].Val < out[j].Val
	})
	return out
}
