package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

var eight = [][2]int64{
	{1000, 60}, {1001, 50}, {1003, 40}, {1004, 60}, {1007, 30}, {1008, 80}, {1010, 20}, {1012, 50},
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func build(t *testing.T, base, interval int64, seq [][2]int64) *Index {
	t.Helper()
	ix, err := New(base, interval)
	must(t, err)
	for _, r := range seq {
		_, err := ix.Append(r[0], r[1])
		must(t, err)
	}
	return ix
}

// 不变量 2（良构）与 3（与重算一致）的公共断言。
func checkWellFormed(t *testing.T, ix *Index, base, interval int64) {
	t.Helper()
	ents, recs := ix.Entries(), ix.seg.Records()
	at := map[int64]int64{}
	for _, r := range recs {
		at[r.Pos] = r.Offset - base
	}
	for i, e := range ents {
		if rel, ok := at[e.Pos]; !ok || rel != int64(e.Rel) {
			t.Fatalf("entry %d not at a matching record start", i)
		}
		if i > 0 && (e.Rel <= ents[i-1].Rel || e.Pos <= ents[i-1].Pos) {
			t.Fatalf("entry %d not strictly increasing", i)
		}
	}
	if fmt.Sprint(ents) != fmt.Sprint(recompute(recs, base, interval)) {
		t.Fatal("index != recomputed")
	}
}

// 不变量 4：任何被拒操作不改变状态；四类哨兵错误互不相同。
func TestFailureNoTrace(t *testing.T) {
	ix := build(t, 1000, 100, eight)
	snap := func() string { return fmt.Sprint(ix.seg.Records(), ix.Entries(), ix.seg.Bytes()) }
	before := snap()
	if _, err := New(-1, 1); !errors.Is(err, ErrInvalid) {
		t.Error("bad base accepted")
	}
	if _, err := New(0, 0); !errors.Is(err, ErrInvalid) {
		t.Error("bad interval accepted")
	}
	for _, a := range [][2]int64{{999, 10}, {1012, 10}, {1013, 0}} {
		if _, err := ix.Append(a[0], a[1]); !errors.Is(err, ErrInvalid) {
			t.Errorf("Append%v accepted", a)
		}
	}
	if _, err := ix.Append(1000+2147483648, 10); !errors.Is(err, ErrOverflow) {
		t.Error("overflow accepted")
	}
	if _, _, err := ix.Lookup(999); !errors.Is(err, ErrBelowBase) {
		t.Error("below-base lookup accepted")
	}
	if _, _, err := ix.Lookup(1013); !errors.Is(err, ErrNotFound) {
		t.Error("not-found not reported")
	}
	if got := snap(); got != before {
		t.Error("state changed after rejections")
	}
	sentinels := []error{ErrInvalid, ErrOverflow, ErrBelowBase, ErrNotFound}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if (i == j) != errors.Is(a, b) {
				t.Errorf("sentinels %d,%d not distinct", i, j)
			}
		}
	}
	must(t, func() error { _, err := ix.Append(1013, 40); return err }()) // 被拒后仍可正常使用
}
func TestIndexWellFormed(t *testing.T) {
	ix := build(t, 100, 25, [][2]int64{{100, 10}, {105, 30}, {106, 20}, {110, 40}, {120, 15}})
	checkWellFormed(t, ix, 100, 25)
}
func TestIndexRecompute(t *testing.T) {
	for _, iv := range []int64{1, 25, 100, 1000} {
		ix := build(t, 100, iv, eight)
		if got, want := fmt.Sprint(ix.Entries()), fmt.Sprint(recompute(ix.seg.Records(), 100, iv)); got != want {
			t.Errorf("interval=%d: %s != %s", iv, got, want)
		}
	}
}

// 一个 goroutine 递增追加，8 个 goroutine 并发查找已追加位点；
// 结果必须等于该位点且物理位置与追加时返回的一致；不用 sleep 造时序。
func TestConcurrentAppendLookup(t *testing.T) {
	const m = 4000
	ix, err := New(0, 40)
	must(t, err)
	positions := make([]int64, m)
	var n atomic.Int64
	var good atomic.Bool
	good.Store(true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < m; i++ {
			positions[i], _ = ix.Append(int64(i), 10)
			n.Store(int64(i + 1))
		}
	}()
	lookup := func(seed int64) {
		defer wg.Done()
		r := rand.New(rand.NewSource(seed))
		for k := 0; k < 1500; k++ {
			if cnt := n.Load(); cnt > 0 {
				tg := r.Int63n(cnt)
				if o, p, err := ix.Lookup(tg); err != nil || o != tg || p != positions[tg] {
					good.Store(false)
				}
			}
		}
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go lookup(int64(g))
	}
	wg.Wait()
	if !good.Load() {
		t.Fatal("concurrent lookup mismatch")
	}
	checkWellFormed(t, ix, 0, 40)
}

func TestSelfCheck(t *testing.T) {
	must(t, build(t, 0, 1, [][2]int64{{0, 1}}).SelfCheck())
}
