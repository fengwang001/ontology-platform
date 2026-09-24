package api_test

import (
	"errors"
	"math/rand"
	"sync"
	"testing"

	"ontology/api"
	"ontology/evt"
)

// scan 是朴素批量参考：重扫多重集，返回最小事件与出现总数。
func scan(c map[evt.Event]int) (string, int64, bool, int) {
	var m evt.Event
	have, total := false, 0
	for e, k := range c {
		total += k
		if k > 0 && (!have || evt.Less(e, m)) {
			m, have = e, true
		}
	}
	return m.Key, m.TS, have, total
}
func TestNaiveRescanConsistency(t *testing.T) {
	for _, seed := range []int64{1, 7, 42} {
		tr, ref, r := api.New(1<<20), map[evt.Event]int{}, rand.New(rand.NewSource(seed))
		for step := 0; step < 2000; step++ {
			e := evt.Event{Key: string(rune('a' + r.Intn(6))), TS: int64(r.Intn(7)) - 3}
			if r.Intn(2) == 0 {
				tr.Add(e.Key, e.TS) // 容量充足、Key 非空，不会失败
				ref[e]++
			} else if tr.Remove(e.Key, e.TS) == nil {
				ref[e]--
			}
			k, ts, ok := tr.First()
			gk, gts, gok, gn := scan(ref)
			if ok != gok || tr.Count() != gn || ok && (k != gk || ts != gts) {
				t.Fatalf("seed=%d step=%d mismatch", seed, step)
			}
		}
	}
}
func TestRemovePromotesNext(t *testing.T) {
	tr, ref := api.New(1<<20), map[evt.Event]int{}
	for _, p := range rand.New(rand.NewSource(99)).Perm(200) {
		tr.Add("k", int64(p-100)) // 容量充足、Key 非空，不会失败
		ref[evt.Event{Key: "k", TS: int64(p - 100)}]++
	}
	for {
		k, ts, ok := tr.First()
		gk, gts, gok, n := scan(ref)
		if ok != gok || ok && (k != gk || ts != gts) {
			t.Fatal("first mismatch")
		}
		if n == 0 {
			break
		}
		if err := tr.Remove(k, ts); err != nil {
			t.Fatal(err)
		}
		ref[evt.Event{Key: k, TS: ts}]-- // 撤首值；下一轮循环立即对拍次首提升结果
	}
}
func TestMultisetRoundTrip(t *testing.T) {
	for _, K := range []int{1, 2, 3, 5, 8} {
		tr := api.New(1 << 20)
		for i := 1; i <= K; i++ {
			if tr.Add("dup", -4) != nil || tr.Count() != i {
				t.Fatalf("K=%d add #%d", K, i)
			}
		}
		for i := K; i >= 1; i-- {
			if tr.Remove("dup", -4) != nil || tr.Count() != i-1 {
				t.Fatalf("K=%d remove->%d", K, i-1)
			}
		}
		if _, _, ok := tr.First(); ok {
			t.Fatalf("K=%d not empty", K)
		}
		if err := tr.Remove("dup", -4); !errors.Is(err, api.ErrNotFound) {
			t.Fatalf("extra remove got %v", err)
		}
	}
}
func TestRejectedOpsAtomic(t *testing.T) {
	tr := api.New(2)
	tr.Add("a", 5) // Key 非空、未超容量，不会失败
	cases := []struct {
		f func() error
		e error
	}{
		{func() error { return tr.Add("", 1) }, api.ErrEmptyKey},
		{func() error { return tr.Remove("", 1) }, api.ErrEmptyKey},
		{func() error { return tr.Remove("z", 9) }, api.ErrNotFound},
	}
	for _, c := range cases {
		bK, bTS, bOK := tr.First()
		bN := tr.Count()
		if err := c.f(); !errors.Is(err, c.e) {
			t.Fatalf("got %v want %v", err, c.e)
		}
		if k, ts, ok := tr.First(); ok != bOK || k != bK || ts != bTS || tr.Count() != bN {
			t.Fatal("rejected op changed state")
		}
	}
	if err := tr.Add("b", 3); err != nil {
		t.Fatal(err)
	}
	if err := tr.Add("c", 4); !errors.Is(err, api.ErrCapacity) {
		t.Fatalf("capacity got %v", err)
	}
	if k, ts, ok := tr.First(); !ok || k != "b" || ts != 3 || tr.Count() != 2 {
		t.Fatal("capacity rejection changed state")
	}
	if tr.Remove("b", 3) != nil || tr.Add("c", 4) != nil { // 被拒后仍可正常使用
		t.Fatal("unusable after rejection")
	}
	if api.ErrEmptyKey == api.ErrNotFound || api.ErrEmptyKey == api.ErrCapacity || api.ErrNotFound == api.ErrCapacity {
		t.Fatal("sentinel errors must be distinct")
	}
	if !api.New(8).SelfCheck() {
		t.Fatal("built-in SelfCheck failed")
	}
}
func TestConcurrentReaders(t *testing.T) {
	tr := api.New(1 << 20)
	for _, p := range rand.New(rand.NewSource(7)).Perm(500) {
		tr.Add("k", int64(p)) // 容量充足、Key 非空，不会失败
	}
	wK, wTS, wOK := tr.First()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				k, ts, ok := tr.First()
				if k != wK || ts != wTS || ok != wOK || tr.Count() != 500 || !tr.SelfCheck() {
					t.Error("concurrent reader diverged")
				}
			}
		}()
	}
	wg.Wait()
}
