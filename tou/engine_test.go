package tou_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/tou"
)

var testLoc = time.FixedZone("UTC+8", 8*3600)

func at(y int, mo time.Month, d, h, mi, s int) time.Time {
	return time.Date(y, mo, d, h, mi, s, 0, testLoc)
}

func flatVersion(eff time.Time, price int64) tou.TariffVersion {
	v := tou.TariffVersion{EffectiveAt: eff}
	for i := range v.Schedules {
		v.Schedules[i].Periods = []tou.Period{{StartSec: 0, Price: price}}
	}
	return v
}

func touVersion(eff time.Time, dayPrice, nightPrice int64) tou.TariffVersion {
	v := tou.TariffVersion{EffectiveAt: eff}
	for i := range v.Schedules {
		v.Schedules[i].Periods = []tou.Period{
			{StartSec: 0, Price: nightPrice},
			{StartSec: 8 * 3600, Price: dayPrice},
			{StartSec: 22 * 3600, Price: nightPrice},
		}
	}
	return v
}

func naiveVersionOf(v tou.TariffVersion) [3][]naivePeriod {
	var out [3][]naivePeriod
	for i := range v.Schedules {
		for _, p := range v.Schedules[i].Periods {
			out[i] = append(out[i], naivePeriod{start: p.StartSec, price: p.Price})
		}
	}
	return out
}

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidSchedules(t *testing.T) {
	e := tou.New(testLoc)
	base := at(2024, 1, 1, 0, 0, 0)
	bad := flatVersion(base, 1)
	bad.Schedules[0].Periods = []tou.Period{{StartSec: 100, Price: 1}}
	if err := e.RegisterTariff(bad); err != tou.ErrInvalidParameter {
		t.Fatalf("want ErrInvalidParameter, got %v", err)
	}
	bad2 := flatVersion(base, 1)
	bad2.Schedules[1].Periods = []tou.Period{
		{StartSec: 0, Price: 1}, {StartSec: 86400, Price: 1},
	}
	if err := e.RegisterTariff(bad2); err != tou.ErrInvalidParameter {
		t.Fatalf("want ErrInvalidParameter, got %v", err)
	}
	bad3 := flatVersion(base, 1)
	bad3.Schedules[2].Periods = []tou.Period{
		{StartSec: 0, Price: 1}, {StartSec: 0, Price: 2},
	}
	if err := e.RegisterTariff(bad3); err != tou.ErrInvalidParameter {
		t.Fatalf("want ErrInvalidParameter, got %v", err)
	}
	must(t, e.RegisterTariff(flatVersion(base, 1)))
	if err := e.RegisterTariff(flatVersion(base, 2)); err != tou.ErrInvalidParameter {
		t.Fatalf("dup effective: want ErrInvalidParameter, got %v", err)
	}
	if err := e.RegisterReading("", base, 0); err != tou.ErrInvalidParameter {
		t.Fatalf("empty point: want ErrInvalidParameter, got %v", err)
	}
	if err := e.RegisterReading("p", base.Add(time.Nanosecond), 0); err != tou.ErrInvalidParameter {
		t.Fatalf("sub-second: want ErrInvalidParameter, got %v", err)
	}
	if err := e.RegisterReading("p", base, -1); err != tou.ErrInvalidParameter {
		t.Fatalf("neg wh: want ErrInvalidParameter, got %v", err)
	}
	if _, err := e.Bill("p", base.AddDate(77, 2, 0)); err != tou.ErrInvalidParameter {
		t.Fatalf("year out of range: want ErrInvalidParameter, got %v", err)
	}
	if err := e.SetHoliday("2024/01/01", true); err != tou.ErrInvalidParameter {
		t.Fatalf("bad date format: want ErrInvalidParameter, got %v", err)
	}
}

