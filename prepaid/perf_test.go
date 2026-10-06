package prepaid

// 性能可验证性：若读数/充值的开销随历史读数与事件数量增长，
// 或友好时段判定的开销随节假日总数增长，则下列测试的规模
// 根本无法在时限内完成（平方级实现会慢几个数量级）。

import (
	"testing"
	"time"
)

func perfCfg() Config {
	return Config{
		InitialPriceMilli:  1000,
		WarnThreshold:      100,
		RestoreThreshold:   20,
		EmergencyAmount:    50,
		EmergencyThreshold: 10,
		ArrearsRatioNum:    1,
		ArrearsRatioDen:    2,
		ConfirmTimeout:     30,
		PeriodLength:       100000,
	}
}

// 30 万条读数与 3 万次充值：每次操作 O(1)（电价二分查找除外，
// 其规模是电价变更次数而非读数/事件数），总量应在线性时间内完成。
func TestReadingAndRechargeCostIndependentOfHistory(t *testing.T) {
	a, err := NewAccount(perfCfg())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	const n = 300000
	for i := 1; i <= n; i++ {
		if err := a.AddReading(int64(i), int64(i)*7); err != nil {
			t.Fatal(err)
		}
		if i%10 == 0 {
			if err := a.Recharge(int64(i), 1000); err != nil {
				t.Fatal(err)
			}
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d readings + %d recharges in %v (%.0f ops/s)", n, n/10, elapsed, float64(n+n/10)/elapsed.Seconds())
	if elapsed > 10*time.Second {
		t.Fatalf("cost grows with history: %v for %d ops", elapsed, n+n/10)
	}
	if !a.CheckInvariant() {
		t.Fatal("invariant violated")
	}
}

// 20 万个节假日下的友好时段判定与推迟计算：
// 节假日存于哈希集合，判定开销与节假日总数无关。
func TestFriendlyCheckCostIndependentOfHolidays(t *testing.T) {
	a, err := NewAccount(perfCfg())
	if err != nil {
		t.Fatal(err)
	}
	const nh = 200000
	for d := int64(0); d < nh; d++ {
		if err := a.AddHoliday(d * 3); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	var sink int64
	for i := int64(0); i < nh; i++ {
		ts := i * 9973
		if a.isFriendly(ts) {
			sink += a.friendlyEnd(ts)
		}
	}
	elapsed := time.Since(start)
	t.Logf("%d friendly checks against %d holidays in %v", nh, nh, elapsed)
	if elapsed > 10*time.Second {
		t.Fatalf("friendly check cost grows with holiday count: %v", elapsed)
	}
	_ = sink
}

func BenchmarkAddReading(b *testing.B) {
	a, _ := NewAccount(perfCfg())
	_ = a.Recharge(0, 1<<60)
	b.ResetTimer()
	for i := 1; i <= b.N; i++ {
		_ = a.AddReading(int64(i), int64(i)*7)
	}
}

func BenchmarkRecharge(b *testing.B) {
	a, _ := NewAccount(perfCfg())
	b.ResetTimer()
	for i := 1; i <= b.N; i++ {
		_ = a.Recharge(int64(i), 100)
	}
}

func BenchmarkIsFriendlyWithManyHolidays(b *testing.B) {
	a, _ := NewAccount(perfCfg())
	for d := int64(0); d < 100000; d++ {
		_ = a.AddHoliday(d * 2)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = a.isFriendly(int64(i) * 9973)
	}
}
