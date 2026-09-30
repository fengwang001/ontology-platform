package pmtu

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var (
	testE        = 1500
	testLo       = 576
	testP        = []int{1400, 1280, 1000, 600}
	testX        = 10 * time.Second
	testK        = 3
	testBaseTime = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := NewCache(testE, testLo, testP, testX, testK)
	if err != nil {
		t.Fatalf("NewCache 失败: %v", err)
	}
	return c
}

func at(sec float64) time.Time {
	return testBaseTime.Add(time.Duration(sec * float64(time.Second)))
}

func mustAccept(t *testing.T, err error, op string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s 被意外拒绝: %v", op, err)
	}
	t.Logf("输入=%s 输出=nil(接受) 判定依据=入参合法", op)
}

func mustReject(t *testing.T, err error, want error, op, why string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s 错误 = %v, 期望 %v", op, err, want)
	}
	t.Logf("输入=%s 输出=%v 判定依据=%s", op, err, why)
}

func query(t *testing.T, c *Cache, dest string, now time.Time, want int, why string) {
	t.Helper()
	got := c.Query(dest, now)
	t.Logf("输入=Query(%q, t+%v) 输出=%d 判定依据=%s", dest, now.Sub(testBaseTime), got, why)
	if got != want {
		t.Fatalf("Query(%q) = %d, 期望 %d（%s）", dest, got, want, why)
	}
}

func TestNewCacheValidationOrder(t *testing.T) {
	cases := []struct {
		name  string
		E, Lo int
		P     []int
		X     time.Duration
		K     int
		want  error
	}{
		{"Lo 为零", 1500, 0, testP, testX, testK, ErrFloorNonPositive},
		{"Lo 为负", 1500, -1, testP, testX, testK, ErrFloorNonPositive},
		{"E 小于 Lo", 500, 576, nil, testX, testK, ErrMTUBelowFloor},
		{"P 非严格降序", 1500, 576, []int{1280, 1400}, testX, testK, ErrPlateausInvalid},
		{"P 有相等相邻项", 1500, 576, []int{1280, 1280}, testX, testK, ErrPlateausInvalid},
		{"P 项低于 Lo", 1500, 576, []int{1400, 500}, testX, testK, ErrPlateausInvalid},
		{"P 项高于 E", 1500, 576, []int{1600}, testX, testK, ErrPlateausInvalid},
		{"X 为零", 1500, 576, testP, 0, testK, ErrTTLNonPositive},
		{"X 为负", 1500, 576, testP, -time.Second, testK, ErrTTLNonPositive},
		{"K 为零", 1500, 576, testP, testX, 0, ErrThresholdNonPos},
		{"K 为负", 1500, 576, testP, testX, -2, ErrThresholdNonPos},
		{"多项非法只报第一个(Lo)", 500, 0, []int{1, 2}, 0, 0, ErrFloorNonPositive},
		{"多项非法只报第一个(E<Lo)", 500, 576, []int{1, 2}, 0, 0, ErrMTUBelowFloor},
		{"多项非法只报第一个(P)", 1500, 576, []int{1, 2}, 0, 0, ErrPlateausInvalid},
		{"多项非法只报第一个(X)", 1500, 576, testP, 0, 0, ErrTTLNonPositive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewCache(tc.E, tc.Lo, tc.P, tc.X, tc.K)
			if !errors.Is(err, tc.want) {
				t.Fatalf("NewCache 错误 = %v, 期望 %v", err, tc.want)
			}
			t.Logf("输入=NewCache(E=%d, Lo=%d, P=%v, X=%v, K=%d) 输出=%v 判定依据=按序校验首个非法项",
				tc.E, tc.Lo, tc.P, tc.X, tc.K, err)
		})
	}
	t.Run("合法构造", func(t *testing.T) {
		if _, err := NewCache(testE, testLo, testP, testX, testK); err != nil {
			t.Fatalf("合法参数被拒绝: %v", err)
		}
		t.Logf("输入=NewCache(合法) 输出=nil 判定依据=全部校验通过")
	})
}

func TestQueryWithoutEntryReturnsE(t *testing.T) {
	c := newTestCache(t)
	query(t, c, "10.0.0.1", at(0), testE, "无条目，返回出接口 MTU E")
}

