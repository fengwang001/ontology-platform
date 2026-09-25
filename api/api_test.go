package api_test

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

func less(a, b api.Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	if a.Src != b.Src {
		return a.Src < b.Src
	}
	return a.Seq < b.Seq
}

// gen builds nSrc sources (strictly increasing TS per source, colliding
// (Key,TS) across sources) and a shuffled registration order.
func gen(rnd *rand.Rand, nSrc, per int) (map[string][]api.Event, []string) {
	srcs, order := map[string][]api.Event{}, []string{}
	for s := 0; s < nSrc; s++ {
		name := fmt.Sprintf("s%03d", s)
		order = append(order, name)
		ts := rnd.Int63n(5)
		for i := 0; i < per; i++ {
			ts += 1 + rnd.Int63n(3)
			e := api.Event{Seq: int64(i), TS: ts, Key: fmt.Sprintf("k%d", rnd.Intn(per)), Val: fmt.Sprintf("%s-%d", name, i)}
			srcs[name] = append(srcs[name], e)
		}
	}
	rnd.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
	return srcs, order
}

// run registers the sources in the given order and drains the merge.
func run(t *testing.T, srcs map[string][]api.Event, order []string) (*api.System, []api.Event, int) {
	t.Helper()
	sys := api.New()
	for _, n := range order {
		if err := sys.AddSource(n, srcs[n]); err != nil {
			t.Fatal(err)
		}
	}
	return sys, sys.Drain(), sys.Dups()
}

// TestViewMatchesBatchRecompute: invariant 1 — View equals last-write-wins
// over the kept events sorted by ≺, across scales and arrival orders.
func TestViewMatchesBatchRecompute(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {3, 3}, {7, 20}, {30, 50}} {
		for seed := int64(0); seed < 5; seed++ {
			srcs, order := gen(rand.New(rand.NewSource(seed)), sz[0], sz[1])
			sys, log, _ := run(t, srcs, order)
			sorted := append([]api.Event(nil), log...)
			sort.Slice(sorted, func(i, j int) bool { return less(sorted[i], sorted[j]) })
			want := map[string]string{}
			for _, e := range sorted {
				want[e.Key] = e.Val
			}
			if fmt.Sprint(sys.View()) != fmt.Sprint(want) {
				t.Fatalf("size %v seed %d: view != batch recompute", sz, seed)
			}
		}
	}
}

// checkLog verifies invariant 2 and returns the distinct (Key,TS) count.
func checkLog(t *testing.T, log []api.Event) int {
	t.Helper()
	seen := map[[2]interface{}]bool{}
	for i, e := range log {
		kt := [2]interface{}{e.Key, e.TS}
		if seen[kt] || (i > 0 && !less(log[i-1], e)) {
			t.Fatalf("log not self-consistent at %d", i)
		}
		seen[kt] = true
	}
	return len(seen)
}

// TestLogSelfConsistent: invariant 2 — ≺-ordered log, unique (Key,TS).
func TestLogSelfConsistent(t *testing.T) {
	srcs, order := gen(rand.New(rand.NewSource(7)), 10, 30)
	_, log, _ := run(t, srcs, order)
	checkLog(t, log)
}

// TestDedupExact: invariant 3 — every (Key,TS) keeps exactly its ≺-minimum
// and dups equals the number of discarded events.
func TestDedupExact(t *testing.T) {
	srcs, order := gen(rand.New(rand.NewSource(9)), 12, 40)
	total, best := 0, map[[2]interface{}]api.Event{}
	for n, evs := range srcs {
		for _, e := range evs {
			e.Src, total = n, total+1
			kt := [2]interface{}{e.Key, e.TS}
			if cur, ok := best[kt]; !ok || less(e, cur) {
				best[kt] = e
			}
		}
	}
	_, log, dups := run(t, srcs, order)
	if checkLog(t, log) != len(best) || dups != total-len(best) {
		t.Fatalf("log=%d distinct=%d dups=%d total=%d", len(log), len(best), dups, total)
	}
	for _, e := range log {
		if best[[2]interface{}{e.Key, e.TS}] != e {
			t.Fatalf("kept non-minimal event %v", e)
		}
	}
}

// TestConcurrentViewReads: N goroutines read View concurrently; all equal.
func TestConcurrentViewReads(t *testing.T) {
	srcs, order := gen(rand.New(rand.NewSource(3)), 8, 25)
	sys, _, _ := run(t, srcs, order)
	want := fmt.Sprint(sys.View())
	start, views := make(chan struct{}), make([]string, 64)
	var wg sync.WaitGroup
	for i := range views {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; views[i] = fmt.Sprint(sys.View()) }(i)
	}
	close(start)
	wg.Wait()
	for _, v := range views {
		if v != want {
			t.Fatal("concurrent views differ")
		}
	}
}

// TestSelfCheck: the built-in self-check passes, also under concurrency.
func TestSelfCheck(t *testing.T) {
	sys, wg := api.New(), sync.WaitGroup{}
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = sys.SelfCheck() }()
	}
	wg.Wait()
	if err := sys.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
