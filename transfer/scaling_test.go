package transfer

import (
	"fmt"
	"testing"
)

// 收货、关闭与找回的判定开销只与单自身规模有关。
// 以下基准在不同 单总数 × 仓库数 × 商品数 下测量同一操作，
// 平坦的 ns/op 即为与全局规模无关的可验证证据：
//
//	go test ./transfer -bench 'Scaling' -benchtime 100x

func buildScalingSystem(b *testing.B, orders, warehouses, products int) (*System, []string) {
	b.Helper()
	initial := map[string]map[string]int64{}
	for w := 0; w < warehouses; w++ {
		stocks := map[string]int64{}
		for p := 0; p < products; p++ {
			stocks[fmt.Sprintf("P%d", p)] = 1 << 40
		}
		initial[fmt.Sprintf("W%d", w)] = stocks
	}
	s, err := NewSystem(Config{OverReceiptTolerancePermille: 100, CloseWaitSeconds: 0}, initial)
	if err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 0, orders)
	var now int64
	for i := 0; i < orders; i++ {
		id := fmt.Sprintf("T%d", i)
		if err := s.CreateOrder(now, id, "W0", "W1", []Line{
			{Product: "P0", Qty: 100},
			{Product: "P1", Qty: 100},
		}); err != nil {
			b.Fatal(err)
		}
		now++
		if err := s.ShipOrder(now, id); err != nil {
			b.Fatal(err)
		}
		now++
		ids = append(ids, id)
	}
	return s, ids
}

func benchmarkScaling(b *testing.B, run func(s *System, ids []string, i int)) {
	for _, scale := range []struct{ orders, warehouses, products int }{
		{1_000, 10, 10},
		{10_000, 100, 100},
		{100_000, 100, 1_000},
	} {
		b.Run(fmt.Sprintf("orders=%d/wh=%d/prod=%d", scale.orders, scale.warehouses, scale.products), func(b *testing.B) {
			s, ids := buildScalingSystem(b, scale.orders, scale.warehouses, scale.products)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				run(s, ids, i%len(ids))
			}
		})
	}
}

func BenchmarkReceiveScaling(b *testing.B) {
	var now int64 = 1 << 30
	benchmarkScaling(b, func(s *System, ids []string, i int) {
		now++
		s.Receive(now, ids[i], 0, 1) // 容忍 1：第二次起超收被拒，开销相同
	})
}

func BenchmarkCloseScaling(b *testing.B) {
	var now int64 = 1 << 30
	benchmarkScaling(b, func(s *System, ids []string, i int) {
		now++
		s.CloseOrder(now, ids[i]) // 第二次起状态错误被拒，开销相同
	})
}

func BenchmarkRecoverScaling(b *testing.B) {
	var now int64 = 1 << 30
	benchmarkScaling(b, func(s *System, ids []string, i int) {
		now++
		s.Recover(now, ids[i], 0, 1) // 未关闭，预期状态错误，开销相同
	})
}
