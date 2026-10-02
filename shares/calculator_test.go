package shares_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/shares"
)

func mustCalc(t *testing.T, wSlow, total, capPer int64) *shares.Calculator {
	t.Helper()
	c, err := shares.NewCalculator(wSlow, total, capPer)
	if err != nil {
		t.Fatalf("NewCalculator(%d, %d, %d) 报错: %v", wSlow, total, capPer, err)
	}
	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("操作报错: %v", err)
	}
}

func mustErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("错误 = %v, 期望 %v", err, want)
	}
}

func sh(kv ...any) []shares.Share {
	out := make([]shares.Share, 0, len(kv)/2)
	for i := 0; i < len(kv); i += 2 {
		out = append(out, shares.Share{ID: kv[i].(string), Value: int64(kv[i+1].(int))})
	}
	return out
}

func mustShares(t *testing.T, c *shares.Calculator, now int64, want []shares.Share) {
	t.Helper()
	got, err := c.Shares(now)
	if err != nil {
		t.Fatalf("Shares(%d) 报错: %v", now, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Shares(%d) = %v, 期望 %v", now, got, want)
	}
	var sum int64
	for _, s := range got {
		if s.Value < 0 {
			t.Fatalf("Shares(%d) 出现负数份额: %v", now, got)
		}
		sum += s.Value
	}
}

func mustSharesErr(t *testing.T, c *shares.Calculator, now int64, want error) {
	t.Helper()
	got, err := c.Shares(now)
	if !errors.Is(err, want) {
		t.Fatalf("Shares(%d) 错误 = %v, 期望 %v (got=%v)", now, err, want, got)
	}
}

// 规格例 1：Wslow=100、T=10000、Y=10000，a(w10, join 0)、b(w10, join 50)，
// now=100 时 eff 为 10 与 5，E=15，底数 6666/3333，余数 10 对 5，差额 1 给 a。
func TestSpecExampleBasic(t *testing.T) {
	c := mustCalc(t, 100, 10_000, 10_000)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	mustShares(t, c, 100, sh("a", 6667, "b", 3333))
}

// 规格例 2：b 在 now=100 才登记，e=0，eff 取 max(1,0)=1，E=11，
// 底数 9090/909，余数 10 对 1，差额 1 给 a。
func TestSpecExampleJoinAtZero(t *testing.T) {
	c := mustCalc(t, 100, 10_000, 10_000)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 100))
	mustShares(t, c, 100, sh("a", 9091, "b", 909))
}

// 规格例 3：三台权重均为 1，余数并列，差额 1 给 id 最小者。
func TestSpecExampleTieBreak(t *testing.T) {
	c := mustCalc(t, 100, 10_000, 10_000)
	mustOK(t, c.AddHost("a", 1, 0))
	mustOK(t, c.AddHost("b", 1, 0))
	mustOK(t, c.AddHost("c", 1, 0))
	mustShares(t, c, 100, sh("a", 3334, "b", 3333, "c", 3333))
}

