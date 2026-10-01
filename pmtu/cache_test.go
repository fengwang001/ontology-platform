package pmtu

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

const (
	testE  = 1500
	testLo = 576
	testK  = 3
)

var (
	testP = []int{1400, 1280, 1000}
	testX = 10 * time.Second
)

func newTestCache(t *testing.T) *Cache {
	t.Helper()
	c, err := NewCache(testE, testLo, testP, testX, testK)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	return c
}

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func checkQuery(t *testing.T, c *Cache, dest string, now time.Time, want int, why string) {
	t.Helper()
	got := c.Query(dest, now)
	t.Logf("query dest=%q now=%+v -> %d, want %d | 依据: %s", dest, now.Sub(base), got, want, why)
	if got != want {
		t.Fatalf("query = %d, want %d (%s)", got, want, why)
	}
}

func mustFrag(t *testing.T, c *Cache, dest string, size, m int, now time.Time) {
	t.Helper()
	if err := c.ReportFragmentationNeeded(dest, size, m, now); err != nil {
		t.Fatalf("frag dest=%q size=%d m=%d: %v", dest, size, m, err)
	}
	t.Logf("frag dest=%q size=%d m=%d now=%+v -> 接受", dest, size, m, now.Sub(base))
}

func mustTimeout(t *testing.T, c *Cache, dest string, size int, now time.Time) {
	t.Helper()
	if err := c.ReportTimeout(dest, size, now); err != nil {
		t.Fatalf("timeout dest=%q size=%d: %v", dest, size, err)
	}
	t.Logf("timeout dest=%q size=%d now=%+v -> 接受", dest, size, now.Sub(base))
}

func mustSuccess(t *testing.T, c *Cache, dest string, size int, now time.Time) {
	t.Helper()
	if err := c.ReportSuccess(dest, size, now); err != nil {
		t.Fatalf("success dest=%q size=%d: %v", dest, size, err)
	}
	t.Logf("success dest=%q size=%d now=%+v -> 接受", dest, size, now.Sub(base))
}

func TestQueryWithoutEntryReturnsE(t *testing.T) {
	c := newTestCache(t)
	t.Logf("输入: 空缓存查询, E=%d", testE)
	checkQuery(t, c, "host-a", at(0), testE, "无条目时查询返回出接口 MTU E")
}

// 未报告（m=0）时取 P 中严格小于 size 的最大项。
func TestFragUnreportedUsesPlateauStrictlyBelowSize(t *testing.T) {
	c := newTestCache(t)

	t.Logf("输入: frag size=1500 m=0, P=%v", testP)
	mustFrag(t, c, "host-a", 1500, 0, at(0))
	checkQuery(t, c, "host-a", at(1), 1400, "m=0 视为未报告, 取 P 中严格小于 1500 的最大项 1400")

	t.Logf("输入: frag size=1400 m=0 (size 本身是台阶值)")
	mustFrag(t, c, "host-b", 1400, 0, at(0))
	checkQuery(t, c, "host-b", at(1), 1280, "严格小于 1400 的最大台阶是 1280, 不能取 1400 本身")

	t.Logf("输入: frag size=900 m=0 (无台阶严格小于 size)")
	mustFrag(t, c, "host-c", 900, 0, at(0))
	checkQuery(t, c, "host-c", at(1), testLo, "P 中无严格小于 900 的项, 取下限 Lo")
}

// 报告值 m 不小于 size 时同样视为未报告，走台阶表。
func TestFragReportNotBelowSizeFallsBackToPlateau(t *testing.T) {
	c := newTestCache(t)

	t.Logf("输入: frag size=1400 m=1400 (m == size)")
	mustFrag(t, c, "host-a", 1400, 1400, at(0))
	checkQuery(t, c, "host-a", at(1), 1280, "m >= size 视为未报告, 取严格小于 1400 的台阶 1280")

	t.Logf("输入: frag size=1400 m=1500 (m > size)")
	mustFrag(t, c, "host-b", 1400, 1500, at(0))
	checkQuery(t, c, "host-b", at(1), 1280, "m > size 视为未报告, 取严格小于 1400 的台阶 1280")
}

