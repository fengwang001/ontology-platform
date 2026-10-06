package matching

import (
	"fmt"
	"testing"
)

// BenchmarkAggressiveNoCross 衡量在巨大簿中，一个不成交新委托的插入/最优价开销。
// 若开销随簿内委托总数增长，则随 N 线性上升；预期几乎不随 N 变化（O(log L)）。
func BenchmarkAggressiveNoCross(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		n := n
		b.Run(fmt.Sprintf("orders=%d", n), func(b *testing.B) {
			e := New()
			for i := 0; i < n; i++ {
				price := int64(100 + i%500)
				if _, _, err := e.Submit(OrderParams{
					ClientID: int64(i + 1), Side: Sell, Price: price,
					TotalQty: 10, Type: Limit,
				}); err != nil {
					b.Fatal(err)
				}
			}
			id := int64(n + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := e.Submit(OrderParams{
					ClientID: id, Side: Buy, Price: 1,
					TotalQty: 1, Type: Limit,
				}); err != nil {
					b.Fatal(err)
				}
				if err := e.Cancel(id); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkSweepFixedTouches 固定只触及 1 个价位、1 个显示批，
// 而簿规模成倍增长；单次主动成交耗时应基本恒定。
func BenchmarkSweepFixedTouches(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		n := n
		b.Run(fmt.Sprintf("book=%d", n), func(b *testing.B) {
			e := New()
			for i := 0; i < n; i++ {
				if _, _, err := e.Submit(OrderParams{
					ClientID: int64(i + 1), Side: Sell, Price: int64(110 + i%400),
					TotalQty: 10, Type: Limit,
				}); err != nil {
					b.Fatal(err)
				}
			}
			base := int64(n + 1)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				refreshID := base + int64(i)
				if _, _, err := e.Submit(OrderParams{
					ClientID: refreshID, Side: Sell, Price: 110, TotalQty: 5, Type: Limit,
				}); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				_, fills, err := e.Submit(OrderParams{
					ClientID: base + 1_000_000 + int64(i), Side: Buy, Price: 110,
					TotalQty: 5, Type: Limit,
				})
				if err != nil || len(fills) != 1 || fills[0].Qty != 5 {
					b.Fatalf("unexpected fills=%d err=%v", len(fills), err)
				}
			}
		})
	}
}
