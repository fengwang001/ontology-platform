package dtc_test

import (
	"fmt"
	"testing"
	"time"

	"ontology/dtc"
)

// driveCycles 驱动 n 个点火循环（每循环：点火开、采样、两次上报、点火关），
// 返回最后一个循环点火关结算的耗时累加器。
func driveCycles(t *testing.T, m *dtc.Manager, tm *int64, n int, measureSettle bool) time.Duration {
	t.Helper()
	var settle time.Duration
	for i := 0; i < n; i++ {
		evs := []dtc.Event{
			{Kind: dtc.EventIgnitionOn},
			{Kind: dtc.EventEnvironmentSample, Speed: 60, CoolantTemp: 80},
			{Kind: dtc.EventMonitorResult, DTCCode: 1, Passed: true},
			{Kind: dtc.EventMonitorResult, DTCCode: 2},
			{Kind: dtc.EventMonitorResult, DTCCode: 3, Passed: true},
		}
		for _, ev := range evs {
			*tm++
			ev.Time = *tm
			ev.Odometer = *tm
			if err := m.Handle(ev); err != nil {
				t.Fatalf("event %+v: %v", ev, err)
			}
		}
		*tm++
		off := dtc.Event{Kind: dtc.EventIgnitionOff, Time: *tm, Odometer: *tm}
		start := time.Now()
		if err := m.Handle(off); err != nil {
			t.Fatalf("ignition off: %v", err)
		}
		if measureSettle {
			settle += time.Since(start)
		}
	}
	return settle
}

// TestSettlementCostIndependentOfHistory 历史长度两档对照：
// 分别积累 2 千与 20 万个历史循环后，测量点火关结算平均耗时。
// 实现只保存计数器不保存历史，两档耗时应处于同一量级。
func TestSettlementCostIndependentOfHistory(t *testing.T) {
	cfg := testConfig()
	const measureCycles = 2000

	measure := func(historyCycles int) time.Duration {
		m, err := dtc.NewManager(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for i, sev := range []int{1, 2, 3} {
			if err := m.RegisterDTC(i+1, sev); err != nil {
				t.Fatal(err)
			}
		}
		var tm int64
		driveCycles(t, m, &tm, historyCycles, false)
		// 预热后取三轮测量的最小值，降低调度噪声。
		best := time.Duration(1<<63 - 1)
		for rep := 0; rep < 3; rep++ {
			if d := driveCycles(t, m, &tm, measureCycles, true); d < best {
				best = d
			}
		}
		return best / measureCycles
	}

	short := measure(2_000)
	long := measure(200_000)
	t.Logf("点火关结算平均耗时：历史 2k 循环=%v，历史 200k 循环=%v（比值 %.2f）",
		short, long, float64(long)/float64(short))

	// 宽松上界：允许常数因子差异与计时噪声，但拒绝随历史增长的复杂度。
	if long > short*4+2*time.Microsecond {
		t.Fatalf("settlement cost grows with history: short=%v long=%v", short, long)
	}
}

// BenchmarkIgnitionOffSettlement 两档历史长度下的点火关结算基准。
func BenchmarkIgnitionOffSettlement(b *testing.B) {
	cfg := testConfig()
	for _, history := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("history=%d", history), func(b *testing.B) {
			m, err := dtc.NewManager(cfg)
			if err != nil {
				b.Fatal(err)
			}
			for i, sev := range []int{1, 2, 3} {
				if err := m.RegisterDTC(i+1, sev); err != nil {
					b.Fatal(err)
				}
			}
			tm := int64(0)
			step := func(ev dtc.Event) {
				tm++
				ev.Time = tm
				ev.Odometer = tm
				if err := m.Handle(ev); err != nil {
					b.Fatal(err)
				}
			}
			for i := 0; i < history; i++ {
				step(dtc.Event{Kind: dtc.EventIgnitionOn})
				step(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: 1, Passed: true})
				step(dtc.Event{Kind: dtc.EventIgnitionOff})
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				step(dtc.Event{Kind: dtc.EventIgnitionOn})
				step(dtc.Event{Kind: dtc.EventMonitorResult, DTCCode: 1, Passed: true})
				step(dtc.Event{Kind: dtc.EventIgnitionOff})
			}
		})
	}
}