// 报告值低于下限时被抬到 Lo。
func TestFragReportBelowLoClampedToLo(t *testing.T) {
	c := newTestCache(t)
	t.Logf("输入: frag size=1500 m=100, Lo=%d", testLo)
	mustFrag(t, c, "host-a", 1500, 100, at(0))
	checkQuery(t, c, "host-a", at(1), testLo, "max(100, Lo) = Lo, 新值抬到下限")
}

// 较大值的迟到报告被忽略，且不刷新设定时刻（有效期仍按原设定时刻计算）。
func TestFragStaleLargerReportIgnoredWithoutRefresh(t *testing.T) {
	c := newTestCache(t)

	mustFrag(t, c, "host-a", 1500, 1280, at(0))
	checkQuery(t, c, "host-a", at(1), 1280, "首个报告把路径 MTU 从 E 降到 1280")

	t.Logf("输入: frag size=1500 m=1400 (大于当前值 1280 的迟到报告)")
	mustFrag(t, c, "host-a", 1500, 1400, at(5))
	checkQuery(t, c, "host-a", at(6), 1280, "新值 1400 不小于当前 1280, 忽略")

	checkQuery(t, c, "host-a", at(9), 1280, "设定时刻未被刷新, 仍是 t=0")
	checkQuery(t, c, "host-a", at(10), testE, "t=10 到达原设定时刻+X, 条目失效回到 E, 证明未刷新")
}

// 恰在到期时刻（now == setAt + X）条目失效，查询回到 E。
func TestExpiryBoundaryReturnsE(t *testing.T) {
	c := newTestCache(t)

	mustFrag(t, c, "host-a", 1500, 1280, at(0))
	checkQuery(t, c, "host-a", at(9), 1280, "t=9 早于 setAt+X=10s, 条目有效")
	checkQuery(t, c, "host-a", at(10), testE, "t=10 恰为 setAt+X, 不早于即失效, 回到 E")
}

// 连续超时计数达到 K 才降级，且逐级走台阶直到 Lo。
func TestTimeoutLowersOnlyOnKth(t *testing.T) {
	c := newTestCache(t)

	mustTimeout(t, c, "host-a", 1500, at(0))
	mustTimeout(t, c, "host-a", 1500, at(1))
	checkQuery(t, c, "host-a", at(1), 1500, "K=3, 仅 2 次超时, 不降级")

	mustTimeout(t, c, "host-a", 1500, at(2))
	checkQuery(t, c, "host-a", at(2), 1400, "第 3 次超时达到 K, 降到严格小于 1500 的最大台阶 1400")

	mustTimeout(t, c, "host-a", 1400, at(3))
	mustTimeout(t, c, "host-a", 1400, at(4))
	mustTimeout(t, c, "host-a", 1400, at(5))
	checkQuery(t, c, "host-a", at(5), 1280, "再次计满 K 次, 降到 1280")

	mustTimeout(t, c, "host-a", 1280, at(6))
	mustTimeout(t, c, "host-a", 1280, at(7))
	mustTimeout(t, c, "host-a", 1280, at(8))
	checkQuery(t, c, "host-a", at(8), 1000, "降到 1000")

	mustTimeout(t, c, "host-a", 1000, at(9))
	mustTimeout(t, c, "host-a", 1000, at(10))
	mustTimeout(t, c, "host-a", 1000, at(11))
	checkQuery(t, c, "host-a", at(11), testLo, "P 中无严格小于 1000 的项, 降到 Lo")

	mustTimeout(t, c, "host-a", testLo, at(12))
	mustTimeout(t, c, "host-a", testLo, at(13))
	mustTimeout(t, c, "host-a", testLo, at(14))
	checkQuery(t, c, "host-a", at(14), testLo, "已在下限, 继续超时保持 Lo 不再下降")
}