func TestCrossBoundariesAndApportionment(t *testing.T) {
	e := tou.New(testLoc)
	// 2024-01-31 周三。区间 21:00 -> 2/1 03:00（21600 秒，216 Wh），
	// 同时跨越日界(00:00=月界)、时段边界(22:00)、版本切换(00:30)。
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	must(t, e.RegisterTariff(touVersion(at(2024, 2, 1, 0, 30, 0), 300, 100)))

	p := "P1"
	must(t, e.RegisterReading(p, at(2024, 1, 31, 21, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 2, 1, 3, 0, 0), 216))

	jan, err := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	feb, err := e.Bill(p, at(2024, 2, 1, 0, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if jan.TotalWh != 108 || feb.TotalWh != 108 {
		t.Fatalf("energy conservation broken: jan=%d feb=%d", jan.TotalWh, feb.TotalWh)
	}
	// 1 月 21-22 点 36Wh@100→3；22-24 点 72Wh@100→7。
	if jan.TotalAmountMilli != 10 {
		t.Fatalf("jan amount = %d, want 10", jan.TotalAmountMilli)
	}
	// 2 月 00:00-00:30 旧版本 18Wh@100→1；00:30-03:00 新版本夜价 90Wh@100→9。
	if feb.TotalAmountMilli != 10 {
		t.Fatalf("feb amount = %d, want 10", feb.TotalAmountMilli)
	}
	// 2 月两条 entry 按时段起点有序，且价格区分。
	seen := map[int]bool{}
	for _, en := range feb.Entries {
		seen[int(en.StartSec)] = true
	}
	if !seen[0] {
		t.Fatalf("feb entries missing: %+v", feb.Entries)
	}
}

func TestReadingExactlyOnMonthEndAndCloseFreeze(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	p := "P2"
	must(t, e.RegisterReading(p, at(2024, 1, 31, 23, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 1, 31, 23, 59, 59), 60))
	must(t, e.RegisterReading(p, at(2024, 2, 1, 0, 0, 0), 61))
	jan, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if jan.TotalWh != 61 {
		t.Fatalf("jan wh = %d, want 61", jan.TotalWh)
	}
	must(t, e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)))
	// 封账后落在该月的读数被拒（1/15 在封账范围内；2/1 锚点同时是已存在
	// 读数时刻，按次序同样先报月份已封账）。
	if err := e.RegisterReading(p, at(2024, 1, 15, 0, 0, 0), 999); err != tou.ErrMonthClosed {
		t.Fatalf("want ErrMonthClosed, got %v", err)
	}
	if err := e.RegisterReading(p, at(2024, 2, 1, 0, 0, 0), 999); err != tou.ErrMonthClosed {
		t.Fatalf("anchor register: want ErrMonthClosed, got %v", err)
	}
	if err := e.CorrectReading(p, at(2024, 2, 1, 0, 0, 0), 62); err != tou.ErrMonthClosed {
		t.Fatalf("anchor correct: want ErrMonthClosed, got %v", err)
	}
	if err := e.DeleteReading(p, at(2024, 2, 1, 0, 0, 0)); err != tou.ErrMonthClosed {
		t.Fatalf("anchor delete: want ErrMonthClosed, got %v", err)
	}
	if err := e.RegisterTariff(flatVersion(at(2024, 1, 15, 0, 0, 0), 1)); err != tou.ErrMonthClosed {
		t.Fatalf("post-close tariff: want ErrMonthClosed, got %v", err)
	}
	if err := e.SetHoliday("2024-01-15", true); err != tou.ErrMonthClosed {
		t.Fatalf("post-close holiday: want ErrMonthClosed, got %v", err)
	}
	// 已封账账单快照不随后续状态变化。
	closedJan, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if !closedJan.Closed || closedJan.TotalWh != 61 {
		t.Fatalf("closed snapshot wrong: %+v", closedJan)
	}
}