// 规格触顶例：第一轮 a=6 超 Y=5 固定为 5，第二轮按 Trem=5 与
// 剩余 eff（b=5, c=1, E=6）重算，余数 1 对 5，差额 1 给 c。
func TestSpecExampleCapped(t *testing.T) {
	c := mustCalc(t, 100, 10, 5)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("c", 1, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	mustShares(t, c, 100, sh("a", 5, "b", 4, "c", 1))
}

// 规格容量不足例：Y=4，a、b 先后固定为 4 后 Trem=2，A 为空。
func TestSpecExampleInsufficientCapacity(t *testing.T) {
	c := mustCalc(t, 100, 10, 4)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	mustSharesErr(t, c, 100, shares.ErrInsufficientCapacity)
}

// e 恰等于 Wslow 取满权重；e 差 1 取下取整值。
func TestEffectiveWeightAtBoundary(t *testing.T) {
	c := mustCalc(t, 100, 190, 190)
	mustOK(t, c.AddHost("a", 10, 0)) // now=100 时 e=100，取满 10
	mustOK(t, c.AddHost("b", 10, 1)) // now=100 时 e=99，取 floor(990/100)=9
	// E=19，a: 10*190/19=100，b: 9*190/19=90，余数均为 0。
	mustShares(t, c, 100, sh("a", 100, "b", 90))
}

// e 为 0 与下取整结果为 0 时有效权重仍至少为 1。
func TestEffectiveWeightFloorOne(t *testing.T) {
	c := mustCalc(t, 100, 20, 20)
	mustOK(t, c.AddHost("a", 1, 0))   // e=100，满权重 1
	mustOK(t, c.AddHost("b", 1, 99))  // e=1，floor(1/100)=0 -> 1
	mustOK(t, c.AddHost("d", 5, 100)) // e=0 -> max(1,0)=1
	// 三者 eff 均为 1，E=3，20=6*3+2，余数并列，差额 2 给 id 较小两者。
	mustShares(t, c, 100, sh("a", 7, "b", 7, "d", 6))
}

// 不健康恢复后重新爬坡；设相同健康值不重置 join。
func TestHealthRampAndNoReset(t *testing.T) {
	c := mustCalc(t, 100, 110, 110)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("r", 10, 0)) // 参照实例，全程健康
	mustOK(t, c.SetHealth("a", false, 50))
	mustOK(t, c.SetHealth("a", false, 60)) // 同值，不改状态
	mustOK(t, c.SetHealth("a", true, 70))  // 恢复，join 重置为 70
	mustOK(t, c.SetHealth("a", true, 75))  // 同值，join 保持 70
	// now=80：a 的 e=10，eff=floor(10*10/100)=1；r 的 e=80，eff=8。
	// E=9，a: 110/9=12 余 2，r: 880/9=97 余 7，差额 1 给 r。
	mustShares(t, c, 80, sh("a", 12, "r", 98))
	// now=170：a 的 e=100 恰满爬坡，eff=10，与 r 平分；
	// 若 75 的同值设置误重置 join，则 eff=9，结果不同。
	mustShares(t, c, 170, sh("a", 55, "r", 55))
}

// 不健康实例份数为 0 且仍出现在结果中。
func TestUnhealthyZeroShare(t *testing.T) {
	c := mustCalc(t, 100, 10, 10)
	mustOK(t, c.AddHost("a", 5, 0))
	mustOK(t, c.AddHost("b", 5, 0))
	mustOK(t, c.SetHealth("b", false, 10))
	mustShares(t, c, 20, sh("a", 10, "b", 0))
}

// SetWeight 只改权重、保留 join。
func TestSetWeightKeepsJoin(t *testing.T) {
	c := mustCalc(t, 100, 200, 200)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("r", 100, 0))
	mustOK(t, c.SetWeight("a", 100, 50))
	// now=100：若 join 保留，a 满权重 100，与 r 平分；若被重置则 eff=50。
	mustShares(t, c, 100, sh("a", 100, "r", 100))
}

// 移除后同 id 再登记视为新实例，重新爬坡。
func TestRemoveAndReaddReramps(t *testing.T) {
	c := mustCalc(t, 100, 15, 15)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("r", 10, 0))
	mustOK(t, c.RemoveHost("a", 50))
	mustOK(t, c.AddHost("a", 10, 60)) // join=60
	// now=110：a 的 e=50，eff=5；r 满权重 10。E=15，a=5，r=10。
	mustShares(t, c, 110, sh("a", 5, "r", 10))
}

// 差额（ leftover ）不超过健康实例数：5 台等权，T=999999，
// 底数 199999，差额 4 依次给 id 最小的 4 台。
func TestLeftoverBoundedByHostCount(t *testing.T) {
	c := mustCalc(t, 1, 999_999, 999_999)
	for _, id := range []string{"h1", "h2", "h3", "h4", "h5"} {
		mustOK(t, c.AddHost(id, 1, 0))
	}
	mustShares(t, c, 0,
		sh("h1", 200000, "h2", 200000, "h3", 200000, "h4", 200000, "h5", 199999))
}

