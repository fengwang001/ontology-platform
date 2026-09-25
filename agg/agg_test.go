package agg

import (
	"math/rand"
	"reflect"
	"testing"
)

type ev struct {
	seq, val int64
}

func TestEightStep(t *testing.T) {
	a := New(3)
	steps := []struct {
		e          ev
		k          Kind
		oldS, newS int64
		chg        bool
		win        []int64
		stale      int64
	}{
		{ev{5, 10}, NewSeq, 0, 10, true, []int64{5}, 0},
		{ev{7, 20}, NewSeq, 10, 30, true, []int64{5, 7}, 0},
		{ev{6, 15}, Late, 30, 45, true, []int64{5, 7, 6}, 0},
		{ev{8, 5}, NewSeq, 45, 50, true, []int64{7, 6, 8}, 0},
		{ev{5, 99}, Stale, 50, 50, false, []int64{7, 6, 8}, 1},
		{ev{6, 15}, Duplicate, 50, 50, false, []int64{7, 6, 8}, 1},
		{ev{6, 30}, Correction, 50, 65, true, []int64{7, 6, 8}, 1},
		{ev{9, 3}, NewSeq, 65, 68, true, []int64{6, 8, 9}, 1},
	}
	for i, s := range steps {
		o := a.Apply(s.e.seq, s.e.val)
		if o.Kind != s.k || o.OldSum != s.oldS || o.NewSum != s.newS ||
			o.Changed != s.chg || a.Sum() != s.newS || a.Stale() != s.stale ||
			!reflect.DeepEqual(a.Window(), s.win) {
			t.Fatalf("step %d: got %+v sum=%d stale=%d win=%v, want kind=%d %d->%d chg=%v stale=%d win=%v",
				i+1, o, a.Sum(), a.Stale(), a.Window(), s.k, s.oldS, s.newS, s.chg, s.stale, s.win)
		}
	}
}

func TestBatchRecompute(t *testing.T) {
	sizes := []int{1, 2, 3, 7, 50, 500}
	for seed := int64(0); seed < 12; seed++ {
		r := rand.New(rand.NewSource(seed))
		for _, n := range sizes {
			w := 1 + r.Intn(5)
			a := New(w)
			// Naive independent model: map + linear-search FIFO, no
			// incremental sum. It enforces the same rejection rules and
			// recomputes the sum from scratch at the end.
			vals := map[int64]int64{}
			var order []int64
			inWin := func(seq int64) bool {
				for _, s := range order {
					if s == seq {
						return true
					}
				}
				return false
			}
			for j := 0; j < n; j++ {
				e := ev{int64(r.Intn(8)) + 1, r.Int63n(41) - 20}
				a.Apply(e.seq, e.val)
				if cur, seen := vals[e.seq]; seen {
					if cur == e.val || !inWin(e.seq) {
						continue // duplicate or expired correction
					}
					vals[e.seq] = e.val // in-window correction keeps order
				} else {
					vals[e.seq] = e.val
					order = append(order, e.seq)
					if len(order) > w {
						order = order[1:]
					}
				}
			}
			var want int64
			for _, v := range vals {
				want += v
			}
			if got := a.Sum(); got != want {
				t.Fatalf("seed=%d n=%d w=%d: sum=%d want=%d", seed, n, w, got, want)
			}
		}
	}
}

func TestIdempotent(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for _, n := range []int{1, 10, 100} {
		a := New(2) // small window forces evictions and stale replays
		for i := 0; i < n; i++ {
			e := ev{int64(r.Intn(6)) + 1, r.Int63n(21) - 10}
			o1 := a.Apply(e.seq, e.val)
			sum, win, stale := a.Sum(), a.Window(), a.Stale()
			o2 := a.Apply(e.seq, e.val) // identical redelivery
			if a.Sum() != sum || !reflect.DeepEqual(a.Window(), win) {
				t.Fatalf("redelivery %v changed sum/window: %+v %+v", e, o1, o2)
			}
			switch o1.Kind {
			case Stale: // different val, outside window: rejected and counted again
				if o2.Kind != Stale || a.Stale() != stale+1 {
					t.Fatalf("stale replay %v: %+v stale %d->%d", e, o2, stale, a.Stale())
				}
			default: // stored val now equals e.val: true idempotent no-op
				if o2.Kind != Duplicate || o2.Changed || a.Stale() != stale {
					t.Fatalf("duplicate %v: %+v stale %d->%d", e, o2, stale, a.Stale())
				}
			}
		}
	}
}

func TestWindowProbeScaling(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		a := New(m)
		for s := int64(1); s <= int64(m); s++ {
			a.Apply(s, s)
		}
		a.Apply(int64(m), 42) // in-window correction
		if a.probes != 1 {
			// 1 map lookup, independent of m: membership is O(1), not a scan
			t.Fatalf("m=%d correction probes=%d, want 1", m, a.probes)
		}
		a.Apply(int64(m)+1, 1) // evicts seq 1
		sumBefore := a.Sum()
		a.Apply(1, 999) // expired correction: sum must be untouched
		if a.probes != 1 || a.Stale() != 1 || a.Sum() != sumBefore {
			t.Fatalf("m=%d expired probes=%d stale=%d sum=%d->%d",
				m, a.probes, a.Stale(), sumBefore, a.Sum())
		}
	}
}
