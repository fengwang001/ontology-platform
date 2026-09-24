package cbf

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/hash"
)

type cfg struct {
	m, k, n int
	max     uint8
	seed    int64
}

var rc = []cfg{{101, 4, 500, 9, 1}, {499, 7, 2000, 50, 2}, {1009, 5, 3000, 200, 3}}

func failIf(t *testing.T, bad bool, msg string) {
	t.Helper()
	if bad {
		t.Fatal(msg)
	}
}

func mustP(t *testing.T, m, k int) hash.Params {
	p, e := hash.New(m, k)
	failIf(t, e != nil, "bad hash params")
	return p
}

func untouched(f *Filter, c []uint8, n map[int64]int) bool {
	return equalU8(f.counters, c) && equalCounts(f.counts, n)
}

func fillRandom(t *testing.T, c cfg) (*Filter, map[int64]int) {
	f, _ := New(mustP(t, c.m, c.k), c.max)
	r, ms := rand.New(rand.NewSource(c.seed)), map[int64]int{}
	for i := 0; i < c.n; i++ {
		x := r.Int63n(int64(c.m) * 3) // 非负 int64 键
		if ms[x] > 0 && r.Intn(3) == 0 {
			failIf(t, f.Delete(x) != nil, "unexpected delete error")
			if ms[x]--; ms[x] == 0 {
				delete(ms, x)
			}
		} else if e := f.Insert(x); e == nil {
			ms[x]++
		} else {
			failIf(t, !errors.Is(e, ErrOverflow), "expected overflow")
		}
	}
	return f, ms
}

// TestConservation 钉不变量 2：计数器之和 == k×（成功插入−成功删除）。
func TestConservation(t *testing.T) {
	for _, c := range rc {
		f, _ := fillRandom(t, c)
		failIf(t, sumU8(f.counters) != c.k*(f.inserts-f.deletes), "conservation broken")
	}
}

// TestNoFalseNegatives 钉不变量 1：仍在册（净次数>0）的键 Contains 必 true。
func TestNoFalseNegatives(t *testing.T) {
	for _, c := range rc {
		f, ms := fillRandom(t, c)
		for x := range ms {
			failIf(t, !f.Contains(x), "false negative")
		}
	}
}

// TestNaiveRecomputation 钉不变量 3：计数器逐格与 Contains（含假阳性）均与朴素重算一致。
func TestNaiveRecomputation(t *testing.T) {
	f, ms := fillRandom(t, rc[1])
	nv := recompute(f.p, ms)
	for q := range nv {
		failIf(t, f.counters[q] != nv[q], "counter disagrees with naive")
	}
	for y := int64(0); y < 1500; y++ {
		na := true
		for _, q := range f.p.Positions(y) {
			na = na && nv[q] > 0
		}
		failIf(t, f.Contains(y) != na, "Contains disagrees with naive")
	}
}

// TestRejectedOpsLeaveNoTrace 钉不变量 4：三类拒绝不留痕、哨兵互不相同、可继续使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, b := range [][3]int{{6, 3, 2}, {3, 5, 2}, {7, 0, 2}, {7, 3, 0}} {
		p, e := hash.New(b[0], b[1])
		if e == nil {
			_, e = New(p, uint8(b[2]))
		}
		failIf(t, !errors.Is(e, hash.ErrInvalidParameters), "bad params not rejected")
	}
	g, _ := New(mustP(t, 7, 3), 2)
	g.Insert(1)
	g.Insert(1)
	c0, n0 := cloneCounters(g.counters), cloneCounts(g.counts)
	failIf(t, func() bool { e := g.Insert(1); return !errors.Is(e, ErrOverflow) || !untouched(g, c0, n0) }(), "overflow left trace")
	h, _ := New(mustP(t, 7, 3), 2)
	h.Insert(4)
	c1, n1 := cloneCounters(h.counters), cloneCounts(h.counts)
	failIf(t, func() bool { e := h.Delete(2); return !errors.Is(e, ErrDeleteUninserted) || !untouched(h, c1, n1) }(), "delete-uninserted left trace")
	failIf(t, ErrOverflow == ErrDeleteUninserted || ErrOverflow == hash.ErrInvalidParameters ||
		ErrDeleteUninserted == hash.ErrInvalidParameters, "sentinels must be distinct")
	failIf(t, h.Insert(9) != nil, "filter unusable after rejection")
}

// TestVisitedCountEqualsK 钉复杂度：m 跨多档，一次 Insert 访问数恒为 k。
func TestVisitedCountEqualsK(t *testing.T) {
	for _, m := range []int{101, 251, 503, 1009, 4999, 9973} {
		f, e := New(mustP(t, m, 7), 10)
		failIf(t, e != nil || f.Insert(123) != nil || f.visited.Load() != 7, "visited != k")
	}
}

// TestConcurrentContains 钉并发（无 sleep）：N goroutine 并发只读，逐键同基线。
func TestConcurrentContains(t *testing.T) {
	f, _ := New(mustP(t, 1009, 6), 255)
	keys := make([]int64, 120)
	for i := range keys {
		keys[i] = int64(i * 3)
		if i < 70 {
			f.Insert(keys[i])
		}
	}
	base := make([]bool, len(keys))
	for i, x := range keys {
		base[i] = f.Contains(x)
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, x := range keys {
				if f.Contains(x) != base[i] { // t.Errorf 可在多 goroutine 并发调用
					t.Errorf("concurrent Contains(%d) differs", x)
				}
			}
		}()
	}
	wg.Wait()
}

func TestSelfCheck(t *testing.T) { failIf(t, SelfCheck() != nil, "self-check failed") }
