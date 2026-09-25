package api

import (
	"errors"
	"fmt"
	"math/rand"
	"ontology/chlog"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func TestEightStep(t *testing.T) {
	cs := []struct {
		seq, val, sum, stale int64
		st                   Status
		nch                  int
	}{
		{5, 10, 10, 0, StatusNew, 1}, {7, 20, 30, 0, StatusNew, 2},
		{6, 15, 45, 0, StatusLate, 2}, {8, 5, 50, 0, StatusNew, 2},
		{5, 99, 50, 1, StatusStale, 0}, {6, 15, 50, 1, StatusDuplicate, 0},
		{6, 30, 65, 1, StatusCorrection, 2}, {9, 3, 68, 1, StatusNew, 2},
	}
	e, _ := New(3)
	for i, c := range cs {
		ch, st, err := e.Apply("k", c.seq, c.val)
		if err != nil || st != c.st || e.View()["k"] != c.sum || len(ch) != c.nch || e.Stale() != c.stale {
			t.Fatalf("step %d: ch=%v st=%d view=%v stale=%d", i+1, ch, st, e.View(), e.Stale())
		}
	}
}
func TestBatchRecompute(t *testing.T) { // independent map+FIFO reference
	for seed := int64(0); seed < 20; seed++ {
		for _, w := range []int{1, 2, 3, 8} {
			r := rand.New(rand.NewSource(seed*8 + int64(w)))
			e, _ := New(w)
			mv, mq, in := map[string]map[int64]int64{}, map[string][]int64{}, map[string]map[int64]bool{}
			for j := 0; j < 200; j++ {
				k := fmt.Sprintf("k%d", r.Intn(4))
				s, v := int64(r.Intn(9))+1, r.Int63n(21)-10
				e.Apply(k, s, v)
				if mv[k] == nil {
					mv[k], in[k] = map[int64]int64{}, map[int64]bool{}
				}
				m := mv[k]
				switch cur, seen := m[s]; {
				case !seen: // new Seq enters the FIFO; evicted Seqs still sum
					m[s], in[k][s] = v, true
					mq[k] = append(mq[k], s)
					if len(mq[k]) > w {
						delete(in[k], mq[k][0])
						mq[k] = mq[k][1:]
					}
				case cur != v && in[k][s]: // in-window correction; order untouched
					m[s] = v
				}
			}
			for k, m := range mv {
				var want int64
				for _, v := range m {
					want += v
				}
				if e.View()[k] != want {
					t.Fatalf("seed=%d w=%d key=%s: %d != %d", seed, w, k, e.View()[k], want)
				}
			}
		}
	}
}
func TestIdempotent(t *testing.T) {
	for _, n := range []int{1, 20, 200} {
		r := rand.New(rand.NewSource(int64(n)))
		e, _ := New(2)
		for i := 0; i < n; i++ {
			k := fmt.Sprintf("k%d", r.Intn(3))
			s, v := int64(r.Intn(6))+1, r.Int63n(11)-5
			e.Apply(k, s, v)
			v0, z0, l0 := e.View(), e.Stale(), e.log.Len()
			_, st, err := e.Apply(k, s, v) // identical redelivery
			if err != nil || !reflect.DeepEqual(e.View(), v0) || e.log.Len() != l0 ||
				(st == StatusStale) != (e.Stale() == z0+1) ||
				(st != StatusStale && st != StatusDuplicate) {
				t.Fatalf("redelivery: st=%d err=%v stale %d->%d", st, err, z0, e.Stale())
			}
		}
	}
}
func TestChangelogPrefixes(t *testing.T) {
	e, _ := New(3)
	for _, s := range [][2]int64{{5, 10}, {7, 20}, {6, 15}, {8, 5}, {6, 30}, {9, 3}} {
		e.Apply("k", s[0], s[1])
	}
	chs := e.log.Changes() // opening + and five -/+ pairs = 11
	for n := range len(chs) + 1 {
		if _, err := chlog.ReplayPrefix(chs, n); err != nil {
			t.Fatalf("prefix %d: %v", n, err)
		}
	}
}
func TestRejectedNoTrace(t *testing.T) {
	e, _ := New(2)
	e.Apply("a", 1, 5)
	v0, z0, l0 := e.View(), e.Stale(), e.log.Len()
	_, eW := New(0)
	_, _, eK := e.Apply("", 1, 1)
	_, _, eS := e.Apply("a", 0, 1)
	for _, c := range [][2]error{{eW, ErrInvalidW}, {eK, ErrEmptyKey}, {eS, ErrInvalidSeq}} {
		if !errors.Is(c[0], c[1]) {
			t.Fatalf("got %v want %v", c[0], c[1])
		}
	}
	if !reflect.DeepEqual(e.View(), v0) || e.Stale() != z0 || e.log.Len() != l0 {
		t.Fatal("rejection left a trace")
	}
	if _, _, err := e.Apply("b", 2, 7); err != nil {
		t.Fatal(err)
	}
}
func TestSelfCheck(t *testing.T) {
	if g, _ := New(3); g.SelfCheck() != nil {
		t.Fatal(g.SelfCheck())
	}
}
func TestConcurrentApply(t *testing.T) {
	const N = 500
	e, _ := New(N)
	var stop atomic.Bool
	var wg, rd sync.WaitGroup
	wg.Add(N)
	for i := 1; i <= N; i++ { // each goroutine owns one distinct Seq -> +1
		go func(s int64) { defer wg.Done(); e.Apply("p", s, 1) }(int64(i))
	}
	rd.Add(1) // a concurrent reader only ever sees committed counts in [0,N]
	go func() {
		defer rd.Done()
		for !stop.Load() {
			if s := e.View()["p"]; s < 0 || s > N {
				t.Errorf("unexplainable concurrent sum %d", s)
			}
		}
	}()
	wg.Wait()
	stop.Store(true)
	rd.Wait()
	if e.View()["p"] != N {
		t.Fatalf("sum=%d want %d", e.View()["p"], N)
	}
}