// 份数恰等于 Y 不触顶；Y+1 触顶并进入固定流程。
func TestExactCapNotCapped(t *testing.T) {
	// T=15、Y=5，三台等权各得 5，恰等于 Y，不触顶。
	c := mustCalc(t, 1, 15, 5)
	mustOK(t, c.AddHost("a", 1, 0))
	mustOK(t, c.AddHost("b", 1, 0))
	mustOK(t, c.AddHost("c", 1, 0))
	mustShares(t, c, 0, sh("a", 5, "b", 5, "c", 5))

	// 同样三台等权，T=16：a 得 6=Y+1 触顶固定为 5，随后 b、c 各得 6
	// 相继触顶，最终 Trem=1 而 A 为空，容量不足。
	c2 := mustCalc(t, 1, 16, 5)
	mustOK(t, c2.AddHost("a", 1, 0))
	mustOK(t, c2.AddHost("b", 1, 0))
	mustOK(t, c2.AddHost("c", 1, 0))
	mustSharesErr(t, c2, 0, shares.ErrInsufficientCapacity)
}

// 同一轮多个实例同时触顶且一并固定：a、b 第一轮各 45 同时固定为 34，
// 第二轮 c 独得 32。
func TestMultipleCappedSameRound(t *testing.T) {
	c := mustCalc(t, 1, 100, 34)
	mustOK(t, c.AddHost("a", 45, 0))
	mustOK(t, c.AddHost("b", 45, 0))
	mustOK(t, c.AddHost("c", 10, 0))
	mustShares(t, c, 1, sh("a", 34, "b", 34, "c", 32))
}

// 固定后第二轮产生新的超限而进入第三轮：
// 第一轮 a=90 固定为 34；第二轮 E=10，b=59 固定为 34；第三轮 c 独得 32。
func TestThreeRounds(t *testing.T) {
	c := mustCalc(t, 1, 100, 34)
	mustOK(t, c.AddHost("a", 90, 0))
	mustOK(t, c.AddHost("b", 9, 0))
	mustOK(t, c.AddHost("c", 1, 0))
	mustShares(t, c, 1, sh("a", 34, "b", 34, "c", 32))
}

// Y 乘以健康实例数恰等于 T：a 第一轮 9 超 Y=5 固定，b 第二轮独得 5=Y，
// 全部实例最终都为 Y，总和恰为 T。
func TestCapTimesHostsEqualsTotal(t *testing.T) {
	c := mustCalc(t, 1, 10, 5)
	mustOK(t, c.AddHost("a", 9, 0))
	mustOK(t, c.AddHost("b", 1, 0))
	mustShares(t, c, 1, sh("a", 5, "b", 5))
}

// Y 乘以健康实例数小于 T 时报容量不足。
func TestCapTimesHostsBelowTotal(t *testing.T) {
	c := mustCalc(t, 1, 10, 4)
	mustOK(t, c.AddHost("a", 1, 0))
	mustOK(t, c.AddHost("b", 1, 0))
	mustSharesErr(t, c, 0, shares.ErrInsufficientCapacity)
}

// 没有健康实例（含没有任何实例）时报无健康实例。
func TestNoHealthyHosts(t *testing.T) {
	c := mustCalc(t, 1, 10, 10)
	mustSharesErr(t, c, 0, shares.ErrNoHealthyHosts)
	mustOK(t, c.AddHost("a", 1, 1))
	mustOK(t, c.SetHealth("a", false, 2))
	mustSharesErr(t, c, 3, shares.ErrNoHealthyHosts)
}

// 构造参数越界整体拒绝。
func TestInvalidConfig(t *testing.T) {
	cases := [][3]int64{
		{0, 10, 5}, {1_000_000_001, 10, 5}, // Wslow 越界
		{1, 0, 1}, {1, 1_000_001, 5}, // T 越界
		{1, 10, 0}, {1, 10, 11}, // Y 越界（含 Y>T）
	}
	for _, tc := range cases {
		if _, err := shares.NewCalculator(tc[0], tc[1], tc[2]); !errors.Is(err, shares.ErrInvalidConfig) {
			t.Fatalf("NewCalculator%v 错误 = %v, 期望 ErrInvalidConfig", tc, err)
		}
	}
	if _, err := shares.NewCalculator(1, 1, 1); err != nil {
		t.Fatalf("边界最小配置应合法: %v", err)
	}
	if _, err := shares.NewCalculator(1_000_000_000, 1_000_000, 1_000_000); err != nil {
		t.Fatalf("边界最大配置应合法: %v", err)
	}
}

