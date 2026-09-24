package cagg

import (
	"cmp"
	"math"
	"math/rand"
	"ontology/cwin"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

func mk(m, s, d int64, mo int) *Agg { sp, _ := cwin.New(m, s, d); return New(sp, mo) }
func gen(rng *rand.Rand, n, keys, span int) []Event {
	evs := make([]Event, n)
	for i := range evs {
		evs[i] = Event{Key: string(rune('a' + rng.Intn(keys))), TS: int64(rng.Intn(span) - span/2)}
	}
	return evs
}
func feed(sp cwin.Spec, maxOpen int, evs []Event) *Agg {
	a := New(sp, maxOpen)
	a.Feed(evs)
	a.Flush()
	return a
}
func srt(r []Out) []Out {
	slices.SortFunc(r, func(a, b Out) int { return cmp.Or(cmp.Compare(a.End, b.End), cmp.Compare(a.Key, b.Key)) })
	return r
}
func mono(t *testing.T, a *Agg) {
	last := map[winID]int{}
	for _, o := range a.All() {
		k := winID{o.Key, o.Start}
		if o.Count < last[k] {
			t.Fatalf("non-monotonic %v", o)
		}
		last[k] = o.Count
	}
}

// ref 独立重算：标量水位线判定接受，逐 (Key,S) 逐子窗口统计 [S,S+j*step) 内被接受事件数。
func ref(sp cwin.Spec, evs []Event) ([]Out, int) {
	ws := map[winID][]int{}
	hi, nacc := int64(math.MinInt64), 0
	for _, e := range evs {
		hi = max(hi, e.TS)
		if S := sp.Start(e.TS); hi-sp.Delay() < sp.EMin(S, e.TS) {
			id := winID{e.Key, S}
			if ws[id] == nil {
				ws[id] = make([]int, sp.Steps())
			}
			ws[id][int((e.TS-S)/sp.Step())]++
			nacc++
		}
	}
	var r []Out
	for g, bins := range ws {
		acc := 0
		for j := 1; j <= sp.Steps(); j++ {
			acc += bins[j-1]
			r = append(r, Out{Key: g.key, Start: g.S, End: sp.End(g.S, j), Count: acc})
		}
	}
	return srt(r), len(evs) - nacc
}
func TestBatchEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for _, p := range [][3]int64{{12, 4, 2}, {6, 2, 0}, {10, 5, 7}, {8, 8, 3}} {
		sp, _ := cwin.New(p[0], p[1], p[2])
		for tr := 0; tr < 15; tr++ {
			evs := gen(rng, 1+rng.Intn(60), 4, 80)
			a := feed(sp, 1000, evs)
			want, drop := ref(sp, evs)
			if !slices.Equal(srt(a.All()), want) || a.Dropped() != drop {
				t.Fatalf("p=%v tr=%d got %v %d want %v %d", p, tr, a.All(), a.Dropped(), want, drop)
			}
		}
	}
}
func TestMonotonic(t *testing.T) {
	sp, _ := cwin.New(12, 4, 2)
	mono(t, feed(sp, 1000, gen(rand.New(rand.NewSource(2)), 200, 3, 100)))
}
func TestNoEarlyTriggerAndWM(t *testing.T) {
	a := mk(12, 4, 2, 1000)
	hi := int64(math.MinInt64)
	for _, e := range gen(rand.New(rand.NewSource(3)), 300, 3, 120) {
		outs, _ := a.Feed([]Event{e})
		hi = max(hi, e.TS)
		if slices.IndexFunc(outs, func(o Out) bool { return o.End > hi-2 }) >= 0 {
			t.Fatalf("early trigger wm=%d", hi-2)
		}
	}
}
func TestFailureNoTrace(t *testing.T) {
	for _, c := range [][3]int64{{0, 1, 0}, {1, 0, 0}, {6, 4, 0}, {4, 4, -1}} {
		if _, e := cwin.New(c[0], c[1], c[2]); e != cwin.ErrInvalidParams {
			t.Fatalf("params %v: %v", c, e)
		}
	}
	if cwin.ErrInvalidParams == ErrEmptyKey || ErrEmptyKey == ErrTooManyOpenWindows || cwin.ErrInvalidParams == ErrTooManyOpenWindows {
		t.Fatal("sentinel errors not distinct")
	}
	a := mk(12, 4, 2, 2)
	a.Feed([]Event{{Key: "K", TS: 1}})
	if _, e := a.Feed([]Event{{Key: "K", TS: 2}, {Key: "", TS: 3}}); e != ErrEmptyKey {
		t.Fatalf("empty key: %v", e)
	}
	b := mk(12, 4, 100, 1)
	b.Feed([]Event{{Key: "K", TS: 1}})
	if _, e := b.Feed([]Event{{Key: "K", TS: 13}}); e != ErrTooManyOpenWindows {
		t.Fatalf("capacity: %v", e)
	}
	if len(a.All()) != 0 || a.Dropped() != 0 || len(b.All()) != 0 || b.Dropped() != 0 {
		t.Fatal("rejected batch left traces")
	}
}
func TestComplexityScansSublinear(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a, evs := mk(1<<30, 1<<29, 1<<40, m+1), make([]Event, m)
		for i := range evs {
			evs[i] = Event{Key: string(rune(i + 0x100)), TS: 1}
		}
		a.Feed(evs)                            // m 个未清除大窗口
		a.Feed([]Event{{Key: "probe", TS: 2}}) // wm 仅前进 1，不触发任何子窗口
		if s := a.scanCount(); s > 2 {         // 与 m 无关的小常数
			t.Fatalf("m=%d scanned %d windows", m, s)
		}
	}
}
func TestConcurrentReadOnly(t *testing.T) {
	sp, _ := cwin.New(12, 4, 2)
	a := feed(sp, 1000, gen(rand.New(rand.NewSource(4)), 60, 3, 100))
	base, d0 := a.All(), a.Dropped()
	var bad atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Go(func() {
			if !slices.Equal(a.All(), base) || a.Dropped() != d0 || a.SelfCheck() != nil {
				bad.Store(true)
			}
		})
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("concurrent reads differ")
	}
}