func TestHolidayOverridesWeekend(t *testing.T) {
	e := tou.New(testLoc)
	// 2024-01-06 周六：工作日 100、休息日 200、节假日 300。
	v := tou.TariffVersion{EffectiveAt: at(2024, 1, 1, 0, 0, 0)}
	prices := [3]int64{100, 200, 300}
	for i := range v.Schedules {
		v.Schedules[i].Periods = []tou.Period{{StartSec: 0, Price: prices[i]}}
	}
	must(t, e.RegisterTariff(v))
	p := "P3"
	must(t, e.RegisterReading(p, at(2024, 1, 6, 0, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 1, 6, 12, 0, 0), 36))
	b, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if b.Entries[0].Price != 200 || b.Entries[0].Day != tou.DayWeekend {
		t.Fatalf("weekend entry wrong: %+v", b.Entries[0])
	}
	if b.TotalAmountMilli != 36*200/1000 {
		t.Fatalf("weekend amount = %d", b.TotalAmountMilli)
	}
	must(t, e.SetHoliday("2024-01-06", true))
	b, _ = e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if b.Entries[0].Price != 300 || b.Entries[0].Day != tou.DayHoliday {
		t.Fatalf("holiday override failed: %+v", b.Entries[0])
	}
	must(t, e.SetHoliday("2024-01-06", false))
	b, _ = e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if b.Entries[0].Price != 200 {
		t.Fatalf("holiday revoke failed: %d", b.Entries[0].Price)
	}
}

func TestBackdatedVersionRecomputesAndFreeze(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 2, 1, 0, 0, 0), 100)))
	p := "P4"
	must(t, e.RegisterReading(p, at(2024, 1, 10, 0, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 2, 2, 0, 0, 0), 230))
	jan, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if jan.UnbillableWh == 0 || jan.TotalAmountMilli != 0 {
		t.Fatalf("expected unbillable jan: %+v", jan)
	}
	if err := e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)); err != tou.ErrUnbillable {
		t.Fatalf("want ErrUnbillable, got %v", err)
	}
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 10, 0, 0, 0), 500)))
	jan, _ = e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if jan.UnbillableWh != 0 || jan.TotalAmountMilli == 0 {
		t.Fatalf("backdated recompute failed: %+v", jan)
	}
	must(t, e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)))
	before := jan.TotalAmountMilli
	if err := e.RegisterTariff(flatVersion(at(2024, 1, 5, 0, 0, 0), 999)); err != tou.ErrMonthClosed {
		t.Fatalf("want ErrMonthClosed, got %v", err)
	}
	after, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if after.TotalAmountMilli != before {
		t.Fatal("rejected operation changed frozen state")
	}
}

func TestCorrectBeforeCloseThenRejectedAfter(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	p := "P5"
	must(t, e.RegisterReading(p, at(2024, 1, 10, 0, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 1, 20, 0, 0, 0), 100))
	must(t, e.RegisterReading(p, at(2024, 1, 31, 1, 0, 0), 200))
	must(t, e.RegisterReading(p, at(2024, 2, 1, 1, 0, 0), 300))
	must(t, e.CorrectReading(p, at(2024, 1, 20, 0, 0, 0), 150))
	if err := e.CorrectReading(p, at(2024, 1, 20, 0, 0, 0), 250); err != tou.ErrReadingRegression {
		t.Fatalf("want regression, got %v", err)
	}
	if err := e.CorrectReading(p, at(2024, 1, 20, 0, 0, 0), -1); err != tou.ErrInvalidParameter {
		t.Fatalf("want invalid, got %v", err)
	}
	jan, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	// 修正 1/20 为 150 后：gap0=150、gap1=50 全在 1 月；
	// gap2 (1/31 01:00->2/1 01:00) 读数差仍是 100，1 月 23 小时
	// → floor(100*82800/86400)=95Wh，余量 5Wh 归 2 月最后一片。
	if jan.TotalWh != 150+50+95 {
		t.Fatalf("jan wh = %d, want 295", jan.TotalWh)
	}
	must(t, e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)))
	if err := e.CorrectReading(p, at(2024, 1, 20, 0, 0, 0), 120); err != tou.ErrMonthClosed {
		t.Fatalf("post-close correct: want ErrMonthClosed, got %v", err)
	}
	// 修正一个不存在的读数时刻：参数非法。
	if err := e.CorrectReading(p, at(2024, 2, 10, 0, 0, 0), 1); err != tou.ErrInvalidParameter {
		t.Fatalf("missing reading: want ErrInvalidParameter, got %v", err)
	}
}