func TestFragmentationUnreportedUsesPlateau(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportFragmentation("d1", 1500, 0, at(0)),
		`ReportFragmentation("d1", size=1500, m=0, t+0s)`)
	query(t, c, "d1", at(0), 1400, "m=0 视为未报告，取 P 中严格小于 1500 的最大项 1400")

	mustAccept(t, c.ReportFragmentation("d2", 1400, 1500, at(1)),
		`ReportFragmentation("d2", size=1400, m=1500, t+1s)`)
	query(t, c, "d2", at(1), 1280, "m=1500 >= size=1400 视为未报告，取 P 中严格小于 1400 的最大项 1280")

	mustAccept(t, c.ReportFragmentation("d3", 600, 0, at(2)),
		`ReportFragmentation("d3", size=600, m=0, t+2s)`)
	query(t, c, "d3", at(2), testLo, "P 中无严格小于 600 的项，取 Lo=576")
}

func TestFragmentationReportedBelowFloorRaisedToLo(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportFragmentation("d1", 1500, 500, at(0)),
		`ReportFragmentation("d1", size=1500, m=500, t+0s)`)
	query(t, c, "d1", at(0), testLo, "报告值 500 低于下限，被抬到 Lo=576")
}

func TestLateLargerReportIgnoredNoRefresh(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportFragmentation("d1", 1500, 1000, at(0)),
		`ReportFragmentation("d1", size=1500, m=1000, t+0s)`)
	query(t, c, "d1", at(0), 1000, "报告值 1000 小于当前 1500，采用")

	mustAccept(t, c.ReportFragmentation("d1", 1400, 1200, at(9)),
		`ReportFragmentation("d1", size=1400, m=1200, t+9s)`)
	query(t, c, "d1", at(9), 1000, "报告值 1200 有效（0<m<size）但不小于当前 1000，忽略")

	query(t, c, "d1", at(10), testE, "迟到报告未刷新设定时刻，t+10s 恰到期，整条失效回升 E")
}

func TestExpiryBoundary(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportFragmentation("d1", 1500, 1400, at(0)),
		`ReportFragmentation("d1", size=1500, m=1400, t+0s)`)
	query(t, c, "d1", at(9.999), 1400, "t+9.999s 早于 设定时刻+X，条目有效")
	query(t, c, "d1", at(10), testE, "t+10s 恰为 设定时刻+X，不早于则失效，回到 E")
	query(t, c, "d1", at(11), testE, "t+11s 已过期，回到 E")
}

func TestTimeoutDowngradesOnlyAtK(t *testing.T) {
	c := newTestCache(t)
	for i := 1; i <= testK-1; i++ {
		mustAccept(t, c.ReportTimeout("d1", 1500, at(float64(i))),
			fmt.Sprintf(`ReportTimeout("d1", size=1500, t+%ds) 第%d次`, i, i))
		query(t, c, "d1", at(float64(i)), 1500,
			fmt.Sprintf("第 %d 次超时未达 K=%d，不降级", i, testK))
	}
	mustAccept(t, c.ReportTimeout("d1", 1500, at(float64(testK))),
		fmt.Sprintf(`ReportTimeout("d1", size=1500, t+%ds) 第%d次`, testK, testK))
	query(t, c, "d1", at(float64(testK)), 1400, "第 K 次超时，降为 P 中严格小于 1500 的最大项 1400，计数清零")

	for i := 1; i <= testK-1; i++ {
		mustAccept(t, c.ReportTimeout("d1", 1400, at(float64(testK+i))),
			fmt.Sprintf(`ReportTimeout("d1", size=1400, t+%ds)`, testK+i))
	}
	query(t, c, "d1", at(float64(2*testK-1)), 1400, "降级后计数从零起，K-1 次不降级")
	mustAccept(t, c.ReportTimeout("d1", 1400, at(float64(2*testK))),
		`ReportTimeout("d1", size=1400)`)
	query(t, c, "d1", at(float64(2*testK)), 1280, "再次达 K 次，降为严格小于 1400 的最大项 1280")
}

func TestTimeoutSmallerSizeIgnored(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportTimeout("d1", 1400, at(0)),
		`ReportTimeout("d1", size=1400, t+0s)`)
	mustAccept(t, c.ReportTimeout("d1", 1500, at(1)),
		`ReportTimeout("d1", size=1500, t+1s)`)
	mustAccept(t, c.ReportTimeout("d1", 1500, at(2)),
		`ReportTimeout("d1", size=1500, t+2s)`)
	query(t, c, "d1", at(2), 1500, "size=1400 不等于当前 1500 不计数，仅 2 次有效超时，未达 K")
	mustAccept(t, c.ReportTimeout("d1", 1500, at(3)),
		`ReportTimeout("d1", size=1500, t+3s)`)
	query(t, c, "d1", at(3), 1400, "第 3 次有效超时达 K，降级")
}

