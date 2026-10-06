package billing

import (
	"fmt"
	"math"
	"testing"
)

// prefillReal 在 n 个槽位各放一个采样，返回就绪的结算器。
func prefillReal(b *testing.B, n int) *Biller {
	b.Helper()
	bl, err := NewBiller(0, n)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := bl.Submit(Sample{Slot: i, Inbound: int64(i + 1), Outbound: int64(i + 1), Version: 1}); err != nil {
			b.Fatal(err)
		}
	}
	return bl
}

func prefillNaive(b *testing.B, n int) *NaiveBiller {
	b.Helper()
	bl := NewNaiveBiller(0, n)
	for i := 0; i < n; i++ {
		if err := bl.Submit(Sample{Slot: i, Inbound: int64(i + 1), Outbound: int64(i + 1), Version: 1}); err != nil {
			b.Fatal(err)
		}
	}
	return bl
}

// BenchmarkQueryReal：treap 路径的查询，理论 O(log K)。
func BenchmarkQueryReal(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		bl := prefillReal(b, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			var sink int64
			for i := 0; i < b.N; i++ {
				r, err := bl.CurrentRate()
				if err != nil {
					b.Fatal(err)
				}
				sink = r
			}
			_ = sink
		})
	}
}

// BenchmarkQueryNaive：每次排序的朴素查询，理论 O(K log K)。
func BenchmarkQueryNaive(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000, 1_000_000} {
		bl := prefillNaive(b, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			var sink int64
			for i := 0; i < b.N; i++ {
				r, err := bl.CurrentRate()
				if err != nil {
					b.Fatal(err)
				}
				sink = r
			}
			_ = sink
		})
	}
}

// BenchmarkSubmitReal：稳态下一次提交包含一次删除 + 一次插入 + 一次
// 顺序统计不参与提交；理论期望 O(log K)。
func BenchmarkSubmitReal(b *testing.B) {
	for _, n := range []int{10_000, 100_000, 1_000_000} {
		bl := prefillReal(b, n)
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				slot := i % n
				if err := bl.Submit(Sample{
					Slot: slot, Inbound: int64(i%n) + 1, Outbound: int64(i%n) + 1,
					Version: int64(i + 2),
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestTreapShape 以 1..1000000 顺序插入后校验树高：
// 期望高度 O(log K)，取 c log2(K) 的宽松上界 c=4（约 80）。
// 这给出了“操作路径长度不随 K 线性增长”的可复现结构证据。
func TestTreapShape(t *testing.T) {
	const k = 1_000_000
	var m multiset
	for i := 0; i < k; i++ {
		m.add(int64(i))
	}
	if m.size() != k {
		t.Fatalf("size=%d want %d", m.size(), k)
	}
	h := m.height()
	bound := int(4 * math.Ceil(math.Log2(k)))
	if h > bound {
		t.Fatalf("treap height=%d exceeds 4*log2(%d)=%d", h, k, bound)
	}
	// 顺序统计抽查：第 r 大等于 k-r。
	for _, r := range []int{1, 2, 50_000, k} {
		if got := m.kthLargest(r); got != int64(k-r) {
			t.Fatalf("kthLargest(%d)=%d want %d", r, got, k-r)
		}
	}
	t.Logf("treap with %d keys: height=%d, 4*log2(K) bound=%d", k, h, bound)
}