// 包长不等于当前路径 MTU 的超时不计数。
func TestTimeoutWithMismatchedSizeNotCounted(t *testing.T) {
	c := newTestCache(t)

	mustTimeout(t, c, "host-a", 1400, at(0))
	t.Logf("依据: 当前路径 MTU 为 E=1500, size=1400 不匹配, 不计数也不建条目")

	mustTimeout(t, c, "host-a", 1500, at(1))
	mustTimeout(t, c, "host-a", 1500, at(2))
	checkQuery(t, c, "host-a", at(2), 1500, "若 t=0 的不匹配超时计入, 此刻已达 K=3; 实际未计入, 仍为 1500")

	mustTimeout(t, c, "host-a", 1500, at(3))
	checkQuery(t, c, "host-a", at(3), 1400, "第 3 次匹配超时达到 K, 降级")
}

// 成功报告清零连续超时计数；包长不匹配的成功报告不清零。
func TestSuccessResetsTimeoutCount(t *testing.T) {
	c := newTestCache(t)

	mustTimeout(t, c, "host-a", 1500, at(0))
	mustTimeout(t, c, "host-a", 1500, at(1))
	mustSuccess(t, c, "host-a", 1500, at(2))
	t.Logf("依据: size 等于当前路径 MTU, 连续超时计数清零")

	mustTimeout(t, c, "host-a", 1500, at(3))
	mustTimeout(t, c, "host-a", 1500, at(4))
	checkQuery(t, c, "host-a", at(4), 1500, "计数被清零后仅累计 2 次, 未达 K, 不降级")

	mustTimeout(t, c, "host-a", 1500, at(5))
	checkQuery(t, c, "host-a", at(5), 1400, "重新计满 K 次后降级")
}

func TestSuccessWithMismatchedSizeDoesNotReset(t *testing.T) {
	c := newTestCache(t)

	mustTimeout(t, c, "host-a", 1500, at(0))
	mustTimeout(t, c, "host-a", 1500, at(1))
	mustSuccess(t, c, "host-a", 1400, at(2))
	t.Logf("依据: size=1400 不等于当前路径 MTU 1500, 计数保留")

	mustTimeout(t, c, "host-a", 1500, at(3))
	checkQuery(t, c, "host-a", at(3), 1400, "计数未被清零, 第 3 次超时达到 K, 降级")
}

// K=1 时一次匹配超时立即降级，且降级刷新设定时刻。
func TestTimeoutThresholdOneDropsImmediately(t *testing.T) {
	c, err := NewCache(testE, testLo, testP, testX, 1)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}

	mustTimeout(t, c, "host-a", 1500, at(0))
	checkQuery(t, c, "host-a", at(1), 1400, "K=1, 首次超时即降级")
	checkQuery(t, c, "host-a", at(9), 1400, "降级时设定时刻置为 t=0, 有效期内保持")
	checkQuery(t, c, "host-a", at(10), testE, "t=10 到期回到 E")
}

