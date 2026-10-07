package slots

import (
	"fmt"
	"testing"
	"time"
)

// perfCfg 返回可伸缩周数的配置。
func perfCfg(totalWeeks int) Config {
	cfg := stdCfg()
	cfg.TotalWeeks = totalWeeks
	cfg.Capacity = 1
	return cfg
}

// buildReturnScenario 构造一次返还触发等候名单分配的场景：
// A 持有 (1,10) 第 1-2 周，B/C/D 的申请在等候名单上；
// extra 为额外填充在无关小时段上的系列数，用于放大机场内系列总数。
func buildReturnScenario(t *testing.T, totalWeeks, extra int) *Coordinator {
	t.Helper()
	cfg := perfCfg(totalWeeks)
	c := newCoord(t, cfg)
	at := day(1)
	mustSubmit(t, c, at, "A", 1, 10, 1, 2)
	mustSubmit(t, c, at.Add(1), "B", 1, 10, 1, 2)
	mustSubmit(t, c, at.Add(2), "C", 1, 10, 1, 1)
	mustSubmit(t, c, at.Add(3), "D", 1, 10, 1, 2)
	for i := 0; i < extra; i++ {
		// 填充无关单元格：不同星期几/小时段，互不争用。
		wd, h := (i/24)%7, i%24
		if wd == 1 && h == 10 {
			h = 11 // 避开被测槽位 (1,10)
		}
		mustSubmit(t, c, at.Add(time.Duration(4+i)), fmt.Sprintf("E%d", i), wd, h, 1, 2)
	}
	mustAllocate(t, c, day(9))
	return c
}

// 判定能否再分配与返还触发的等候名单分配，其开销（以容量判定次数计）
// 不随机场内系列总数与航季总周数增长。
func TestReturnPromotionCostIndependentOfScale(t *testing.T) {
	type scenario struct {
		totalWeeks int
		extra      int
	}
	var deltas []int64
	for _, s := range []scenario{{4, 0}, {4, 50}, {40, 0}, {40, 200}} {
		c := buildReturnScenario(t, s.totalWeeks, s.extra)
		c.cellChecks = 0
		if err := c.Return(day(15), "A", 0, []int{1}); err != nil {
			t.Fatalf("Return: %v", err)
		}
		deltas = append(deltas, c.cellChecks)
	}
	for i, d := range deltas {
		if d != deltas[0] {
			t.Fatalf("cell checks grow with scale: %v (index %d)", deltas, i)
		}
	}
	t.Logf("capacity checks per return-triggered promotion: %d (constant)", deltas[0])
}

// 计算单个系列使用率的开销（以周扫描次数计）只随系列周数增长。
func TestUsageCostIndependentOfScale(t *testing.T) {
	cfg := perfCfg(40)
	cfg.Capacity = 4
	c := newCoord(t, cfg)
	at := day(1)
	short := mustSubmit(t, c, at, "A", 1, 10, 1, 3)
	long := mustSubmit(t, c, at.Add(1), "B", 2, 10, 1, 30)
	for i := 0; i < 100; i++ {
		mustSubmit(t, c, at.Add(time.Duration(2+i)), fmt.Sprintf("E%d", i), (3+i)%7, i%24, 1, 2)
	}
	mustAllocate(t, c, day(9))

	c.weeksScanned = 0
	if _, _, _, err := c.Usage(short); err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if got := c.weeksScanned; got != 3 {
		t.Fatalf("weeks scanned for 3-week series = %d, want 3", got)
	}
	c.weeksScanned = 0
	if _, _, _, err := c.Usage(long); err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if got := c.weeksScanned; got != 30 {
		t.Fatalf("weeks scanned for 30-week series = %d, want 30", got)
	}
}

// 基准：随系列总数增长，返还触发的等候名单分配耗时应保持平稳。
func BenchmarkReturnPromotion(b *testing.B) {
	for _, extra := range []int{0, 100, 1000} {
		b.Run(fmt.Sprintf("extra=%d", extra), func(b *testing.B) {
			t := &testing.T{}
			for i := 0; i < b.N; i++ {
				c := buildReturnScenario(t, 4, extra)
				_ = c.Return(day(15), "A", 0, []int{1})
			}
		})
	}
}
