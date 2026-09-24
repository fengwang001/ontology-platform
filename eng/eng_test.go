package eng

import (
	"errors"
	"maps"
	"math"
	"math/rand"
	"testing"

	"ontology/wrap"
)

// TestLookbackConstant 证明增量维护 O(1) 状态：先喂 m 个单调递增位点，
// 再喂一个更大的前进位点，该次 Feed 回看的位点个数恒为 1，不随 m 增长。
func TestLookbackConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		e := New(1 << 20)
		for i := 0; i < m; i++ {
			if _, err := e.Feed(uint32(i)); err != nil {
				t.Fatalf("m=%d feed %d: %v", m, i, err)
			}
		}
		if _, err := e.Feed(uint32(m)); err != nil {
			t.Fatalf("m=%d final feed: %v", m, err)
		}
		if e.lookback != 1 {
			t.Fatalf("m=%d: lookback=%d, want 1 (only prev compared)", m, e.lookback)
		}
	}
}

// TestOverflowRejected 溢出被拒且不留痕：构造 pu 接近 int64 上限的引擎，
// 喂入一个使展开值越界的前进位点，断言 ErrOverflow 且状态不变。
func TestOverflowRejected(t *testing.T) {
	e := New(1 << 20)
	if _, err := e.Feed(100); err != nil {
		t.Fatal(err)
	}
	e.pu = math.MaxInt64 - 10 // 同包内部测试直接构造临界状态
	before := e.Counts()
	_, err := e.Feed(200) // delta=100，越界
	if !errors.Is(err, wrap.ErrOverflow) {
		t.Fatalf("want ErrOverflow, got %v", err)
	}
	if e.prev != 100 || e.pu != math.MaxInt64-10 {
		t.Fatal("overflow mutated prev/pu")
	}
	for k, v := range before {
		if e.counts[k] != v {
			t.Fatal("overflow mutated counts")
		}
	}
	// 拒绝后仍可正常使用
	if _, err := e.Feed(105); err != nil {
		t.Fatalf("feed after overflow: %v", err)
	}
}

// TestWrapCorrectness 钉住回卷公式 unwrapped = pu + (2^32 − prev) + r。
func TestWrapCorrectness(t *testing.T) {
	for _, c := range []struct{ prev, r uint32 }{{4294967200, 50}, {4294967295, 5}, {4294967295, 0}, {4000000000, 10}} {
		e := New((1 << 31) - 1)
		e.Feed(c.prev)
		ev, err := e.Feed(c.r)
		u, _ := e.LastUnwrapped()
		if want := int64(c.prev) + (1<<32 - int64(c.prev)) + int64(c.r); err != nil || ev != wrap.Wrap || u != want {
			t.Fatalf("prev=%d r=%d: got %v/%v/%d want Wrap/%d", c.prev, c.r, ev, err, u, want)
		}
	}
}

// TestMonotonic 随机序列下 unwrapped 单调不减：接受步严格递增（Duplicate 不变），
// 被拒步不改变它。
func TestMonotonic(t *testing.T) {
	e := New(1 << 20)
	last, cur := int64(-1), uint32(0)
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 5000; i++ {
		r := cur
		switch x := rnd.Intn(10); {
		case x == 1 && cur > 5: // 小步倒退（被拒）
			r = cur - uint32(1+rnd.Intn(5))
		case x == 2: // 跳到回卷边界附近
			cur = math.MaxUint32 - uint32(rnd.Intn(100))
			r = cur
		case x >= 3: // 前进（自然越过 2^32 即回卷）
			cur += uint32(rnd.Intn(1000))
			r = cur
		}
		ev, err := e.Feed(r)
		u, _ := e.LastUnwrapped()
		bad := (err != nil && u != last) || (err == nil && ev == wrap.Duplicate && u != last) ||
			(err == nil && ev != wrap.Duplicate && u <= last)
		if bad {
			t.Fatalf("monotonic violated: ev=%v err=%v %d -> %d", ev, err, last, u)
		}
		if err == nil {
			last = u
		}
	}
}

// TestConcurrentReadOnly N 个 goroutine 并发只读同一个已喂满的实例，
// 各自拿到的 LastUnwrapped 与 Counts 必须逐字段相同。
func TestConcurrentReadOnly(t *testing.T) {
	e := New(1 << 20)
	for i := 0; i < 1000; i++ {
		e.Feed(uint32(i))
	}
	type res struct {
		u int64
		c map[wrap.Event]int
	}
	ch := make(chan res, 16)
	for i := 0; i < 16; i++ {
		go func() {
			u, _ := e.LastUnwrapped()
			if _, err := e.LastRaw(); err != nil {
				t.Error(err)
			}
			ch <- res{u, e.Counts()}
		}()
	}
	want := <-ch
	for i := 1; i < 16; i++ {
		if r := <-ch; r.u != want.u || !maps.Equal(r.c, want.c) {
			t.Fatal("concurrent readers got different results")
		}
	}
}
