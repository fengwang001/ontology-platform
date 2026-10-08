package gateway

import (
	"fmt"
	"testing"
	"time"
)

// fillHistory 向车辆 v1 填充 n 条已终结指令的历史。
func fillHistory(b testing.TB, g *Gateway, n int) {
	b.Helper()
	tm := g.ClockNow()
	for i := 0; i < n; i++ {
		tm++
		// 周期上报保持状态新鲜。
		if i%50 == 0 {
			if _, err := g.ReportState("v1", awakeReport(int64(i/50+2), tm)); err != nil {
				b.Fatalf("report: %v", err)
			}
			tm++
		}
		res, err := g.SubmitCommand(SubmitRequest{
			VehicleID: "v1", Submitter: "bench", RequestID: fmt.Sprintf("h-%d", i),
			Type: CmdFindCar, Time: tm, ValiditySec: 100000000,
		})
		if err != nil {
			b.Fatalf("submit history %d: %v", i, err)
		}
		tm++
		if _, err := g.Ack("v1", res.CommandID, tm, true); err != nil {
			b.Fatalf("ack history %d: %v", i, err)
		}
	}
}

// TestAcceptanceCostIndependentOfHistory 验证受理开销不随历史指令总数增长：
// 在同一车辆上持续受理-回执，比较前段与后段的平均耗时。
func TestAcceptanceCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip("short 模式跳过耗时测试")
	}
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))

	const total = 100000
	const window = 10000
	tm := int64(1)
	var firstWindow, lastWindow time.Duration
	for i := 0; i < total; i++ {
		start := time.Now()
		tm++
		if i%50 == 0 {
			mustReport(t, g, "v1", awakeReport(int64(i/50+2), tm))
			tm++
		}
		res, err := g.SubmitCommand(SubmitRequest{
			VehicleID: "v1", Submitter: "t", RequestID: fmt.Sprintf("r-%d", i),
			Type: CmdFindCar, Time: tm, ValiditySec: 100000000,
		})
		if err != nil {
			t.Fatalf("op %d: %v", i, err)
		}
		tm++
		if _, err := g.Ack("v1", res.CommandID, tm, true); err != nil {
			t.Fatalf("ack %d: %v", i, err)
		}
		elapsed := time.Since(start)
		if i < window {
			firstWindow += elapsed
		}
		if i >= total-window {
			lastWindow += elapsed
		}
	}
	avgFirst := firstWindow / window
	avgLast := lastWindow / window
	t.Logf("历史 %d 条: 前 %d 次平均 %v, 后 %d 次平均 %v", total, window, avgFirst, window, avgLast)
	// 宽松上界：允许常数倍的测量噪声，但不容忍随历史线性增长。
	if avgLast > avgFirst*10+5*time.Microsecond {
		t.Fatalf("受理开销随历史增长: 前段 %v, 后段 %v", avgFirst, avgLast)
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.CommandTotal != total {
		t.Fatalf("历史指令数应为 %d, got %d", total, snap.CommandTotal)
	}
}

// BenchmarkSteadyState 不同历史规模下的稳态受理-回执开销。
// 验证受理与状态判定开销与历史指令总数无关（O(1)）。
func BenchmarkSteadyState(b *testing.B) {
	for _, history := range []int{0, 1000, 100000} {
		b.Run(fmt.Sprintf("history_%d", history), func(b *testing.B) {
			g, err := NewGateway(testConfig())
			if err != nil {
				b.Fatal(err)
			}
			if _, err := g.ReportState("v1", awakeReport(1, 0)); err != nil {
				b.Fatal(err)
			}
			fillHistory(b, g, history)
			tm := g.ClockNow()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tm++
				if i%50 == 0 {
					if _, err := g.ReportState("v1", awakeReport(int64(history/50+i/50+2), tm)); err != nil {
						b.Fatal(err)
					}
					tm++
				}
				res, err := g.SubmitCommand(SubmitRequest{
					VehicleID: "v1", Submitter: "bench", RequestID: fmt.Sprintf("b-%d", i),
					Type: CmdFindCar, Time: tm, ValiditySec: 100000000,
				})
				if err != nil {
					b.Fatal(err)
				}
				tm++
				if _, err := g.Ack("v1", res.CommandID, tm, true); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
