package system

import (
	"fmt"
	"testing"
	"time"

	"ontology/calendar"
	"ontology/domain"
)

// buildLarge 构造 n 个对象（设备+附件各半），其中 hit 个附件的到期日
// 落在预警窗口内，其余对象到期日都在窗口之外。
func buildLarge(tb testing.TB, n, hit int) *System {
	tb.Helper()
	s, err := New(testConfigs())
	if err != nil {
		tb.Fatalf("New: %v", err)
	}
	base := calendar.FromCivil(2024, 1, 1)
	// 先登记全部设备与附件：首次检验日拉开，使绝大多数对象到期日远离查询窗口。
	// 安全阀周期 6 个月；令第 i 个对象首次检验日为 base+i 天，
	// 则到期日均匀散布在 6 个月（约 180 天）带上，窗口 30 天。
	for i := 0; i < n; i++ {
		if i%2 == 0 {
			if err := s.Register(fmt.Sprintf("D%06d", i), domain.CatBoiler, base+i); err != nil {
				tb.Fatalf("register: %v", err)
			}
		} else {
			if err := s.Register(fmt.Sprintf("A%06d", i), domain.CatSafetyValve, base+i); err != nil {
				tb.Fatalf("register: %v", err)
			}
		}
	}
	return s
}

// TestWarnScales 两档对象总数对照：预警查询耗时不随总数线性增长。
// （严格的比较次数证明见 index 包测试；此处用墙钟时间佐证。）
func TestWarnScales(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	queryDate := calendar.FromCivil(2024, 1, 1) + 200 // 命中窗口内约 30 个对象
	var prev time.Duration
	for _, n := range []int{10_000, 100_000} {
		s := buildLarge(t, n, 0)
		// 预热 + 计时
		if _, err := s.Warn(queryDate); err != nil {
			t.Fatalf("warn: %v", err)
		}
		const reps = 200
		start := time.Now()
		var hits int
		for i := 0; i < reps; i++ {
			entries, _ := s.Warn(queryDate)
			hits = len(entries)
		}
		elapsed := time.Since(start) / reps
		t.Logf("n=%d 命中=%d 单次预警耗时=%v", n, hits, elapsed)
		if prev > 0 && elapsed > prev*10 {
			t.Fatalf("预警耗时随总量线性增长? 上档 %v, 本档 %v", prev, elapsed)
		}
		prev = elapsed
	}
}

// TestUsableScales 两档对象总数对照：可使用判定耗时只与设备附件数相关。
func TestUsableScales(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	var prev time.Duration
	for _, n := range []int{10_000, 100_000} {
		s := buildLarge(t, n, 0)
		// 先给目标设备与附件续期（此时它们已超期，检验后以检验日为基准重算）
		renew := calendar.FromCivil(2024, 1, 1) + n + 1
		for _, id := range []string{"D000000", "A000001", "A000003", "A000005"} {
			must(t, s.Inspect(id, renew, domain.ResultPass, 0))
		}
		// 给设备 D000000 挂 3 个附件
		must(t, s.Attach("D000000", "A000001", renew+1))
		must(t, s.Attach("D000000", "A000003", renew+2))
		must(t, s.Attach("D000000", "A000005", renew+3))
		qdate := renew + 4
		if _, _, err := s.Usable("D000000", qdate); err != nil {
			t.Fatalf("usable: %v", err)
		}
		const reps = 1000
		start := time.Now()
		for i := 0; i < reps; i++ {
			if _, _, err := s.Usable("D000000", qdate); err != nil {
				t.Fatalf("usable: %v", err)
			}
		}
		elapsed := time.Since(start) / reps
		t.Logf("n=%d 单次可使用判定耗时=%v", n, elapsed)
		if prev > 0 && elapsed > prev*10 {
			t.Fatalf("可使用判定耗时随总量线性增长? 上档 %v, 本档 %v", prev, elapsed)
		}
		prev = elapsed
	}
}
