package cal

import (
	"testing"
	"time"
)

// B=10^9 工作分钟、100 个节假日：推进算法必须按周批量跳，不可逐日扫描。
func TestAdvanceLargeBudget(t *testing.T) {
	c := New(540, 1080, 0b0011111)
	for i := int64(2); i <= 400; i += 4 {
		if err := c.AddHoliday(i, (i-1)*1440); err != nil {
			t.Fatalf("add holiday %d: %v", i, err)
		}
	}
	start := time.Now()
	r, ok := c.advanceLocked(600, 1_000_000_000)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("advance took %s, must not scan day by day", d)
	}
	if !ok || r <= 0 {
		t.Fatalf("1e9 work minutes with 9h/day should be reachable within 1e12, r=%d ok=%v", r, ok)
	}
	if w := c.WorkLocked(600, r); w != 1_000_000_000 {
		t.Fatalf("Work to deadline=%d want 1e9", w)
	}
	t.Logf("B=1e9 with 100 holidays reached at %d in %v; Work inverse matches", r, time.Since(start))

	// 0 个节假日、能在范围内到达：约 1e9/(540*5)=370370 天 ≈ 5.3e8，落在 1e12 内
	c2 := New(0, 1440, 127)
	r2, ok2 := c2.advanceLocked(0, 1_000_000_000)
	if !ok2 || r2 != 1_000_000_000 {
		t.Fatalf("all-day all-week advance=%d,%v want 1e9", r2, ok2)
	}

	// 窄窗口（每天 1 分钟）下 1e9 工作分钟需 ~1.44e12，超出 1e12
	c3 := New(0, 1, 1)
	if r3, ok3 := c3.advanceLocked(0, 1_000_000_000); ok3 || r3 != 0 {
		t.Fatalf("narrow window must report out-of-range, got %d,%v", r3, ok3)
	}

	// 工作日 9 小时、恰好少量 B 验证推进点与 Work 互逆
	r3, ok3 := c.AdvanceLocked(3000*1440+540, 1_000_000)
	if !ok3 || c.WorkLocked(3000*1440+540, r3) != 1_000_000 {
		t.Fatalf("roundtrip advance/work mismatch r=%d ok=%v", r3, ok3)
	}
	t.Logf("1e6 minutes advance from day3000 => %d, Work inverse matches", r3)
}
