package api

import (
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/rc"
	"ontology/txlog"
)

var stepHW = []int{2, 4, 6, 7, 9, 10, 11}
var stepLSO = []int{0, 1, 1, 4, 4, 8, 11}
var stepOut = [][]string{{}, {"a"}, {}, {"c"}, {}, {"d", "f"}, {}}
var wantVals = []string{"a", "c", "d", "f"}

func eq(x, y []string) bool      { return strings.Join(x, ",") == strings.Join(y, ",") }
func da(a *API, p int, v string) { a.AppendData(p, v) }
func s1(a *API)                  { da(a, 1, "a"); da(a, 2, "b"); da(a, 1, "c"); a.AppendCommit(1) }
func s2(a *API)                  { da(a, 3, "d"); da(a, 2, "e"); a.AppendAbort(2); da(a, 3, "f") }
func s3(a *API)                  { da(a, 1, "g"); a.AppendCommit(3); a.AppendAbort(1) }
func seq3(a *API)                { s1(a); s2(a); s3(a) }
func must(t *testing.T, ok bool, f string, a ...any) {
	if !ok {
		t.Fatalf(f, a...)
	}
}
func replay(a *API) (out [][]string, lso []int, st []string) {
	from := 0
	for _, h := range stepHW {
		a.AdvanceHW(h)
		lso = append(lso, a.LSO())
		v, nx, _ := a.Fetch(from)
		from, st, out = nx, append(st, v...), append(out, v)
	}
	return
}
func randStream(seed int64) bool {
	a, r := New(), rand.New(rand.NewSource(seed))
	mk := []func(int) (int, error){a.AppendCommit, a.AppendAbort}
	var opn [5]bool
	end, hw, cur := 0, 0, 0
	var st []string
	add := func(p int) { a.AppendData(p, "x"); opn[p], end = true, end+1 }
	fin := func(p int) { mk[r.Intn(2)](p); opn[p], end = false, end+1 }
	for k := 0; k < 600; k++ {
		p := 1 + r.Intn(4)
		if r.Intn(2) == 0 {
			add(p)
		} else if opn[p] {
			fin(p)
		}
		hw += r.Intn(end - hw + 1)
		a.AdvanceHW(hw)
		v, nx, _ := a.Fetch(cur)
		cur, st = nx, append(st, v...)
	}
	b, _, _ := a.Fetch(0)
	return eq(st, b)
}
func TestFetchVisibility(t *testing.T) {
	a := New()
	seq3(a)
	out, _, _ := replay(a)
	for i := range out {
		must(t, eq(out[i], stepOut[i]), "step %d: %v", i, out[i])
	}
	b, _, _ := a.Fetch(0)
	must(t, eq(b, wantVals), "full scan = %v", b)
}
func TestStreamingMatchesBatch(t *testing.T) {
	a := New()
	seq3(a)
	_, _, st := replay(a)
	b, _, _ := a.Fetch(0)
	must(t, eq(st, b) && eq(b, wantVals), "fixed: %v vs %v", st, b)
	for _, seed := range []int64{1, 2, 3} {
		must(t, randStream(seed), "seed %d stream != batch", seed)
	}
}
func TestLSOMonotonic(t *testing.T) {
	a := New()
	seq3(a)
	_, lso, _ := replay(a)
	for i := range lso {
		must(t, lso[i] <= stepHW[i] && lso[i] == stepLSO[i] && (i == 0 || lso[i] >= lso[i-1]), "step %d LSO %d", i, lso[i])
	}
	must(t, a.AdvanceHW(11) == nil && a.LSO() == 11, "equal-HW no-op changed LSO")
}
func TestRejectedOpsNoTrace(t *testing.T) {
	a := New()
	a.AppendData(5, "keep")
	a.AdvanceHW(1) // non-empty state: HW=1, pid 5 unresolved, LSO=0
	do := []func() error{
		func() error { _, e := a.AppendData(0, "x"); return e },
		func() error { _, e := a.AppendCommit(7); return e },
		func() error { return a.AdvanceHW(-1) },
		func() error { _, _, e := a.Fetch(-1); return e },
	}
	want := []error{txlog.ErrInvalidRecord, txlog.ErrNoActiveTransaction, txlog.ErrHWOutOfRange, rc.ErrInvalidFrom}
	must(t, len(map[error]bool{want[0]: true, want[1]: true, want[2]: true, want[3]: true}) == 4, "sentinels must be distinct")
	for i, f := range do {
		err := f()
		v, _, _ := a.Fetch(0)
		must(t, err == want[i] && a.HW() == 1 && a.LSO() == 0 && len(v) == 0, "case %d: %v left a trace", i, err)
	}
	_, e := a.AppendCommit(5)
	must(t, e == nil, "pid 5 txn must remain usable after rejections: %v", e)
}
func consume(w *API, bad *atomic.Bool) {
	cur, prev, got := 0, 0, []string{}
	for cur < 11 {
		l := w.LSO()
		v, nx, e := w.Fetch(cur)
		if l < prev || e != nil {
			bad.Store(true)
			return
		}
		prev, cur, got = l, nx, append(got, v...)
	}
	bad.CompareAndSwap(false, !eq(got, wantVals))
}
func TestConcurrentConsumers(t *testing.T) {
	for _, n := range []int{2, 4, 8} {
		w := New()
		var wg sync.WaitGroup
		var bad atomic.Bool
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); consume(w, &bad) }()
		}
		seq3(w)
		for _, h := range stepHW {
			w.AdvanceHW(h)
		}
		wg.Wait()
		must(t, !bad.Load(), "n=%d mismatch or LSO regressed", n)
	}
}
