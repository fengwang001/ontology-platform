// Package wrs implements a single weighted reservoir: it keeps the k
// elements with the largest A-Res keys (key = U^(1/w)) seen so far.
package wrs

import (
	"math"
	"sort"
)

// Item is one upstream element: a value and a positive weight.
type Item struct {
	Val    string
	Weight float64
}

// Slot is a retained element together with its sort key.
type Slot struct {
	Item Item
	Key  float64
}

// Key computes the A-Res sort key U^(1/w). u must be in (0,1), w > 0.
func Key(u, w float64) float64 {
	return math.Pow(u, 1/w)
}

// Reservoir keeps the k slots with the largest keys.
type Reservoir struct {
	k     int
	slots []Slot
}

// New returns an empty reservoir of capacity k. k must be > 0.
func New(k int) *Reservoir {
	return &Reservoir{k: k, slots: make([]Slot, 0, k)}
}

// Len reports how many slots are currently occupied.
func (r *Reservoir) Len() int { return len(r.slots) }

// Consider offers it with the given key. While under capacity the item is
// admitted unconditionally; otherwise it replaces the current minimum-key
// slot iff its key is strictly larger, and is dropped otherwise.
func (r *Reservoir) Consider(it Item, key float64) {
	if len(r.slots) < r.k {
		r.slots = append(r.slots, Slot{Item: it, Key: key})
		return
	}
	m := r.minIdx()
	if key > r.slots[m].Key {
		r.slots[m] = Slot{Item: it, Key: key}
	}
}

// minIdx locates the slot holding the smallest key.
func (r *Reservoir) minIdx() int {
	m := 0
	for i := 1; i < len(r.slots); i++ {
		if r.slots[i].Key < r.slots[m].Key {
			m = i
		}
	}
	return m
}

// Items returns a copy of the retained slots sorted by key descending.
func (r *Reservoir) Items() []Slot {
	out := make([]Slot, len(r.slots))
	copy(out, r.slots)
	sort.Slice(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	return out
}
