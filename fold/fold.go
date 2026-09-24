// Package fold rewrites a change stream into a shorter equivalent stream.
package fold

import (
	"maps"
	"math/rand"
	"sync/atomic"

	"ontology/ev"
)

// lastCmp counts pairs compared by the latest Compact; unexported.
var lastCmp atomic.Int64

// State is the multiset of live values per group key.
type State = map[string]map[int64]int

// Compact returns a no-longer, terminal-equivalent stream keeping every legal
// prefix legal. Only a Retract cancels the surviving top Insert (LIFO); an
// Insert never cancels a Retract: that could whitewash an illegal one (NOTES).
func Compact(stream []ev.Event) ([]ev.Event, error) {
	var cmp int64
	st := make([]ev.Event, 0, len(stream))
	for _, e := range stream {
		if !e.Valid() {
			return nil, ev.ErrInvalidEvent // whole input rejected, nothing partial
		}
		if e.Op == ev.OpRetract && len(st) > 0 {
			cmp++ // at most one comparison per event: single pass, O(n)
			if t := st[len(st)-1]; t.Op == ev.OpInsert && t.Key == e.Key && t.Val == e.Val {
				st = st[:len(st)-1]
				continue
			}
		}
		st = append(st, e)
	}
	lastCmp.Store(cmp)
	out := make([]ev.Event, len(st))
	copy(out, st)
	return out, nil
}

// Compared returns the event-pair comparison count of the latest Compact.
func Compared() int64 { return lastCmp.Load() }

// GenLegal builds a stream legal on the empty state (keys a-c, values 0-2),
// shared by table-driven tests and the demo.
func GenLegal(rng *rand.Rand, n int) []ev.Event {
	cur := map[string]map[int64]int{}
	s := make([]ev.Event, 0, n)
	for i := 0; i < n; i++ {
		k, v := string(rune('a'+rng.Intn(3))), int64(rng.Intn(3))
		if cur[k] == nil {
			cur[k] = map[int64]int{}
		}
		op := ev.OpInsert
		if cur[k][v] > 0 && rng.Intn(2) == 0 {
			op = ev.OpRetract
		}
		s = append(s, ev.Event{Key: k, Val: v, Op: op})
		if op == ev.OpRetract {
			cur[k][v]--
		} else {
			cur[k][v]++
		}
	}
	return s
}

// Terminal applies s to a fresh copy of init and returns the final state.
// It returns nil as soon as an event retracts a value with no live copy,
// i.e. when s is illegal on init.
func Terminal(init State, s []ev.Event) State {
	st := State{}
	for k, m := range init {
		st[k] = maps.Clone(m) // deep copy: both levels independent
	}
	for _, e := range s {
		m, ok := st[e.Key]
		if !ok {
			m = map[int64]int{}
			st[e.Key] = m
		}
		if e.Op == ev.OpInsert {
			m[e.Val]++
			continue
		}
		if m[e.Val] == 0 {
			return nil
		}
		if m[e.Val]--; m[e.Val] == 0 {
			delete(m, e.Val)
		}
		if len(m) == 0 {
			delete(st, e.Key) // canonical form: no empty groups
		}
	}
	return st
}