func TestSuccessResetsTimeoutCount(t *testing.T) {
	c := newTestCache(t)
	mustAccept(t, c.ReportTimeout("d1", 1500, at(0)), `ReportTimeout("d1", size=1500, t+0s)`)
	mustAccept(t, c.ReportTimeout("d1", 1500, at(1)), `ReportTimeout("d1", size=1500, t+1s)`)
	mustAccept(t, c.ReportSuccess("d1", 1400, at(2)), `ReportSuccess("d1", size=1400, t+2s)`)
	mustAccept(t, c.ReportTimeout("d1", 1500, at(3)), `ReportTimeout("d1", size=1500, t+3s)`)
	query(t, c, "d1", at(3), 1400, "size=1400 的成功报告不清零，第 3 次超时达 K 降级")

	c2 := newTestCache(t)
	mustAccept(t, c2.ReportTimeout("d1", 1500, at(0)), `ReportTimeout("d1", size=1500, t+0s)`)
	mustAccept(t, c2.ReportTimeout("d1", 1500, at(1)), `ReportTimeout("d1", size=1500, t+1s)`)
	mustAccept(t, c2.ReportSuccess("d1", 1500, at(2)), `ReportSuccess("d1", size=1500, t+2s)`)
	mustAccept(t, c2.ReportTimeout("d1", 1500, at(3)), `ReportTimeout("d1", size=1500, t+3s)`)
	mustAccept(t, c2.ReportTimeout("d1", 1500, at(4)), `ReportTimeout("d1", size=1500, t+4s)`)
	query(t, c2, "d1", at(4), 1500, "匹配的成功报告清零计数，之后仅 2 次超时未达 K")
}

func TestInvalidReportsRejected(t *testing.T) {
	newCacheWithEntry := func(t *testing.T) *Cache {
		c := newTestCache(t)
		mustAccept(t, c.ReportFragmentation("d1", 1500, 1000, at(5)),
			`ReportFragmentation("d1", size=1500, m=1000, t+5s)`)
		return c
	}

	t.Run("时钟回拨", func(t *testing.T) {
		c := newCacheWithEntry(t)
		mustReject(t, c.ReportFragmentation("d1", 1500, 800, at(4)), ErrClockRollback,
			`ReportFragmentation("d1", 1500, 800, t+4s)`, "t+4s 早于上次操作 t+5s")
		mustReject(t, c.ReportTimeout("d1", 1000, at(4)), ErrClockRollback,
			`ReportTimeout("d1", 1000, t+4s)`, "时钟回拨")
		mustReject(t, c.ReportSuccess("d1", 1000, at(4)), ErrClockRollback,
			`ReportSuccess("d1", 1000, t+4s)`, "时钟回拨")
		query(t, c, "d1", at(6), 1000, "被拒绝的操作不改变条目")
	})

	t.Run("目的地为空", func(t *testing.T) {
		c := newCacheWithEntry(t)
		mustReject(t, c.ReportFragmentation("", 1500, 800, at(6)), ErrEmptyDestination,
			`ReportFragmentation("", 1500, 800, t+6s)`, "目的地为空")
		mustReject(t, c.ReportTimeout("", 1000, at(6)), ErrEmptyDestination,
			`ReportTimeout("", 1000, t+6s)`, "目的地为空")
		mustReject(t, c.ReportSuccess("", 1000, at(6)), ErrEmptyDestination,
			`ReportSuccess("", 1000, t+6s)`, "目的地为空")
	})

	t.Run("size 非法", func(t *testing.T) {
		c := newCacheWithEntry(t)
		for _, size := range []int{0, -1, 1501} {
			mustReject(t, c.ReportFragmentation("d1", size, 800, at(6)), ErrSizeOutOfRange,
				fmt.Sprintf(`ReportFragmentation("d1", size=%d, 800, t+6s)`, size), "size 非正或大于 E")
			mustReject(t, c.ReportTimeout("d1", size, at(6)), ErrSizeOutOfRange,
				fmt.Sprintf(`ReportTimeout("d1", size=%d, t+6s)`, size), "size 非正或大于 E")
			mustReject(t, c.ReportSuccess("d1", size, at(6)), ErrSizeOutOfRange,
				fmt.Sprintf(`ReportSuccess("d1", size=%d, t+6s)`, size), "size 非正或大于 E")
		}
		query(t, c, "d1", at(6), 1000, "被拒绝的操作不改变条目")
	})

	t.Run("m 非法", func(t *testing.T) {
		c := newCacheWithEntry(t)
		for _, m := range []int{-1, 1501} {
			mustReject(t, c.ReportFragmentation("d1", 1500, m, at(6)), ErrReportMTUInvalid,
				fmt.Sprintf(`ReportFragmentation("d1", 1500, m=%d, t+6s)`, m), "m 为负或大于 E")
		}
		query(t, c, "d1", at(6), 1000, "被拒绝的操作不改变条目")
	})

	t.Run("被拒绝的操作不推进时钟", func(t *testing.T) {
		c := newCacheWithEntry(t)
		mustReject(t, c.ReportTimeout("d1", 0, at(9)), ErrSizeOutOfRange,
			`ReportTimeout("d1", size=0, t+9s)`, "size 非法，拒绝")
		mustAccept(t, c.ReportTimeout("d1", 1000, at(7)),
			`ReportTimeout("d1", size=1000, t+7s)`)
		t.Log("判定依据=被拒绝的 t+9s 未推进时钟，t+7s 仍被接受")
	})
}

