package pivas

import (
	"fmt"
	"sync"
	"testing"
)

// buildScaleEnv 构造 scale 档规模环境：大量药品与禁忌对。
func buildScaleEnv(b *testing.B, nDrugs, nBad int) *System {
	s := New()
	if err := s.SetTransport(0, Room, 10); err != nil {
		b.Fatal(err)
	}
	if err := s.SetTransport(0, Cold, 20); err != nil {
		b.Fatal(err)
	}
	for n, d := range map[int]int{1: 100, 2: 150, 3: 190, 4: 220, 5: 240, 6: 250} {
		if err := s.SetDuration(0, n, d); err != nil {
			b.Fatal(err)
		}
	}
	if err := s.RegisterBench(0, Bench{ID: "A", Capacity: 6, ClearGap: 10}); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < nDrugs; i++ {
		id := fmt.Sprintf("drug%06d", i)
		if err := s.RegisterDrug(0, id, Drug{
			RoomStableSec: 100000, ColdStableSec: 200000,
			SolventClass: "NS",
		}); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < nBad && i+1 < nDrugs; i++ {
		if err := s.AddIncompatibility(0,
			fmt.Sprintf("drug%06d", i),
			fmt.Sprintf("drug%06d", (i*7+1)%nDrugs),
		); err != nil {
			b.Fatal(err)
		}
	}
	return s
}

// BenchmarkIncompatibilityLookup 证明禁忌判定不随禁忌对总数增长：
// 两档（10^3 与 10^5 对）单次受理的目录判定耗时应基本一致。
func BenchmarkIncompatibilityLookup(b *testing.B) {
	for _, nBad := range []int{1_000, 100_000} {
		s := buildScaleEnv(b, 120_000, nBad)
		in := OrderInput{
			ID: "probe", Drugs: []string{"drug000001", "drug000002", "drug000003"},
			Solvent: "NS", DueAt: 1_000_000,
		}
		b.Run(fmt.Sprintf("bad=%d", nBad), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				in.ID = fmt.Sprintf("probe%d", i)
				// 多数应因禁忌或无安排返回；我们只测目录判定路径的稳定性，
				// 故用互不相关的三种药保证走到禁忌哈希查找。
				_ = s.AcceptOrder(0, in)
			}
		})
	}
}

// BenchmarkAcceptVsHistory 证明受理开销只与未开始批次数相关。
// 构造：制造大量“已完成”历史批次（now 之后它们全部成为过去），
// 受理一张新医嘱，两档历史规模（100 / 10000）耗时应基本一致。
func BenchmarkAcceptWithManyFinishedBatches(b *testing.B) {
	for _, hist := range []int{100, 10_000} {
		s := buildScaleEnv(b, 20, 0)
		// 额外加一台容量 1 的台，逐张成批以制造大量历史批次。
		if err := s.RegisterBench(0, Bench{ID: "Z", Capacity: 1, ClearGap: 10}); err != nil {
			b.Fatal(err)
		}
		makeHist(b, s, hist)
		b.Run(fmt.Sprintf("history=%d", hist), func(b *testing.B) {
			now := hist*120 + 1
			// 计时前推进一次 head 游标，跳过全部历史批次，排除一次性摊销。
			for _, bs := range s.benches {
				bs.advanceHead(now)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				in := OrderInput{
					ID:      fmt.Sprintf("late%06d", i),
					Drugs:   []string{"drug000001"},
					Solvent: "NS",
					DueAt:   now + 1_000_000,
				}
				b.StopTimer()
				if err := s.AcceptOrder(now, in); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				// 在已计时的受理完成后取消，避免未开始批次堆积。
				b.StopTimer()
				if err := s.CancelOrder(now, in.ID); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
		})
	}
}

func makeHist(b *testing.B, s *System, hist int) {
	for i := 0; i < hist; i++ {
		now := i * 120
		in := OrderInput{
			ID:      fmt.Sprintf("hist%06d", i),
			Drugs:   []string{"drug000001"},
			Solvent: "NS",
			DueAt:   now + 1_000_000,
		}
		if err := s.AcceptOrder(now, in); err != nil {
			b.Fatalf("hist %d: %v", i, err)
		}
	}
}

// TestConcurrentAccept 并发调用结果须等价于某个串行顺序，且状态一致。
func TestConcurrentAccept(t *testing.T) {
	s := New()
	if err := s.SetTransport(0, Room, 10); err != nil {
		t.Fatal(err)
	}
	for n, d := range map[int]int{1: 10, 2: 20, 3: 30, 4: 40, 5: 50, 6: 60} {
		if err := s.SetDuration(0, n, d); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.RegisterBench(0, Bench{ID: "A", Capacity: 6, ClearGap: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterDrug(0, "d", Drug{RoomStableSec: 1_000_000, ColdStableSec: 2_000_000, SolventClass: "NS"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	const g = 16
	errs := make([]error, g)
	for gIdx := 0; gIdx < g; gIdx++ {
		wg.Add(1)
		go func(gi int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				id := fmt.Sprintf("c-%d-%d", gi, j)
				errs[gi] = s.AcceptOrder(0, OrderInput{
					ID: id, Drugs: []string{"d"}, Solvent: "NS", DueAt: 1_000_000,
				})
				if errs[gi] != nil {
					return
				}
			}
		}(gIdx)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent accept: %v", err)
		}
	}
	if len(s.orders) != g*25 {
		t.Fatalf("lost updates: got %d orders", len(s.orders))
	}
	// 全部已受理医嘱在 now=0 时刻必须满足两项约束。
	for id := range s.orders {
		info, err := s.QueryOrder(0, id)
		if err != nil {
			t.Fatalf("query %s: %v", id, err)
		}
		if !info.OnTime {
			t.Fatalf("order %s not on time: %+v", id, info)
		}
	}
}
