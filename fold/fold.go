// Package fold compacts a change stream into a shorter, terminally
// equivalent stream. It depends only on package ev.
package fold

import (
	"fmt"
	"math/rand/v2"
	"sync/atomic"

	"ontology/ev"
)

// Compacter is safe for concurrent use. The only shared mutable part is the
// unexported comparison counter, which concurrent runs may pollute.
type Compacter struct {
	comparisons atomic.Uint64
}

// New returns a ready Compacter.
func New() *Compacter { return &Compacter{} }

// Compact rewrites stream by cancelling only surviving adjacent
// Insert→Retract pairs on the same Key/Val. A Retract whose copy is not
// supplied by the stream (it must be guaranteed by the initial state) is
// always kept. The whole stream is validated first, so an invalid event
// fails the call outright instead of returning a partial result.
func (c *Compacter) Compact(stream []ev.Event) ([]ev.Event, error) {
	for i := range stream {
		if err := stream[i].Validate(); err != nil {
			return nil, fmt.Errorf("fold: event %d: %w", i, err)
		}
	}
	type slot struct {
		key string
		val int64
	}
	stacks := make(map[slot][]int) // unmatched surviving Inserts per Key/Val
	alive := make([]bool, len(stream))
	for i, e := range stream {
		alive[i] = true
		s := slot{e.Key, e.Val}
		if e.Op == ev.Insert {
			stacks[s] = append(stacks[s], i)
			continue
		}
		// This event is compared with the current stack top at most once.
		c.comparisons.Add(1)
		if st := stacks[s]; len(st) > 0 {
			alive[st[len(st)-1]] = false
			alive[i] = false
			stacks[s] = st[:len(st)-1]
		} // empty stack: Retract survives, guaranteed by the initial state
	}
	out := make([]ev.Event, 0, len(stream))
	for i, e := range stream {
		if alive[i] {
			out = append(out, e)
		}
	}
	return out, nil
}

// LinearBound compacts one generated pseudo-random stream of length n and
// reports whether that run performed at most bound*n comparisons. Only the
// boolean verdict is exposed; the counter itself stays unexported.
func (f *Compacter) LinearBound(n, bound int) bool {
	rng := rand.New(rand.NewPCG(1, uint64(n)))
	keys := []string{"a", "b", "c", "d"}
	s := make([]ev.Event, n)
	for i := range s {
		s[i] = ev.Event{Key: keys[rng.IntN(len(keys))],
			Val: int64(rng.IntN(7)), Op: ev.Op(1 + rng.IntN(2))}
	}
	start := f.comparisons.Load()
	out, err := f.Compact(s)
	used := f.comparisons.Load() - start
	return err == nil && len(out) <= n && used <= uint64(bound)*uint64(n)
}
