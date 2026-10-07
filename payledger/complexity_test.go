package payledger

import (
	"fmt"
	"testing"
)

// 结构化验证：全部授权过期并结算后，账户的活跃侧缓存为空，
// 后续查询只读取常量字段（liveTotal），不做任何逐授权/逐账户扫描。
func TestQueryDoesNotScanAfterReap(t *testing.T) {
	l := NewLedger(Config{ExpiryDays: 1, ToleranceBps: 0})
	mustOK(t, l.CreateAccount("c1", 1<<50, 0))
	const n = 100000
	for i := 0; i < n; i++ {
		mustOK(t, l.Authorize("c1", fmt.Sprintf("a%d", i), 1, int64(i)))
	}
	// 一次远期查询触发全部结算。
	if _, err := l.Available("c1", int64(n)+10); err != nil {
		t.Fatal(err)
	}
	acct := l.accounts["c1"]
	if len(acct.tracker.buckets) != 0 || len(acct.tracker.days) != 0 || acct.tracker.liveTotal != 0 {
		t.Fatalf("结算后活跃侧应为空: buckets=%d days=%d live=%d",
			len(acct.tracker.buckets), len(acct.tracker.days), acct.tracker.liveTotal)
	}
	// 后续查询不再触碰堆与桶（结构保持为空），结果恒定。
	for q := int64(0); q < 5; q++ {
		av, err := l.Available("c1", int64(n)+10+q)
		if err != nil || av != 1<<50 {
			t.Fatalf("查询异常: %d %v", av, err)
		}
		if len(acct.tracker.days) != 0 {
			t.Fatalf("查询触发了额外的堆活动")
		}
	}
}

// 结构化验证：查询只访问目标账户的跟踪器，不触碰其他账户。
func TestQueryDoesNotScanAccounts(t *testing.T) {
	l := NewLedger(Config{ExpiryDays: 1, ToleranceBps: 0})
	const m = 1000
	for i := 0; i < m; i++ {
		id := fmt.Sprintf("c%d", i)
		mustOK(t, l.CreateAccount(id, 100, int64(i)))
		mustOK(t, l.Authorize(id, fmt.Sprintf("a%d", i), 50, int64(i)))
	}
	// 记录其他账户的水位，查询 c0 后它们不得变化。
	type wm struct {
		has bool
		day int64
	}
	before := make(map[string]wm)
	for i := 1; i < m; i++ {
		id := fmt.Sprintf("c%d", i)
		tr := l.accounts[id].tracker
		before[id] = wm{tr.hasWM, tr.watermark}
	}
	if _, err := l.Available("c0", 1<<40); err != nil {
		t.Fatal(err)
	}
	for i := 1; i < m; i++ {
		id := fmt.Sprintf("c%d", i)
		tr := l.accounts[id].tracker
		if tr.hasWM != before[id].has || tr.watermark != before[id].day {
			t.Fatalf("查询 c0 触碰了账户 %s 的跟踪器", id)
		}
	}
}

// 基准：可用额度查询 vs 历史授权总数。全部授权过期并结算后，
// 查询耗时应与 N 无关（常数）。运行：
//
//	go test -bench=AvailableVsHistory -benchmem ./payledger
func BenchmarkAvailableVsHistory(b *testing.B) {
	for _, n := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			l := NewLedger(Config{ExpiryDays: 1, ToleranceBps: 0})
			_ = l.CreateAccount("c1", 1<<50, 0)
			for i := 0; i < n; i++ {
				_ = l.Authorize("c1", fmt.Sprintf("a%d", i), 1, int64(i))
			}
			now := int64(n) + 10
			_, _ = l.Available("c1", now) // 触发结算
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = l.Available("c1", now)
			}
		})
	}
}

// 基准：可用额度查询 vs 账户总数。
func BenchmarkAvailableVsAccounts(b *testing.B) {
	for _, m := range []int{100, 100000} {
		b.Run(fmt.Sprintf("M=%d", m), func(b *testing.B) {
			l := NewLedger(Config{ExpiryDays: 1, ToleranceBps: 0})
			for i := 0; i < m; i++ {
				id := fmt.Sprintf("c%d", i)
				_ = l.CreateAccount(id, 100, int64(i))
				_ = l.Authorize(id, fmt.Sprintf("a%d", i), 50, int64(i))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = l.Available("c0", 1<<40)
			}
		})
	}
}

// 基准：含活跃持有的前向查询（均摊结算成本）。
func BenchmarkAvailableAmortized(b *testing.B) {
	l := NewLedger(Config{ExpiryDays: 7, ToleranceBps: 0})
	_ = l.CreateAccount("c1", 1<<50, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = l.Authorize("c1", fmt.Sprintf("a%d", i), 1, int64(i))
		_, _ = l.Available("c1", int64(i))
	}
}