// 构造参数按 Lo、E、P、X、K 的顺序校验。
func TestConstructorValidation(t *testing.T) {
	tests := []struct {
		name string
		E    int
		Lo   int
		P    []int
		X    time.Duration
		K    int
		want error
	}{
		{"Lo 为零", 1500, 0, testP, testX, testK, ErrInvalidLo},
		{"Lo 为负", 1500, -1, testP, testX, testK, ErrInvalidLo},
		{"E 小于 Lo", 500, 576, nil, testX, testK, ErrInvalidE},
		{"P 非降序", 1500, 576, []int{1280, 1400}, testX, testK, ErrInvalidPlateaus},
		{"P 有相等项", 1500, 576, []int{1280, 1280}, testX, testK, ErrInvalidPlateaus},
		{"P 项低于 Lo", 1500, 576, []int{500}, testX, testK, ErrInvalidPlateaus},
		{"P 项高于 E", 1500, 576, []int{1501}, testX, testK, ErrInvalidPlateaus},
		{"X 为零", 1500, 576, testP, 0, testK, ErrInvalidExpiry},
		{"X 为负", 1500, 576, testP, -time.Second, testK, ErrInvalidExpiry},
		{"K 为零", 1500, 576, testP, testX, 0, ErrInvalidThreshold},
		{"K 为负", 1500, 576, testP, testX, -2, ErrInvalidThreshold},
		{"多项非法时先报 Lo", 1500, 0, []int{1, 2, 3}, 0, 0, ErrInvalidLo},
		{"Lo 合法但 E 与 K 非法时先报 E", 100, 576, nil, 0, 0, ErrInvalidE},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCache(tt.E, tt.Lo, tt.P, tt.X, tt.K)
			t.Logf("输入: E=%d Lo=%d P=%v X=%s K=%d -> err=%v", tt.E, tt.Lo, tt.P, tt.X, tt.K, err)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

// 报告非法输入被拒绝，且不改变任何条目。
func TestReportValidation(t *testing.T) {
	c := newTestCache(t)
	mustFrag(t, c, "host-a", 1500, 1280, at(5))

	t.Run("时钟回拨", func(t *testing.T) {
		if err := c.ReportFragmentationNeeded("host-a", 1500, 1000, at(4)); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("frag err = %v, want %v", err, ErrClockRollback)
		}
		if err := c.ReportTimeout("host-a", 1280, at(4)); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("timeout err = %v, want %v", err, ErrClockRollback)
		}
		if err := c.ReportSuccess("host-a", 1280, at(4)); !errors.Is(err, ErrClockRollback) {
			t.Fatalf("success err = %v, want %v", err, ErrClockRollback)
		}
		t.Logf("依据: t=4 早于条目设定时刻 t=5, 判定为时钟回拨")
	})

	t.Run("目的地为空", func(t *testing.T) {
		if err := c.ReportFragmentationNeeded("", 1500, 1000, at(6)); !errors.Is(err, ErrEmptyDestination) {
			t.Fatalf("frag err = %v, want %v", err, ErrEmptyDestination)
		}
		if err := c.ReportTimeout("", 1500, at(6)); !errors.Is(err, ErrEmptyDestination) {
			t.Fatalf("timeout err = %v, want %v", err, ErrEmptyDestination)
		}
		if err := c.ReportSuccess("", 1500, at(6)); !errors.Is(err, ErrEmptyDestination) {
			t.Fatalf("success err = %v, want %v", err, ErrEmptyDestination)
		}
	})

	t.Run("size 非法", func(t *testing.T) {
		for _, size := range []int{0, -1, testE + 1} {
			if err := c.ReportFragmentationNeeded("host-a", size, 1000, at(6)); !errors.Is(err, ErrInvalidSize) {
				t.Fatalf("frag size=%d err = %v, want %v", size, err, ErrInvalidSize)
			}
			if err := c.ReportTimeout("host-a", size, at(6)); !errors.Is(err, ErrInvalidSize) {
				t.Fatalf("timeout size=%d err = %v, want %v", size, err, ErrInvalidSize)
			}
			if err := c.ReportSuccess("host-a", size, at(6)); !errors.Is(err, ErrInvalidSize) {
				t.Fatalf("success size=%d err = %v, want %v", size, err, ErrInvalidSize)
			}
			t.Logf("size=%d -> 拒绝: 必须落在 (0, E]", size)
		}
	})

	t.Run("m 非法", func(t *testing.T) {
		for _, m := range []int{-1, testE + 1} {
			if err := c.ReportFragmentationNeeded("host-a", 1500, m, at(6)); !errors.Is(err, ErrInvalidMTU) {
				t.Fatalf("frag m=%d err = %v, want %v", m, err, ErrInvalidMTU)
			}
			t.Logf("m=%d -> 拒绝: 必须落在 [0, E]", m)
		}
	})

	checkQuery(t, c, "host-a", at(6), 1280, "全部被拒绝的操作均未改变条目")
}