func TestReadingsOrderRegressionAndInsufficient(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	p := "P6"
	must(t, e.RegisterReading(p, at(2024, 1, 1, 0, 0, 0), 10))
	// 时刻早于最新读数：时序错误（即使电量合法）。
	if err := e.RegisterReading(p, at(2023, 12, 31, 0, 0, 0), 10); err != tou.ErrOutOfOrder {
		t.Fatalf("want ErrOutOfOrder, got %v", err)
	}
	// 同一时刻重复：时序错误。
	if err := e.RegisterReading(p, at(2024, 1, 1, 0, 0, 0), 10); err != tou.ErrOutOfOrder {
		t.Fatalf("want ErrOutOfOrder, got %v", err)
	}
	// 时刻更晚但电量倒退：读数倒退。
	if err := e.RegisterReading(p, at(2024, 1, 2, 0, 0, 0), 9); err != tou.ErrReadingRegression {
		t.Fatalf("want ErrReadingRegression, got %v", err)
	}
	// 无月末读数不能封账。
	must(t, e.RegisterReading(p, at(2024, 1, 20, 0, 0, 0), 20))
	if err := e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)); err != tou.ErrInsufficientReadings {
		t.Fatalf("want ErrInsufficientReadings, got %v", err)
	}
	// 空供电点封账同样读数不足。
	if err := e.CloseMonth("GHOST", at(2024, 1, 1, 0, 0, 0)); err != tou.ErrInsufficientReadings {
		t.Fatalf("ghost point: want ErrInsufficientReadings, got %v", err)
	}
}

func TestRejectOrdering(t *testing.T) {
	// 同一操作同时触发多类错误时，固定按
	// 参数非法 > 月份已封账 > 时序错误 > 读数倒退 判定。
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	p := "P7"
	must(t, e.RegisterReading(p, at(2024, 1, 1, 0, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 2, 1, 0, 0, 0), 100))
	must(t, e.RegisterReading(p, at(2024, 2, 10, 0, 0, 0), 200))
	must(t, e.CloseMonth(p, at(2024, 1, 1, 0, 0, 0)))
	// 空供电点 + 倒退 + 封账 → 参数非法优先。
	if err := e.RegisterReading("", at(2024, 1, 15, 0, 0, 0), -1); err != tou.ErrInvalidParameter {
		t.Fatalf("order: want ErrInvalidParameter, got %v", err)
	}
	// 参数合法但落在封账月内且时刻倒退且电量倒退 → 月份已封账优先。
	if err := e.RegisterReading(p, at(2024, 1, 15, 0, 0, 0), 50); err != tou.ErrMonthClosed {
		t.Fatalf("order: want ErrMonthClosed, got %v", err)
	}
	// 2 月（未封账）、时刻早于最新读数、电量倒退 → 时序错误优先于读数倒退。
	if err := e.RegisterReading(p, at(2024, 2, 5, 0, 0, 0), 50); err != tou.ErrOutOfOrder {
		t.Fatalf("order: want ErrOutOfOrder, got %v", err)
	}
	// 时刻晚于最新但电量倒退 → 读数倒退。
	if err := e.RegisterReading(p, at(2024, 2, 15, 0, 0, 0), 50); err != tou.ErrReadingRegression {
		t.Fatalf("order: want ErrReadingRegression, got %v", err)
	}
}

