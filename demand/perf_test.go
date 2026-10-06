package demand_test

import (
	"testing"
	"time"

	"ontology/demand"
)

// TestHistoryLengthIndependence 以两档历史长度对照，验证稳态单次
// 上报+评估耗时不随历史上报次数增长（控制器只保留 W/slip 个重叠切片）。
func TestHistoryLengthIndependence(t *testing.T) {
	if testing.Short() {
		t.Skip("性能对照用例")
	}
	const timed = 2000
	short := steadyStateCost(t, 1000, timed)
	long := steadyStateCost(t, 100000, timed)
	t.Logf("稳态单次上报+评估: 历史1k=%v/次 历史100k=%v/次 比值=%.2f",
		short, long, float64(long.Nanoseconds())/float64(short.Nanoseconds()))
	// 允许常数抖动，但不允许出现随历史线性增长（100 倍）的量级。
	if long > short*10 {
		t.Fatalf("评估耗时疑似随历史增长: %v vs %v", short, long)
	}
}

func steadyStateCost(t *testing.T, reports, timed int64) time.Duration {
	t.Helper()
	cfg := demand.Config{ContractKW: 100, WindowSec: 600, SlipSec: 10, MaxPowerKW: 100000}
	c, err := demand.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 12; i++ {
		_ = c.AddLoad(0, mkLoad(i, 8, i, 0, 0))
	}
	report := func(k int64) {
		power := int64(90)
		if k%37 == 0 {
			power = 160 // 周期性越限，触发切除/恢复路径
		}
		if _, err := c.Report(k, power); err != nil {
			t.Fatal(err)
		}
	}
	for k := int64(1); k <= reports; k++ {
		report(k)
	}
	start := time.Now()
	for k := reports + 1; k <= reports+timed; k++ {
		report(k)
	}
	return time.Since(start) / time.Duration(timed)
}
