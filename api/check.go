package api

import (
	"fmt"
	"math/rand"
	"reflect"

	"ontology/vc"
)

func mm(f int, v ...int64) Msg { return Msg{From: f, V: v} }
func mk(m Msg) [2]int64        { return [2]int64{int64(m.From), m.V[m.From]} }

func veq(a, b []int64) bool { return reflect.DeepEqual(a, b) }
func snap(b *Buffer) string {
	return fmt.Sprint(b.Local(), len(b.Buffered()), len(b.Delivered()), b.Dups())
}

// closed builds a causally closed pool on one simulated true order.
func closed(rng *rand.Rand, n, count int) []Msg {
	l, p := make([]int64, n), []Msg{}
	for i := 0; i < count; i++ {
		j := rng.Intn(n)
		v := append([]int64(nil), l...)
		v[j]++
		l[j]++
		p = append(p, mm(j, v...))
	}
	return p
}

// ordered verifies invariant 2: contiguous per-sender sequence and every
// referenced predecessor already delivered.
func ordered(log []Msg) error {
	c := map[int]int64{}
	seen := map[[2]int64]bool{}
	for _, m := range log {
		if c[m.From]+1 != m.V[m.From] {
			return fmt.Errorf("selfcheck: non-contiguous sequence from %d", m.From)
		}
		for k, v := range m.V {
			for s := int64(1); k != m.From && s <= v; s++ {
				if !seen[[2]int64{int64(k), s}] {
					return fmt.Errorf("selfcheck: predecessor (%d,%d) missing", k, s)
				}
			}
		}
		c[m.From] = m.V[m.From]
		seen[mk(m)] = true
	}
	return nil
}

var seven = []Msg{mm(1, 2, 2, 0), mm(2, 1, 1, 1), mm(1, 1, 1, 0),
	mm(0, 2, 0, 0), mm(2, 2, 2, 2), mm(0, 1, 0, 0), mm(1, 1, 1, 0)}

// naive is the independent whole-table-scan reference shared by SelfCheck
// and the internal tests.
type naive struct {
	n, cap int
	l      []int64
	b      map[[2]int64]Msg
	d      []Msg
	dup    int64
}

func newNaive(n, c int) *naive {
	return &naive{n: n, cap: c, l: make([]int64, n), b: map[[2]int64]Msg{}}
}

func (r *naive) recv(m Msg) (int, error) {
	if e := vc.Legal(r.n, m); e != nil {
		return 0, e
	}
	k := mk(m)
	if _, ok := r.b[k]; m.V[m.From] <= r.l[m.From] || ok {
		r.dup++
		return 0, nil
	}
	put := func(x Msg) { r.l[x.From]++; r.d = append(r.d, x) }
	c := 0
	if !vc.Deliverable(m, r.l) {
		if len(r.b) >= r.cap {
			return 0, ErrBufferFull
		}
		r.b[k] = m
	} else {
		put(m)
		c++
	}
	for { // whole-table scan to fixpoint, smallest From first
		var bk [2]int64
		px, found := Msg{}, false
		for kk, x := range r.b {
			if vc.Deliverable(x, r.l) && (!found || kk[0] < bk[0]) {
				bk, px, found = kk, x, true
			}
		}
		if !found {
			return c, nil
		}
		delete(r.b, bk)
		put(px)
		c++
	}
}

// SelfCheck runs built-in arrival sequences and verifies all four
// invariants against the whole-table naive reference.
func (b *Buffer) SelfCheck() error {
	buf, _ := New(3, 8) // section-3 seven arrivals: invariants 2/3 + dup
	for _, m := range seven {
		if _, e := buf.Receive(m); e != nil {
			return e
		}
	}
	if !veq(buf.Local(), []int64{2, 2, 2}) || len(buf.Buffered()) != 0 ||
		len(buf.Delivered()) != 6 || buf.Dups() != 1 || ordered(buf.Delivered()) != nil {
		return fmt.Errorf("selfcheck: section-3 mismatch")
	}
	s := snap(buf) // invariant 4
	if _, e := buf.Receive(mm(9, 1)); e != ErrBadSender || snap(buf) != s {
		return fmt.Errorf("selfcheck: rejection left a trace")
	}
	// Invariant 1 (+3), cap=2 forces full-buffer rejections.
	rng := rand.New(rand.NewSource(1))
	real, _ := New(3, 2)
	ref := newNaive(3, 2)
	arr := append([]Msg{}, closed(rng, 3, 40)...)
	for k := 0; k < 12; k++ {
		arr = append(arr, arr[rng.Intn(40)])
	}
	rng.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
	for _, m := range arr {
		g, e := real.Receive(m)
		rg, re := ref.recv(m)
		if (e == nil) != (re == nil) || len(g) != rg || !veq(real.Local(), ref.l) ||
			real.Dups() != ref.dup || len(real.Buffered()) != len(ref.b) ||
			len(real.Delivered()) != len(ref.d) {
			return fmt.Errorf("selfcheck: divergence vs naive reference")
		}
	}
	return ordered(real.Delivered())
}