func TestMonotonicWithinValidity(t *testing.T) {
	c := newTestCache(t)
	prev := testE
	for i := 0; i < 50; i++ {
		now := at(float64(i) * 0.1)
		if err := c.ReportTimeout("d1", c.Query("d1", now), now); err != nil {
			t.Fatalf("ReportTimeout 被拒绝: %v", err)
		}
		got := c.Query("d1", now)
		if got > prev {
			t.Fatalf("有效期内路径 MTU 上升: %d -> %d", prev, got)
		}
		if got < testLo || got > testE {
			t.Fatalf("路径 MTU 越界: %d 不在 [%d, %d]", got, testLo, testE)
		}
		prev = got
	}
	t.Logf("判定依据=有效期内只降不升且落在 [Lo, E]，最终值=%d", prev)
}

func TestConcurrency(t *testing.T) {
	c := newTestCache(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dest := fmt.Sprintf("10.0.0.%d", g%4)
			now := at(0)
			for i := 0; i < 200; i++ {
				switch i % 4 {
				case 0:
					_ = c.ReportFragmentation(dest, 1500, 1000, now)
				case 1:
					_ = c.ReportTimeout(dest, 1500, now)
				case 2:
					_ = c.ReportSuccess(dest, 1500, now)
				default:
					if got := c.Query(dest, now); got < testLo || got > testE {
						t.Errorf("并发查询越界: %d", got)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	t.Log("判定依据=8 协程混合调用无数据竞争，查询结果始终在 [Lo, E]")
}

func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind    int // 0=需分片 1=超时 2=成功
		dest    string
		size, m int
		now     time.Time
	}
	ops := []op{
		{0, "a", 1500, 0, at(0)},
		{1, "a", 1400, 0, at(1)},
		{1, "a", 1400, 0, at(2)},
		{2, "a", 1400, 0, at(3)},
		{1, "a", 1400, 0, at(4)},
		{0, "b", 1200, 900, at(5)},
		{1, "b", 900, 0, at(6)},
		{0, "a", 1000, 1500, at(7)},
	}
	run := func() []int {
		c := newTestCache(t)
		var out []int
		for _, o := range ops {
			switch o.kind {
			case 0:
				_ = c.ReportFragmentation(o.dest, o.size, o.m, o.now)
			case 1:
				_ = c.ReportTimeout(o.dest, o.size, o.now)
			case 2:
				_ = c.ReportSuccess(o.dest, o.size, o.now)
			}
			out = append(out, c.Query(o.dest, o.now))
		}
		return out
	}
	first, second := run(), run()
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("重放第 %d 步不一致: %d != %d", i, first[i], second[i])
		}
	}
	t.Logf("输入=%d 步固定操作序列 输出=%v 判定依据=两次重放结果完全相同", len(ops), first)
}
