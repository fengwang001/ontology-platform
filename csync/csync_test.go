package csync

import (
	"math/bits"
	"math/rand"
	"sort"
	"strconv"
	"sync"
	"testing"
)

func newSet(f int, cs ...Clock) *Set {
	s, _ := NewSet(f)
	for _, c := range cs {
		s.Add(c)
	}
	return s
}
func randClocks(r *rand.Rand, n int, e int64) []Clock {
	cs := make([]Clock, n)
	for i := range cs {
		cs[i] = Clock{strconv.Itoa(i), r.Int63n(200) - 100, r.Int63n(e)}
	}
	return cs
}
func naiveSweep(cs []Clock, need int) (lo, hi int64, ok bool) {
	var es [][2]int64
	for _, c := range cs {
		es = append(es, [2]int64{c.Offset - c.Err, 1}, [2]int64{c.Offset + c.Err, -1})
	}
	sort.SliceStable(es, func(i, j int) bool {
		return es[i][0] < es[j][0] || es[i][0] == es[j][0] && es[i][1] > es[j][1]
	})
	n, in, cl := 0, false, int64(0)
	for _, e := range es {
		p := n
		n += int(e[1])
		if !in && p < need && n >= need {
			cl, in = e[0], true
		} else if in && n < need {
			if !ok || e[0]-cl < hi-lo {
				lo, hi = cl, e[0]
			}
			ok, in = true, false
		}
	}
	return
}
func naiveCount(cs []Clock, t int64) (n int) {
	for _, c := range cs {
		if c.Offset-c.Err <= t && t <= c.Offset+c.Err {
			n++
		}
	}
	return
}

var abc = []Clock{{ID: "A", Offset: 10, Err: 2}, {ID: "B", Offset: 11, Err: 1}, {ID: "C", Offset: 20, Err: 1}}

func TestConsensusMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	cases, fs := [][]Clock{abc, abc}, []int{1, 0}
	for range 40 {
		cs := randClocks(r, 2+r.Intn(8), 6)
		cases, fs = append(cases, cs), append(fs, r.Intn(len(cs)))
	}
	for i, cs := range cases {
		lo, hi, err := newSet(fs[i], cs...).Consensus()
		nlo, nhi, nok := naiveSweep(cs, len(cs)-fs[i])
		if nok != (err == nil) || err == nil && (lo != nlo || hi != nhi) {
			t.Fatalf("case %d: [%d,%d] %v naive [%d,%d] %v", i, lo, hi, err, nlo, nhi, nok)
		}
	}
}
func TestBoundaryClosed(t *testing.T) {
	cases := []struct {
		f      int
		cs     []Clock
		lo, hi int64
	}{
		{1, abc, 10, 12},
		{1, []Clock{{"a", 0, 2}, {"b", 3, 1}, {"c", 100, 1}}, 2, 2},
	}
	for _, tc := range cases {
		s, need := newSet(tc.f, tc.cs...), len(tc.cs)-tc.f
		lo, hi, err := s.Consensus()
		if err != nil || lo != tc.lo || hi != tc.hi || s.CountAt(lo) < need || s.CountAt(hi) < need {
			t.Fatalf("%v f=%d [%d,%d] %v ends=%d,%d", tc.cs, tc.f, lo, hi, err, s.CountAt(tc.lo), s.CountAt(tc.hi))
		}
	}
}
func TestCountAtMatchesNaive(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for range 30 {
		cs := randClocks(r, 2+r.Intn(10), 8)
		s := newSet(0, cs...)
		for _, c := range cs {
			for _, tv := range []int64{c.Offset - c.Err - 1, c.Offset - c.Err, c.Offset, c.Offset + c.Err, c.Offset + c.Err + 1, r.Int63n(200) - 100} {
				if s.CountAt(tv) != naiveCount(cs, tv) {
					t.Fatalf("CountAt(%d) mismatch, cs=%v", tv, cs)
				}
			}
		}
	}
}
func TestCountAtProbeCount(t *testing.T) {
	r := rand.New(rand.NewSource(99))
	for _, m := range []int{100, 1000, 10000} {
		s := newSet(1, randClocks(r, m, 1000)...)
		s.CountAt(1 << 30)
		if s.probes > 2*bits.Len(uint(m))+2 { // probes 须 O(log m)：两次二分，不随 m 线性增长
			t.Fatalf("m=%d probes=%d", m, s.probes)
		}
	}
}
func TestConcurrentConsistency(t *testing.T) {
	ro := newSet(1, abc...)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 64 { // 阶段一：并发只读 Consensus/CountAt，结果须一致（start 闸门、无 sleep）
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if l, h, _ := ro.Consensus(); l != 10 || h != 12 || ro.CountAt(12) != 2 {
				t.Error("readonly disagreement")
			}
		}()
	}
	close(start)
	wg.Wait()
	cs := randClocks(rand.New(rand.NewSource(2026)), 200, 5) // 阶段二：并发 Add 后共识须与串行一致
	par := newSet(1)
	start = make(chan struct{})
	for _, c := range cs {
		wg.Add(1)
		go func(c Clock) {
			defer wg.Done()
			<-start
			par.Add(c)
		}(c)
	}
	close(start)
	wg.Wait()
	pl, ph, _ := par.Consensus()
	sl, sh, _ := newSet(1, cs...).Consensus()
	if par.Len() != 200 || pl != sl || ph != sh {
		t.Fatalf("parallel [%d,%d] serial [%d,%d]", pl, ph, sl, sh)
	}
}
