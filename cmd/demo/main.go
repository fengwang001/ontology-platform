// Command demo exercises the multi-source CDC merge/dedup pipeline.
// It prints one OK/FAIL line per judgement and exits non-zero on failure.
package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/merge"
	"ontology/msrc"
	"os"
	"sort"
	"sync"
)

func report(name string, ok bool) bool {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
	return ok
}
func ev(src string, seq int, ts int64, key, val string) api.Event {
	return api.Event{Src: src, Seq: int64(seq), TS: ts, Key: key, Val: val}
}
func sources() map[string][]api.Event {
	return map[string][]api.Event{
		"A": {ev("A", 0, 5, "k1", "a1"), ev("A", 1, 7, "k2", "a2"), ev("A", 2, 9, "k1", "a3")},
		"B": {ev("B", 0, 5, "k1", "b1"), ev("B", 1, 8, "k3", "b2"), ev("B", 2, 9, "k1", "b3")},
		"C": {ev("C", 0, 6, "k4", "c1"), ev("C", 1, 7, "k2", "c2"), ev("C", 2, 10, "k5", "c3")},
	}
}
func less(a, b api.Event) bool {
	return a.TS < b.TS || a.TS == b.TS && (a.Src < b.Src || a.Src == b.Src && a.Seq < b.Seq)
}
func eqMap(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
func batch(all []api.Event) map[string]string {
	in := append([]api.Event(nil), all...)
	sort.Slice(in, func(i, j int) bool { return less(in[i], in[j]) })
	seen, v := map[[2]any]bool{}, map[string]string{}
	for _, e := range in {
		k := [2]any{e.TS, e.Key}
		if !seen[k] {
			seen[k] = true
			v[e.Key] = e.Val
		}
	}
	return v
}
func main() {
	allOK := true
	fail := func(n string, ok bool) {
		if !report(n, ok) {
			allOK = false
		}
	}
	// Judgement 1: four distinct, decidable sentinel errors.
	sentinels := []error{msrc.ErrSeqNotStrict, msrc.ErrTSDecreased, msrc.ErrEmptyKey, merge.ErrDupSource}
	distinct := true
	for i := range sentinels {
		for j := i + 1; j < len(sentinels); j++ {
			if errors.Is(sentinels[i], sentinels[j]) {
				distinct = false
			}
		}
	}
	bad := [][]api.Event{
		{{Seq: 1, TS: 1, Key: "k"}, {Seq: 1, TS: 2, Key: "j"}},
		{{Seq: 0, TS: 2, Key: "k"}, {Seq: 1, TS: 1, Key: "j"}},
		{{Seq: 0, TS: 1, Key: ""}},
	}
	sentinelOK := distinct
	probe := api.New()
	if probe.AddSource("A", []api.Event{{Seq: 0, TS: 1, Key: "k"}}) != nil {
		sentinelOK = false
	}
	for i, e := range bad {
		sentinelOK = sentinelOK && errors.Is(probe.AddSource(fmt.Sprintf("x%d", i), e), sentinels[i])
	}
	sentinelOK = sentinelOK && errors.Is(probe.AddSource("A", nil), merge.ErrDupSource)
	fail("four distinct decidable sentinel errors", sentinelOK)
	// Main nine-event scenario.
	p := api.New()
	added := true
	for _, n := range []string{"A", "B", "C"} {
		added = added && p.AddSource(n, sources()[n]) == nil
	}
	log := p.Drain()
	wantVals := []string{"a1", "c1", "a2", "b2", "a3", "c3"}
	orderOK := added && len(log) == len(wantVals)
	for i, e := range log {
		orderOK = orderOK && e.Val == wantVals[i]
	}
	wantView := map[string]string{"k1": "a3", "k2": "a2", "k3": "b2", "k4": "c1", "k5": "c3"}
	fail("nine events output/discard order + final view", orderOK && eqMap(p.View(), wantView))
	fail("duplicate count == 3", p.Dups() == 3)
	var all []api.Event
	for _, s := range sources() {
		all = append(all, s...)
	}
	fail("view == independent batch recompute", eqMap(p.View(), batch(all)))
	prefixOK := true
	seen := map[[2]any]bool{}
	for _, e := range log {
		k := [2]any{e.TS, e.Key}
		prefixOK = prefixOK && !seen[k]
		seen[k] = true
	}
	fail("changelog prefix self-consistent", prefixOK)
	// Rejected adds (including duplicate name) leave no trace.
	before := len(p.Drain())
	noTrace := errors.Is(p.AddSource("D", bad[0]), msrc.ErrSeqNotStrict) &&
		errors.Is(p.AddSource("E", bad[2]), msrc.ErrEmptyKey) &&
		errors.Is(p.AddSource("A", nil), merge.ErrDupSource) &&
		len(p.Drain()) == before && p.Dups() == 3
	fail("rejected add leaves no trace", noTrace)
	fail("min-head comparisons sublinear in m", merge.CheckMinHeadBound() == nil)
	// Concurrent read-only View calls must agree key-by-key. No sleeps.
	const n = 16
	var wg sync.WaitGroup
	concOK := true
	var mu sync.Mutex
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !eqMap(p.View(), wantView) {
				mu.Lock()
				concOK = false
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	fail("concurrent read-only views identical", concOK)
	if !allOK {
		os.Exit(1)
	}
}
