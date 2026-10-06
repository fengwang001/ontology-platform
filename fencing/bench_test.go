package fencing

import (
	"strconv"
	"testing"
)

// 确定性证明：已结束的历史事件被物理清理，区域结构规模有界，
// 因此可达判定的开销不随历史事件数增长。
func TestPruningKeepsNoHistory(t *testing.T) {
	s := NewSystem()
	const cellCount = 200000
	cells := make([]Cell, 0, cellCount)
	for i := 0; i < cellCount; i++ {
		cells = append(cells, Cell{ID: "c" + strconv.Itoa(i), Ring: i % 8})
	}
	mustOK(t, s.RegisterMerchant(0, "m", "r", cells))
	const eventCount = 50000
	for i := 0; i < eventCount; i++ {
		mustOK(t, s.RegisterEvent(0, "e"+strconv.Itoa(i), "r", 0, 1, i%5))
	}
	// 推进已提交时钟越过全部事件的结束时刻，触发清理。
	mustOK(t, s.SetMerchantLevel(2, "m", 0))
	r := s.regions["r"]
	if got := len(r.events); got != 0 {
		t.Fatalf("live events after prune = %d, want 0 (history must be discarded)", got)
	}
	// 清理后可达判定仍然正确。
	mustReachability(t, s, "m", "c7", ReachableNow) // 环距 7 = 基础半径
	mustReachability(t, s, "m", "ghost", UnreachablePermanent)
}

// 基准：可达判定耗时与基础范围单元总数无关。
// 运行：go test -bench=ReachableVsCellCount -benchtime=100000x ./fencing
func BenchmarkReachableVsCellCount(b *testing.B) {
	for _, n := range []int{1_000, 100_000, 1_000_000} {
		b.Run(strconv.Itoa(n)+"_cells", func(b *testing.B) {
			s := NewSystem()
			cells := make([]Cell, 0, n)
			for i := 0; i < n; i++ {
				cells = append(cells, Cell{ID: "c" + strconv.Itoa(i), Ring: i % 8})
			}
			if err := s.RegisterMerchant(0, "m", "r", cells); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Reachable("m", "c123"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 基准：可达判定耗时与已结束的历史收缩事件数量无关。
// 运行：go test -bench=ReachableVsEndedEvents -benchtime=100000x ./fencing
func BenchmarkReachableVsEndedEvents(b *testing.B) {
	for _, n := range []int{0, 10_000, 100_000} {
		b.Run(strconv.Itoa(n)+"_ended_events", func(b *testing.B) {
			s := NewSystem()
			if err := s.RegisterMerchant(0, "m", "r", ringCells("c", 4)); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < n; i++ {
				if err := s.RegisterEvent(0, "e"+strconv.Itoa(i), "r", 0, 1, i%5); err != nil {
					b.Fatal(err)
				}
			}
			// 推进已提交时钟，清理全部历史事件。
			if err := s.SetMerchantLevel(2, "m", 0); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Reachable("m", "c2"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