// 错误报告顺序：参数非法 > 时间非法 > 时钟回退 > 状态非法。
func TestErrorPrecedence(t *testing.T) {
	c := mustCalc(t, 1, 10, 10)
	mustOK(t, c.AddHost("a", 1, 100))

	// 参数非法优先于时间非法。
	mustErr(t, c.AddHost("", 0, -1), shares.ErrInvalidParam)
	mustErr(t, c.SetWeight("a", 1_000_001, -1), shares.ErrInvalidParam)
	// 时间非法优先于时钟回退。
	mustErr(t, c.SetHealth("a", true, -1), shares.ErrInvalidTime)
	mustErr(t, c.SetHealth("a", true, 1_000_000_000_000_001), shares.ErrInvalidTime)
	// 时钟回退优先于状态非法（id 不存在）。
	mustErr(t, c.SetHealth("ghost", true, 99), shares.ErrClockRollback)
	// 状态非法：id 已存在 / id 不存在。
	mustErr(t, c.AddHost("a", 1, 100), shares.ErrHostExists)
	mustErr(t, c.SetWeight("ghost", 1, 100), shares.ErrHostNotFound)
	mustErr(t, c.SetHealth("ghost", true, 100), shares.ErrHostNotFound)
	mustErr(t, c.RemoveHost("ghost", 100), shares.ErrHostNotFound)
	// 空 id 与越界 weight。
	mustErr(t, c.AddHost("", 1, 100), shares.ErrInvalidParam)
	mustErr(t, c.AddHost("b", 0, 100), shares.ErrInvalidParam)
}

// 被拒绝的操作不改变任何状态（含最大 now）。
func TestRejectedOpsKeepState(t *testing.T) {
	c := mustCalc(t, 100, 10_000, 10_000)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	want := sh("a", 6667, "b", 3333)
	mustShares(t, c, 100, want)

	// 各类被拒绝操作。
	mustErr(t, c.AddHost("", 1, 200), shares.ErrInvalidParam)
	mustErr(t, c.AddHost("c", 0, 200), shares.ErrInvalidParam)
	mustErr(t, c.SetWeight("a", 1, -1), shares.ErrInvalidTime)
	mustErr(t, c.SetHealth("a", false, 99), shares.ErrClockRollback)
	mustErr(t, c.AddHost("a", 1, 200), shares.ErrHostExists)
	mustErr(t, c.RemoveHost("ghost", 200), shares.ErrHostNotFound)
	mustSharesErr(t, c, 50, shares.ErrClockRollback)

	// 状态未变：同一 now 重放结果一致，且 maxNow 仍为 100。
	mustShares(t, c, 100, want)
	mustOK(t, c.AddHost("c", 1, 150)) // 若 maxNow 被推到 200 则会回退报错
}

// 因无健康实例或容量不足而报错的 Shares 仍推进最大 now。
func TestFailedSharesAdvancesMaxNow(t *testing.T) {
	c := mustCalc(t, 1, 10, 10)
	mustSharesErr(t, c, 100, shares.ErrNoHealthyHosts)
	mustErr(t, c.AddHost("a", 1, 99), shares.ErrClockRollback)
	mustOK(t, c.AddHost("a", 1, 100))

	c2 := mustCalc(t, 1, 10, 4)
	mustOK(t, c2.AddHost("a", 1, 0))
	mustOK(t, c2.AddHost("b", 1, 0))
	mustSharesErr(t, c2, 100, shares.ErrInsufficientCapacity)
	mustErr(t, c2.AddHost("c", 1, 99), shares.ErrClockRollback)
}

// 并发调用：结果等价于某个串行顺序，成功 Shares 满足不变量。
func TestConcurrent(t *testing.T) {
	c := mustCalc(t, 100, 1_000, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := string(rune('a' + g))
			base := int64(g * 1_000)
			_ = c.AddHost(id, int64(g+1), base)
			for i := int64(1); i <= 200; i++ {
				now := base + i
				_ = c.SetWeight(id, int64(g+1), now)
				_ = c.SetHealth(id, i%3 != 0, now)
				got, err := c.Shares(now)
				if err != nil {
					continue
				}
				var sum int64
				for _, s := range got {
					if s.Value < 0 || s.Value > 100 {
						t.Errorf("份额越界: %v", got)
						return
					}
					sum += s.Value
				}
				if sum != 1_000 {
					t.Errorf("份额总和 = %d, 期望 1000: %v", sum, got)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