// 有效期内路径 MTU 只降不升，且始终落在 [Lo, E]。
func TestValueOnlyDecreasesWithinValidity(t *testing.T) {
	c := newTestCache(t)

	reports := []struct {
		sec  int
		size int
		m    int
	}{
		{0, 1500, 1300},
		{1, 1500, 1400}, // 大于当前值, 应被忽略
		{2, 1500, 1280},
		{3, 1500, 200},  // 低于 Lo, 抬到 Lo
		{4, 1500, 1000}, // 大于当前值 Lo, 应被忽略
	}
	prev := testE
	for _, r := range reports {
		mustFrag(t, c, "host-a", r.size, r.m, at(r.sec))
		got := c.Query("host-a", at(r.sec))
		t.Logf("t=%d 后路径 MTU=%d (前一值 %d)", r.sec, got, prev)
		if got > prev {
			t.Fatalf("t=%d: 路径 MTU 从 %d 升到 %d, 违反只降不升", r.sec, prev, got)
		}
		if got < testLo || got > testE {
			t.Fatalf("t=%d: 路径 MTU %d 越出 [%d, %d]", r.sec, got, testLo, testE)
		}
		prev = got
	}
	checkQuery(t, c, "host-a", at(5), testLo, "最终停在下限 Lo")
}

// 相同操作序列重放结果完全相同。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind string
		dest string
		size int
		m    int
		sec  int
	}
	script := []op{
		{"frag", "a", 1500, 0, 0},
		{"timeout", "a", 1400, 0, 1},
		{"timeout", "b", 1500, 0, 1},
		{"frag", "a", 1400, 1290, 2},
		{"timeout", "a", 1290, 0, 3},
		{"success", "a", 1290, 0, 4},
		{"timeout", "a", 1290, 0, 5},
		{"frag", "b", 1500, 100, 6},
		{"timeout", "a", 1290, 0, 7},
		{"timeout", "a", 1290, 0, 8},
		{"frag", "a", 1290, 1400, 9},
		{"timeout", "b", 576, 0, 10},
		{"frag", "a", 1290, 0, 20}, // a 的条目已过期, 按新条目处理
	}
	run := func() []int {
		c := newTestCache(t)
		out := make([]int, 0, len(script))
		for _, o := range script {
			switch o.kind {
			case "frag":
				_ = c.ReportFragmentationNeeded(o.dest, o.size, o.m, at(o.sec))
			case "timeout":
				_ = c.ReportTimeout(o.dest, o.size, at(o.sec))
			case "success":
				_ = c.ReportSuccess(o.dest, o.size, at(o.sec))
			}
			out = append(out, c.Query(o.dest, at(o.sec)))
		}
		return out
	}
	first, second := run(), run()
	t.Logf("第一次重放结果: %v", first)
	t.Logf("第二次重放结果: %v", second)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("重放结果不一致: %v vs %v", first, second)
	}
}

// 并发调用查询与三种报告；配合 go test -race 验证无数据竞争，
// 且任意时刻路径 MTU 都在 [Lo, E] 内。
func TestConcurrentAccess(t *testing.T) {
	c := newTestCache(t)

	const workers = 8
	const iterations = 200
	var wg sync.WaitGroup
	for g := 0; g < workers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			dest := fmt.Sprintf("host-%d", g%4)
			for i := 0; i < iterations; i++ {
				now := base.Add(time.Duration(i) * time.Millisecond)
				switch i % 4 {
				case 0:
					_ = c.ReportFragmentationNeeded(dest, 1500, 1000+g, now)
				case 1:
					_ = c.ReportTimeout(dest, c.Query(dest, now), now)
				case 2:
					_ = c.ReportSuccess(dest, 1500, now)
				default:
					if got := c.Query(dest, now); got < testLo || got > testE {
						t.Errorf("并发查询得到越界值 %d", got)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	for g := 0; g < 4; g++ {
		dest := fmt.Sprintf("host-%d", g)
		got := c.Query(dest, base.Add(time.Hour))
		t.Logf("最终 query dest=%q -> %d", dest, got)
		if got < testLo || got > testE {
			t.Fatalf("最终值 %d 越出 [%d, %d]", got, testLo, testE)
		}
	}
}
