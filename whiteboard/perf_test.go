package whiteboard

import (
	"fmt"
	"math/bits"
	"testing"
)

// 性能性质的可验证证明：
// treap.visits 统计全序原语访问的节点数。若 Rank 与单元素 Reorder 是 O(log n)，
// 则元素总数放大 100 倍（两个数量级）时，访问数只应约增长 log2(100) ≈ 6.6 倍，
// 且有确定的 log 上界；若是 O(n)，访问数会约增长 100 倍。
// Lock/Unlock 为哈希表 O(1) 操作，由基准测试在两档规模下对照佐证。

func buildBoard(tb testing.TB, n int) *Board {
	tb.Helper()
	b := New()
	for i := 0; i < n; i++ {
		if err := b.Add("setup", fmt.Sprintf("e%d", i), int64(i)); err != nil {
			tb.Fatalf("setup add failed: %v", err)
		}
	}
	return b
}

func log2ceil(n int) int {
	return bits.Len(uint(n))
}

func TestRankReorderVisitsScaleLogarithmically(t *testing.T) {
	const small, large = 1_000, 100_000 // 相差两个数量级
	bs := buildBoard(t, small)
	bl := buildBoard(t, large)
	measure := func(b *Board, f func()) int64 {
		b.ol.t.visits = 0
		f()
		return b.ol.t.visits
	}
	// Rank：对中间元素求名次。
	rs := measure(bs, func() { bs.Rank("e500") })
	rl := measure(bl, func() { bl.Rank("e50000") })
	// 单元素 Reorder：把中间元素移到另一元素上方。
	ms := measure(bs, func() {
		if err := bs.Reorder("u", "e500", "e100", Above, 1<<62, int64(small)); err != nil {
			t.Fatal(err)
		}
	})
	ml := measure(bl, func() {
		if err := bl.Reorder("u", "e50000", "e10000", Above, 1<<62, int64(large)); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("Rank visits: n=%d -> %d, n=%d -> %d (log2 上界 %d / %d)",
		small, rs, large, rl, 4*log2ceil(small), 4*log2ceil(large))
	t.Logf("Reorder(单元素) visits: n=%d -> %d, n=%d -> %d", small, ms, large, ml)
	// 确定性上界：每次原语访问不超过常数倍树高。
	if bound := int64(4 * log2ceil(small)); rs > bound {
		t.Fatalf("Rank visits %d exceed log bound %d at n=%d", rs, bound, small)
	}
	if bound := int64(4 * log2ceil(large)); rl > bound {
		t.Fatalf("Rank visits %d exceed log bound %d at n=%d", rl, bound, large)
	}
	if bound := int64(32 * log2ceil(small)); ms > bound {
		t.Fatalf("Reorder visits %d exceed log bound %d at n=%d", ms, bound, small)
	}
	if bound := int64(32 * log2ceil(large)); ml > bound {
		t.Fatalf("Reorder visits %d exceed log bound %d at n=%d", ml, bound, large)
	}
	// 增长比上界：规模放大 100 倍，访问数增长不得超过 8 倍（log2(100)≈6.6）。
	if rl > 8*rs {
		t.Fatalf("Rank visits grew %.1fx for 100x elements", float64(rl)/float64(rs))
	}
	if ml > 8*ms {
		t.Fatalf("Reorder visits grew %.1fx for 100x elements", float64(ml)/float64(ms))
	}
}

// ---- 基准：两档规模（10^4 与 10^6，相差两个数量级）对照 ----

func BenchmarkRank(b *testing.B) {
	for _, n := range []int{10_000, 1_000_000} {
		bd := buildBoard(b, n)
		mid := fmt.Sprintf("e%d", n/2)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				bd.Rank(mid)
			}
		})
	}
}

func BenchmarkReorderSingle(b *testing.B) {
	for _, n := range []int{10_000, 1_000_000} {
		bd := buildBoard(b, n)
		mid := fmt.Sprintf("e%d", n/2)
		anchor := fmt.Sprintf("e%d", n/4)
		now := int64(n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				now++
				if err := bd.Reorder("u", mid, anchor, Above, 1<<62, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkLockUnlock(b *testing.B) {
	for _, n := range []int{10_000, 1_000_000} {
		bd := buildBoard(b, n)
		// 先挂上大量锁，验证 Lock/Unlock 开销不随锁总数增长。
		for i := 0; i < n/2; i++ {
			if err := bd.Lock("u", fmt.Sprintf("e%d", i), MaxTTL, int64(n+i)); err != nil {
				b.Fatal(err)
			}
		}
		target := fmt.Sprintf("e%d", n-1)
		now := int64(2 * n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				now++
				if err := bd.Lock("u", target, MaxTTL, now); err != nil {
					b.Fatal(err)
				}
				now++
				if err := bd.Unlock("u", target, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