func TestDeleteReading(t *testing.T) {
	e := tou.New(testLoc)
	must(t, e.RegisterTariff(flatVersion(at(2024, 1, 1, 0, 0, 0), 100)))
	p := "P8"
	must(t, e.RegisterReading(p, at(2024, 1, 1, 0, 0, 0), 0))
	must(t, e.RegisterReading(p, at(2024, 1, 2, 0, 0, 0), 100))
	if err := e.DeleteReading(p, at(2024, 1, 1, 0, 0, 0)); err != tou.ErrOutOfOrder {
		t.Fatalf("delete non-latest: want ErrOutOfOrder, got %v", err)
	}
	must(t, e.DeleteReading(p, at(2024, 1, 2, 0, 0, 0)))
	b, _ := e.Bill(p, at(2024, 1, 1, 0, 0, 0))
	if b.TotalWh != 0 {
		t.Fatalf("after delete bill should be empty, got %d", b.TotalWh)
	}
	// 可重新追加一条时刻等于刚删除时刻的读数。
	must(t, e.RegisterReading(p, at(2024, 1, 2, 0, 0, 0), 50))
}

type opLog struct {
	mu  sync.Mutex
	bw  *bufio.Writer
	f   *os.File
	seq int
}

func newOpLog(t *testing.T) *opLog {
	t.Helper()
	path := filepath.Join(os.TempDir(), fmt.Sprintf("tou-diff-%d.log", time.Now().UnixNano()))
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("differential log: %s", path)
	l := &opLog{bw: bufio.NewWriter(f), f: f}
	return l
}

func (l *opLog) line(format string, args ...interface{}) {
	l.mu.Lock()
	l.seq++
	fmt.Fprintf(l.bw, "#%d %s\n", l.seq, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *opLog) close() { l.bw.Flush(); l.f.Close() }

func errClass(err error) string {
	switch err {
	case nil:
		return ""
	case tou.ErrInvalidParameter:
		return "参数非法"
	case tou.ErrMonthClosed:
		return "月份已封账"
	case tou.ErrOutOfOrder:
		return "时序错误"
	case tou.ErrReadingRegression:
		return "读数倒退"
	case tou.ErrInsufficientReadings:
		return "读数不足"
	case tou.ErrUnbillable:
		return "不可计价片阻止封账"
	default:
		return err.Error()
	}
}

func sameClass(a, b string) bool {
	if a == b {
		return true
	}
	// 朴素模型对「封账时存在不可计价片」与引擎使用同一中文判定。
	return false
}

// TestDifferentialRandom 用随机操作序列同时驱动引擎与独立朴素模型，
// 逐条比对拒绝类别与所有受影响月份的账单；日志含每条输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	const runs, opsPerRun = 40, 400
	if s := os.Getenv("TOU_SEED"); s != "" {
		var seed int64
		if _, err := fmt.Sscanf(s, "%d", &seed); err != nil {
			t.Fatal(err)
		}
		runDifferential(t, seed, opsPerRun)
		return
	}
	for run := 0; run < runs; run++ {
		seed := time.Now().UnixNano()
		runDifferential(t, seed, opsPerRun)
	}
}

