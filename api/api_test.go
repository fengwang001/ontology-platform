package api

import (
	"math/rand"
	"ontology/vc"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func mk(f int, v ...int64) Msg { return Msg{From: f, V: v} }
func naive(n int, arr []Msg) (loc []int64, buf, del []Msg, dups int64) {
	loc, have := make([]int64, n), map[[2]int64]bool{}
	for _, m := range arr {
		k := [2]int64{int64(m.From), m.V[m.From]}
		if have[k] || m.V[m.From] <= loc[m.From] {
			dups++
			continue
		}
		have[k] = true
		if !vc.Deliverable(m, loc) {
			buf = append(buf, m)
			continue
		}
		loc[m.From]++
		del = append(del, m)
		for {
			best := -1
			for i := range buf {
				if vc.Deliverable(buf[i], loc) && (best < 0 || buf[i].From < buf[best].From) {
					best = i
				}
			}
			if best < 0 {
				break
			}
			loc[buf[best].From]++
			del = append(del, buf[best])
			buf = append(buf[:best], buf[best+1:]...)
		}
	}
	return
}
func closedSet(n, k int) (ms []Msg) {
	for p := range n * k {
		j, t := p/k, p%k+1
		v := slices.Repeat([]int64{int64(t - 1)}, n)
		v[j] = int64(t)
		ms = append(ms, Msg{From: j, V: v})
	}
	return
}
func TestSevenStepScenario(t *testing.T) {
	a, b, c := mk(0, 1, 0, 0), mk(1, 1, 1, 0), mk(0, 2, 0, 0)
	d, e, f := mk(2, 1, 1, 1), mk(1, 2, 2, 0), mk(2, 2, 2, 2)
	r, _ := New(3, 8)
	exp := [][3]int{{0, 1, 0}, {0, 2, 0}, {0, 3, 0}, {0, 4, 0}, {0, 5, 0}, {6, 0, 0}, {0, 0, 1}}
	for i, m := range []Msg{e, d, b, c, f, a, b} {
		out, err := r.Receive(m)
		if err != nil || len(out) != exp[i][0] || len(r.Buffered()) != exp[i][1] || r.Dups() != int64(exp[i][2]) {
			t.Fatalf("step %d", i+1)
		}
	}
	if dl, l := r.Delivered(), r.Local(); !reflect.DeepEqual(dl, []Msg{a, c, b, e, d, f}) || causalOK(dl) != nil || l[0] != 2 || l[1] != 2 || l[2] != 2 {
		t.Fatalf("delivered=%v local=%v", dl, l)
	}
}
func TestNaiveAgreement(t *testing.T) {
	for _, c := range []struct{ n, k, ex, it int }{{2, 4, 2, 12}, {3, 3, 4, 12}, {4, 2, 3, 8}} {
		rnd := rand.New(rand.NewSource(int64(c.n*131 + c.k)))
		for range c.it {
			set := closedSet(c.n, c.k)
			arr := append(append([]Msg{}, set...), set[:c.ex]...)
			rnd.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
			r, _ := New(c.n, c.n*c.k+8)
			for _, m := range arr {
				r.Receive(m)
			}
			l, bf, dl, dp := naive(c.n, arr)
			if !reflect.DeepEqual(r.Delivered(), dl) || !reflect.DeepEqual(r.Local(), l) || len(r.Buffered()) != len(bf) || r.Dups() != dp {
				t.Fatalf("n=%d", c.n)
			}
		}
	}
}
func TestCompletenessAnyOrder(t *testing.T) {
	for _, c := range []struct{ n, k int }{{2, 5}, {3, 4}, {5, 3}} {
		for _, seed := range []int64{1, 2, 7, 42} {
			arr := closedSet(c.n, c.k)
			rand.New(rand.NewSource(seed)).Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
			r, _ := New(c.n, c.n*c.k)
			for _, m := range arr {
				r.Receive(m)
			}
			if d := r.Delivered(); len(d) != c.n*c.k || len(r.Buffered()) != 0 || r.Dups() != 0 || causalOK(d) != nil {
				t.Fatalf("n=%d seed=%d", c.n, seed)
			}
		}
	}
}
func TestRejectLeavesNoTrace(t *testing.T) {
	if len(map[error]bool{ErrParam: true, ErrSender: true, ErrVector: true, ErrFull: true}) != 4 {
		t.Fatal("sentinels not distinct")
	}
	for _, nb := range [][2]int{{0, 1}, {2, -1}} {
		if _, e := New(nb[0], nb[1]); e != ErrParam {
			t.Fatal("param want ErrParam")
		}
	}
	r, _ := New(2, 1)
	if _, e := r.Receive(mk(9, 0, 0)); e != ErrSender {
		t.Fatalf("sender want ErrSender got %v", e)
	}
	for _, m := range []Msg{mk(0, 2), mk(1, -1, 0), mk(0, 0, 0)} {
		if _, e := r.Receive(m); e != ErrVector {
			t.Fatalf("%v want ErrVector got %v", m, e)
		}
	}
	if r.Dups() != 0 || len(r.Buffered()) != 0 || len(r.Delivered()) != 0 {
		t.Fatal("illegal rejection left a trace")
	}
	u, _ := New(2, 0)
	_, e1 := u.Receive(mk(0, 2, 0))
	_, e2 := u.Receive(mk(0, 1, 0))
	if e1 != ErrFull || e2 != nil {
		t.Fatalf("full=%v usable-after=%v", e1, e2)
	}
}
func TestConcurrentReceive(t *testing.T) {
	s := closedSet(3, 4)
	arr := append(append([]Msg{}, s...), s[:6]...)
	rand.New(rand.NewSource(99)).Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
	r, _ := New(3, 128)
	const N = 8
	var wg sync.WaitGroup
	for w := range N {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < len(arr); i += N {
				r.Receive(arr[i])
			}
		}(w)
	}
	wg.Wait()
	if d := r.Delivered(); len(d) != 12 || len(r.Buffered()) != 0 || r.Dups() != 6 || causalOK(d) != nil || r.SelfCheck() != nil {
		t.Fatalf("del=%d buf=%d dup=%d", len(d), len(r.Buffered()), r.Dups())
	}
}
