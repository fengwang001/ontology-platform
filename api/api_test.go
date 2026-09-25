package api

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/merge"
	"ontology/msrc"
)

var srcData = map[string][]Event{
	"A": {{Seq: 0, TS: 5, Key: "k1", Val: "a1"}, {Seq: 1, TS: 7, Key: "k2", Val: "a2"}, {Seq: 2, TS: 9, Key: "k1", Val: "a3"}},
	"B": {{Seq: 0, TS: 5, Key: "k1", Val: "b1"}, {Seq: 1, TS: 8, Key: "k3", Val: "b2"}, {Seq: 2, TS: 9, Key: "k1", Val: "b3"}},
	"C": {{Seq: 0, TS: 6, Key: "k4", Val: "c1"}, {Seq: 1, TS: 7, Key: "k2", Val: "c2"}, {Seq: 2, TS: 10, Key: "k5", Val: "c3"}},
}

func newScenario(t *testing.T) *Pipeline {
	t.Helper()
	p := New()
	for _, n := range []string{"A", "B", "C"} {
		if err := p.AddSource(n, srcData[n]); err != nil {
			t.Fatalf("add %s: %v", n, err)
		}
	}
	return p
}
func ord(a, b Event) bool {
	return a.TS < b.TS || a.TS == b.TS && (a.Src < b.Src || a.Src == b.Src && a.Seq < b.Seq)
}
func eqMap(a, b map[string]string) bool { return reflect.DeepEqual(a, b) }
func keyOf(e Event) [2]any              { return [2]any{e.TS, e.Key} }
func batchView(all []Event) map[string]string {
	in := append([]Event(nil), all...)
	sort.Slice(in, func(i, j int) bool { return ord(in[i], in[j]) })
	seen, v := map[[2]any]bool{}, map[string]string{}
	for _, e := range in {
		k := keyOf(e)
		if !seen[k] {
			seen[k] = true
			v[e.Key] = e.Val
		}
	}
	return v
}
func allEvents() (a []Event) {
	for _, n := range []string{"A", "B", "C"} {
		a = append(a, srcData[n]...)
	}
	return a
}
func TestViewMatchesBatchRecompute(t *testing.T) {
	if got := newScenario(t).View(); !eqMap(got, batchView(allEvents())) {
		t.Fatalf("view %v != batch %v", got, batchView(allEvents()))
	}
}
func TestChangelogNoDuplicateKeyTS(t *testing.T) {
	log := newScenario(t).Drain()
	seen := map[[2]any]bool{}
	for i, e := range log {
		if i > 0 && ord(e, log[i-1]) {
			t.Fatal("change log not in ≺ order")
		}
		if seen[keyOf(e)] {
			t.Fatalf("duplicate (Key,TS) %s@%d", e.Key, e.TS)
		}
		seen[keyOf(e)] = true
	}
}
func TestDedupWinnersAndCount(t *testing.T) {
	p := newScenario(t)
	log := p.Drain()
	if p.Dups() != 3 || len(log) != 6 {
		t.Fatalf("dups=%d kept=%d, want 3/6", p.Dups(), len(log))
	}
	want := map[string]string{"k1": "a3", "k2": "a2", "k3": "b2", "k4": "c1", "k5": "c3"}
	if !eqMap(p.View(), want) {
		t.Fatalf("view=%v want=%v", p.View(), want)
	}
	winners := map[[2]any]string{}
	for _, e := range log {
		winners[keyOf(e)] = e.Src
	}
	for _, k := range [][2]any{{int64(5), "k1"}, {int64(7), "k2"}, {int64(9), "k1"}} {
		if winners[k] != "A" {
			t.Fatalf("winner %v=%s want A", k, winners[k])
		}
	}
}
func TestRejectedAddLeavesNoTrace(t *testing.T) {
	sentinels := []error{msrc.ErrSeqNotStrict, msrc.ErrTSDecreased, msrc.ErrEmptyKey, merge.ErrDupSource}
	seen := map[string]bool{}
	for _, e := range sentinels {
		if seen[e.Error()] {
			t.Fatal("sentinel errors must be pairwise distinct")
		}
		seen[e.Error()] = true
	}
	cases := []struct {
		name string
		evs  []Event
		want error
	}{
		{"seq", []Event{{Seq: 1, TS: 1, Key: "k"}, {Seq: 1, TS: 2, Key: "j"}}, msrc.ErrSeqNotStrict},
		{"ts", []Event{{Seq: 0, TS: 2, Key: "k"}, {Seq: 1, TS: 1, Key: "j"}}, msrc.ErrTSDecreased},
		{"key", []Event{{Seq: 0, TS: 1, Key: ""}}, msrc.ErrEmptyKey},
	}
	p := New()
	if err := p.AddSource("A", []Event{{Seq: 0, TS: 1, Key: "k", Val: "v"}}); err != nil {
		t.Fatal(err)
	}
	base := len(p.Drain())
	for _, tc := range cases {
		if err := p.AddSource(tc.name, tc.evs); !errors.Is(err, tc.want) || len(p.Drain()) != base || p.Dups() != 0 {
			t.Fatalf("%s rejected wrong or changed state: %v", tc.name, err)
		}
	}
	if err := p.AddSource("A", nil); !errors.Is(err, merge.ErrDupSource) || len(p.Drain()) != base {
		t.Fatal("dup-name accepted or changed state")
	}
	if err := p.AddSource("B", []Event{{Seq: 0, TS: 2, Key: "j", Val: "w"}}); err != nil || p.View()["j"] != "w" {
		t.Fatal("valid source after rejections did not merge")
	}
}
func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}
func TestConcurrentViewReaders(t *testing.T) {
	p, want := newScenario(t), newScenario(t).View()
	const n = 16
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if !eqMap(p.View(), want) || p.Dups() != 3 || p.SelfCheck() != nil {
					t.Error("concurrent reader saw inconsistent state")
					return
				}
			}
		}()
	}
	wg.Wait()
}