func runDifferential(t *testing.T, seed int64, n int) {
	rng := rand.New(rand.NewSource(seed))
	log := newOpLog(t)
	defer log.close()
	log.line("seed=%d ops=%d", seed, n)

	e := tou.New(testLoc)
	m := newNaive(testLoc)
	points := []string{"A", "B"}

	// 随机时段表构造：保证无缝覆盖整日。
	randomSchedules := func() [3][]naivePeriod {
		var sch [3][]naivePeriod
		for d := 0; d < 3; d++ {
			bounds := []int{0}
			cuts := rng.Intn(3)
			for i := 0; i < cuts; i++ {
				bounds = append(bounds, 1+rng.Intn(86398))
			}
			sortInts(bounds)
			uniq := []int{0}
			for _, b := range bounds[1:] {
				if b > uniq[len(uniq)-1] && b < 86400 {
					uniq = append(uniq, b)
				}
			}
			for _, s := range uniq {
				sch[d] = append(sch[d], naivePeriod{start: s, price: int64(rng.Intn(5) * 100)})
			}
		}
		return sch
	}
	toEngineSchedules := func(sch [3][]naivePeriod) [3]tou.DaySchedule {
		var out [3]tou.DaySchedule
		for d := 0; d < 3; d++ {
			for _, p := range sch[d] {
				out[d].Periods = append(out[d].Periods, tou.Period{StartSec: p.start, Price: p.price})
			}
		}
		return out
	}

	// 初始基线：一个较早版本，避免一开始全部不可计价（仍随机制造无版本区间）。
	compare := func(stage string) {
		for _, pt := range points {
			for mk := int64(2024*12) - 1; mk <= int64(2024*12)+4; mk++ {
				month := time.Date(int(mk/12), time.Month(mk%12+1), 1, 0, 0, 0, 0, testLoc)
				got, gerr := e.Bill(pt, month)
				if gerr != nil {
					t.Fatalf("[seed %d] bill err %v", seed, gerr)
				}
				want, _ := m.bill(pt, month)
				if !billsEqual(got, want) {
					log.close()
					t.Fatalf("[seed %d] bill mismatch after %s pt=%s month=%s\nengine=%+v\nnaive =%+v\nlog kept",
						seed, stage, pt, month.Format("2006-01"), got, want)
				}
			}
		}
	}

	// 记录每个点已经登记成功的读数，供修正操作选取。
	hist := map[string][]naiveReading{}
	lastWh := map[string]int64{}
	lastAt := map[string]time.Time{}
	randMoment := func() time.Time {
		// 2023-12 ~ 2024-05，按小时对齐，日界/月界命中概率更高。
		base := at(2023, 12, 1, 0, 0, 0)
		return base.Add(time.Duration(rng.Intn(180*24)) * time.Hour)
	}
	expect := func(stage, got, want, detail string) {
		if got != want {
			log.line("MISMATCH stage=%s detail=%s engine=%s naive=%s", stage, detail, got, want)
			log.close()
			t.Fatalf("[seed %d] reject mismatch at %s: engine=%q naive=%q (%s)", seed, stage, got, want, detail)
		}
		log.line("%s -> %s | %s", detail, got, basis(got))
	}

	compare("init")
	for i := 0; i < n; i++ {
		pt := points[rng.Intn(len(points))]
		switch rng.Intn(7) {
		case 0: // 注册版本
			sch := randomSchedules()
			eff := randMoment()
			detail := fmt.Sprintf("registerTariff eff=%s", eff.Format("2006-01-02 15"))
			gerr := e.RegisterTariff(tou.TariffVersion{EffectiveAt: eff, Schedules: toEngineSchedules(sch)})
			werrText := m.registerVersion(eff, sch)
			expect("tariff", errClass(gerr), werrText, detail)
		case 1: // 节假日开关
			d := randMoment().Format("2006-01-02")
			on := rng.Intn(2) == 0
			detail := fmt.Sprintf("setHoliday %s on=%v", d, on)
			gerr := e.SetHoliday(d, on)
			werrText := m.setHoliday(d, on)
			expect("holiday", errClass(gerr), werrText, detail)
		case 2: // 追加读数
			tm := randMoment()
			var wh int64
			if rng.Intn(4) == 0 {
				wh = lastWh[pt] + int64(rng.Intn(30)) // 正常增长
			} else {
				wh = int64(rng.Intn(200)) // 可能倒退
			}
			detail := fmt.Sprintf("registerReading pt=%s at=%s wh=%d", pt, tm.Format("2006-01-02 15"), wh)
			gerr := e.RegisterReading(pt, tm, wh)
			werrText := m.registerReading(pt, tm, wh)
			expect("reading", errClass(gerr), werrText, detail)
			if gerr == nil {
				hist[pt] = append(hist[pt], naiveReading{at: tm, wh: wh})
				lastWh[pt], lastAt[pt] = wh, tm
			}
		case 3: // 修正读数
			if len(hist[pt]) == 0 {
				continue
			}
			r := hist[pt][rng.Intn(len(hist[pt]))]
			newWh := int64(rng.Intn(220))
			detail := fmt.Sprintf("correctReading pt=%s at=%s newWh=%d", pt, r.at.Format("2006-01-02 15"), newWh)
			gerr := e.CorrectReading(pt, r.at, newWh)
			werrText := m.correctReading(pt, r.at, newWh)
			expect("correct", errClass(gerr), werrText, detail)
			if gerr == nil {
				for j := range hist[pt] {
					if hist[pt][j].at.Equal(r.at) {
						hist[pt][j].wh = newWh
					}
				}
			}
		case 4: // 删除最新读数
			if len(hist[pt]) == 0 {
				continue
			}
			r := hist[pt][len(hist[pt])-1]
			detail := fmt.Sprintf("deleteReading pt=%s at=%s", pt, r.at.Format("2006-01-02 15"))
			gerr := e.DeleteReading(pt, r.at)
			werrText := m.deleteReading(pt, r.at)
			expect("delete", errClass(gerr), werrText, detail)
			if gerr == nil {
				hist[pt] = hist[pt][:len(hist[pt])-1]
				if len(hist[pt]) > 0 {
					lastWh[pt] = hist[pt][len(hist[pt])-1].wh
					lastAt[pt] = hist[pt][len(hist[pt])-1].at
				} else {
					lastWh[pt], lastAt[pt] = 0, time.Time{}
				}
			}
		case 5: // 封账（随机月份）
			mk := int64(2024*12) - 1 + int64(rng.Intn(5))
			month := time.Date(int(mk/12), time.Month(mk%12+1), 1, 0, 0, 0, 0, testLoc)
			detail := fmt.Sprintf("closeMonth pt=%s month=%s", pt, month.Format("2006-01"))
			gerr := e.CloseMonth(pt, month)
			werrText := m.closeMonth(pt, month)
			expect("close", errClass(gerr), werrText, detail)
		default: // 查询并全量比对
			compare(fmt.Sprintf("op#%d", i))
		}
		if i%16 == 0 {
			compare(fmt.Sprintf("op#%d", i))
		}
	}
	compare("final")
}

func basis(class string) string {
	switch class {
	case "":
		return "accepted"
	case "参数非法":
		return "basis: 优先级1 参数/时段表/格式非法"
	case "月份已封账":
		return "basis: 优先级2 操作落在封账冻结范围"
	case "时序错误":
		return "basis: 优先级3 读数时刻不严格递增/非最新删除"
	case "读数倒退":
		return "basis: 优先级4 累计电量小于前一读或大于后一读"
	case "读数不足":
		return "basis: 封账缺少不早于月末的读数"
	case "不可计价片阻止封账":
		return "basis: 月内存在无版本可用的片"
	default:
		return "basis: " + class
	}
}

func sortInts(a []int) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func billsEqual(g tou.Bill, w naiveBill) bool {
	if g.TotalWh != w.totalWh || g.TotalAmountMilli != w.amount || g.UnbillableWh != w.unbill {
		return false
	}
	if len(g.Entries) != len(w.entries) {
		return false
	}
	for i := range g.Entries {
		a, b := g.Entries[i], w.entries[i]
		if int(a.Day) != b.day || a.StartSec != b.startSec || a.Price != b.price ||
			a.EnergyWh != b.wh || a.AmountMilli != b.amount {
			return false
		}
	}
	return true
}
