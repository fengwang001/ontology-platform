package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

func ch(k string, v, n int64) api.Change { return api.Change{Key: k, Ver: v, Val: n} }
func bruteForce(cs []api.Change) []api.Record {
	w := map[string]struct{ v, sn, val int64 }{}
	for i, c := range cs {
		sn := int64(i + 1)
		if x, ok := w[c.Key]; !ok || c.Ver > x.v || c.Ver == x.v && sn > x.sn {
			w[c.Key] = struct{ v, sn, val int64 }{c.Ver, sn, c.Val}
		}
	}
	ks := make([]string, 0, len(w))
	for k := range w {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	out := make([]api.Record, 0, len(ks))
	for _, k := range ks {
		out = append(out, api.Record{Key: k, Val: w[k].val})
	}
	return out
}

var mixed = []api.Change{
	ch("a", 10, 100), ch("b", 5, 50), ch("a", 10, 200), ch("c", 7, 70),
	ch("a", 5, 50), ch("b", 12, 90), ch("c", 7, 77), ch("a", 11, 1),
}

func TestReplayMatchesReference(t *testing.T) {
	for _, cut := range [][]int{{8}, {1, 1, 1, 1, 1, 1, 1, 1}, {3, 2, 3}} {
		e, i := api.New(100), 0
		for _, n := range cut {
			if err := e.Feed(mixed[i : i+n]); err != nil {
				t.Fatalf("cut=%v: %v", cut, err)
			}
			i += n
		}
		if got := e.Replay(); !reflect.DeepEqual(got, bruteForce(mixed)) {
			t.Fatalf("cut=%v Replay=%v, want %v", cut, got, bruteForce(mixed))
		}
	}
}
func TestWinnerStepwise(t *testing.T) {
	e := api.New(100)
	want := [][3]int64{{100, 0, 0}, {100, 50, 0}, {200, 50, 0}, {200, 50, 70}, {200, 50, 70}, {200, 90, 70}, {200, 90, 77}}
	for i, c := range mixed[:7] {
		if err := e.Feed([]api.Change{c}); err != nil {
			t.Fatalf("step %d: %v", i+1, err)
		}
		var got [3]int64
		for _, r := range e.Replay() {
			got[r.Key[0]-'a'] = r.Val
		}
		if got != want[i] {
			t.Fatalf("step %d winners=%v, want %v", i+1, got, want[i])
		}
	}
}
func TestReplayDeterministic(t *testing.T) {
	e := api.New(100)
	if err := e.Feed(mixed); err != nil {
		t.Fatal(err)
	}
	if r := e.Replay(); !reflect.DeepEqual(r, e.Replay()) || fmt.Sprint(r) != fmt.Sprint(e.Replay()) {
		t.Fatal("Replay not deterministic or state-changing")
	}
	exp := map[string][]api.Change{}
	for _, c := range mixed {
		exp[c.Key] = append(exp[c.Key], c)
	}
	for k, h := range exp {
		if !reflect.DeepEqual(e.History(k), h) {
			t.Fatalf("History(%q) not SN-ascending: %v want %v", k, e.History(k), h)
		}
	}
}
func TestFeedRejectionAtomic(t *testing.T) {
	if errors.Is(api.ErrEmptyKey, api.ErrNegativeVer) || errors.Is(api.ErrNegativeVer, api.ErrHistoryOverflow) ||
		errors.Is(api.ErrEmptyKey, api.ErrHistoryOverflow) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	bads := [][]api.Change{
		{ch("a", 1, 1), ch("", 1, 1)},
		{ch("a", -1, 1)},
		{ch("a", 3, 3), ch("a", 4, 4)},
	}
	wants := []error{api.ErrEmptyKey, api.ErrNegativeVer, api.ErrHistoryOverflow}
	for i, bad := range bads {
		e := api.New(2)
		if err := e.Feed([]api.Change{ch("a", 1, 1), ch("b", 2, 2)}); err != nil {
			t.Fatal(err)
		}
		before := fmt.Sprint(e.Replay())
		if !errors.Is(e.Feed(bad), wants[i]) {
			t.Fatalf("case %d: want %v", i, wants[i])
		}
		if fmt.Sprint(e.Replay()) != before || len(e.History("a")) != 1 {
			t.Fatal("rejected batch left a trace")
		}
		if err := e.Feed([]api.Change{ch("b", 9, 9)}); err != nil {
			t.Fatalf("unusable after rejection: %v", err)
		}
		if h := e.History("b"); len(h) != 2 || h[0].Ver != 2 || h[1].Ver != 9 {
			t.Fatalf("post-reject history not SN-continuous: %+v", h)
		}
	}
}
func TestConcurrentReadsEqual(t *testing.T) {
	e := api.New(1000)
	for range 10 {
		if err := e.Feed(mixed); err != nil {
			t.Fatal(err)
		}
	}
	wantR, wantH, n := e.Replay(), e.History("a"), 32
	start, div, wg := make(chan struct{}), make([]bool, n), sync.WaitGroup{}
	for g := range n {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			if !reflect.DeepEqual(e.Replay(), wantR) || !reflect.DeepEqual(e.History("a"), wantH) {
				div[g] = true
			}
		}(g)
	}
	close(start)
	wg.Wait()
	for g, d := range div {
		if d {
			t.Fatalf("reader %d diverged", g)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	if err := api.New(1).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
