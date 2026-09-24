package api

import (
	"math/rand"
	"ontology/ev"
	"ontology/fold"
	"reflect"
	"sync"
	"testing"
)

var sixS = []ev.Event{mk("g", 7, ev.OpInsert), mk("g", 7, ev.OpInsert), mk("g", 7, ev.OpRetract), mk("g", 7, ev.OpRetract), mk("g", 7, ev.OpInsert), mk("g", 3, ev.OpInsert)}
var tInits = [3]fold.State{nil, {}, {"g": {7: 2, 3: 1}, "h": {5: 2}}}

func mk(k string, v int64, o ev.Op) ev.Event { return ev.Event{Key: k, Val: v, Op: o} }
func ok(t *testing.T, c bool, m string, a ...any) {
	t.Helper()
	if !c {
		t.Fatalf(m, a...)
	}
}

func check(t *testing.T, mode int) {
	rng := rand.New(rand.NewSource(11))
	ss := [][]ev.Event{sixS, nil, {mk("g", 7, ev.OpRetract), mk("g", 7, ev.OpInsert)}}
	for _, n := range [4]int{1, 3, 17, 64} {
		ss = append(ss, fold.GenLegal(rng, n))
	}
	inits := tInits
	if mode == 2 { // the Retract-Insert stream needs a live 7 to stay legal
		inits = [3]fold.State{{"g": {7: 1}}, {"g": {7: 1}}, {"g": {7: 1}}}
	}
	for idx, s := range ss {
		c, e := fold.Compact(s)
		ok(t, e == nil, "compact: %v", e)
		for _, in := range inits {
			switch mode {
			case 0:
				ok(t, reflect.DeepEqual(fold.Terminal(in, s), fold.Terminal(in, c)), "equiv: s=%v c=%v", s, c)
			case 1:
				c2, _ := fold.Compact(c)
				ok(t, len(c) <= len(s) && reflect.DeepEqual(c, c2), "growth/non-idempotent: %v", c)
			case 2:
				ok(t, fold.Terminal(in, c) != nil, "legality lost: in=%v c=%v", in, c)
				if idx == 2 {
					ok(t, reflect.DeepEqual(c, s), "Retract-Insert pair must not be cancelled: %v", c)
				}
			}
		}
	}
}
func TestCompact_TerminalEquivalence(t *testing.T)  { check(t, 0) }
func TestCompact_NoGrowthIdempotent(t *testing.T)   { check(t, 1) }
func TestCompact_LegalityPreservation(t *testing.T) { check(t, 2) }
func TestCompact_InvalidAtomic(t *testing.T) {
	i1, r1 := mk("g", 1, ev.OpInsert), mk("g", 1, ev.OpRetract)
	ok(t, ev.AreInverse(i1, r1) && ev.AreInverse(r1, i1) && !ev.AreInverse(i1, mk("h", 1, ev.OpRetract)), "AreInverse wrong")
	bad := ev.Event{Key: "g", Op: ev.Op(7)}
	for _, s := range [][]ev.Event{{{Key: "", Op: ev.OpInsert}}, {i1, bad}, {bad, i1}} {
		out, e := fold.Compact(s)
		ok(t, out == nil && e == ev.ErrInvalidEvent, "partial result after rejection: %v", out)
	}
}
func TestAPI_Errors(t *testing.T) {
	I := ev.OpInsert
	bad := [][]ev.Event{{mk("g", 1, I), mk("g", 2, I), mk("g", 3, I)}, {{Key: "", Op: I}}, {{Key: "g", Op: ev.Op(3)}}, {mk("g", 1, ev.OpRetract)}, {mk("g", 1, I), mk("g", 2, ev.OpRetract)}}
	want := []error{ErrTooLong, ev.ErrInvalidEvent, ev.ErrInvalidEvent, ErrIllegalRetract, ErrIllegalRetract}
	for i := range bad {
		out, e := New(2).Replay(nil, bad[i])
		ok(t, e == want[i] && out == nil, "case %d: want %v got %v", i, want[i], e)
	}
	ok(t, ErrTooLong != ErrIllegalRetract && ErrIllegalRetract != ev.ErrInvalidEvent, "sentinels not distinct")
}
func TestAPI_RejectedStillUsable(t *testing.T) {
	c, in := New(10), fold.State{"g": {1: 1}}
	_, e0 := c.Replay(in, []ev.Event{mk("g", 9, ev.OpRetract)})
	got, e := c.Replay(in, []ev.Event{mk("g", 2, ev.OpInsert)})
	ok(t, e0 == ErrIllegalRetract && e == nil && reflect.DeepEqual(got, fold.State{"g": {1: 1, 2: 1}}) && in["g"][1] == 1, "rejection left traces: %v %v", e0, got)
}
func TestSelfCheck(t *testing.T) { ok(t, New(1000).SelfCheck(), "built-in SelfCheck failed") }
func TestCompact_ComparisonBound(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	for _, n := range [6]int{100, 500, 1000, 2500, 5000, 10000} {
		_, e := fold.Compact(fold.GenLegal(rng, n))
		ok(t, e == nil && fold.Compared() <= 2*int64(n), "n=%d cmp=%d", n, fold.Compared())
	}
}
func TestCompact_Concurrent(t *testing.T) {
	var wg sync.WaitGroup
	res, c := make([][]ev.Event, 32), New(100)
	for g := range res {
		wg.Add(2)
		go func(g int) { defer wg.Done(); res[g], _ = fold.Compact(sixS) }(g)
		go func() { defer wg.Done(); _, _ = c.Replay(nil, sixS) }()
	}
	wg.Wait()
	for g := 1; g < len(res); g++ {
		ok(t, reflect.DeepEqual(res[0], res[g]), "goroutine %d got a different compaction", g)
	}
}
