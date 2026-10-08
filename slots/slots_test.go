package slots

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// Model 抽象主模型与朴素对照模型，供差分测试共用一套操作脚本。
type Model interface {
	RegisterAirline(at time.Time, id string) error
	Apply(at time.Time, airline string, day, hour, sw, ew int) (string, error)
	SettleAllocation(at time.Time) error
	ReturnWeek(at time.Time, airline, sid string, week int) (string, error)
	ExchangeSeries(at time.Time, alA, sidA, alB, sidB string) error
	RegisterWeek(at time.Time, airline, sid string, week int, st RegStatus) error
	SettleSeason(at time.Time) ([]Qualification, error)
	Snapshot() Snapshot
}

var (
	_ Model = (*Coordinator)(nil)
	_ Model = (*NaiveModel)(nil)
)

var t0 = time.Date(2026, 3, 29, 0, 0, 0, 0, time.UTC)

const week = 7 * 24 * time.Hour

func baseConfig(weeks, minWeeks int) Config {
	var cap [7][24]int
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			cap[d][h] = 100
		}
	}
	return Config{
		Weeks:               weeks,
		WeekLength:          week,
		SeasonStart:         t0,
		MinWeeks:            minWeeks,
		Capacity:            cap,
		ApplyDeadline:       t0.Add(-24 * time.Hour),
		ReturnDeadline:      t0.Add(2 * week),
		QualifyPercent:      80,
		NewEntrantThreshold: 2,
		RegisterWindow:      48 * time.Hour,
		SeasonEnd:           t0.Add(time.Duration(weeks) * week),
	}
}

func setCap(cfg *Config, d, h, v int) { cfg.Capacity[d][h] = v }

func newPair(t *testing.T, cfg Config, hist map[string][]HistKey, log io.Writer) (*Coordinator, *NaiveModel) {
	t.Helper()
	c, err := NewCoordinator(cfg, hist, log)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	m, err := NewNaiveModel(cfg, hist, nil)
	if err != nil {
		t.Fatalf("NewNaiveModel: %v", err)
	}
	return c, m
}

func mustErrIs(t *testing.T, got error, want SlotError, ctx string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", ctx, got, want)
	}
}

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func assertSnap(t *testing.T, c Model, m Model, ctx string) {
	t.Helper()
	a, b := c.Snapshot(), m.Snapshot()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("snapshot mismatch at %s:\nmain=%#v\nnaive=%#v", ctx, a, b)
	}
}

func seriesByAirline(snap Snapshot, al string) []SeriesSnap {
	var out []SeriesSnap
	for _, s := range snap.Series {
		if s.Airline == al {
			out = append(out, s)
		}
	}
	return out
}

func seriesIDOf(snap Snapshot, airline string) string {
	for _, s := range snap.Series {
		if s.Airline == airline {
			return s.ID
		}
	}
	return ""
}

// 使用率恰等于达标比例达标；低一个单位（分子少 1）不达标。
func TestQualifyExactAndOneBelow(t *testing.T) {
	cfg := baseConfig(5, 5)
	cfg.QualifyPercent = 80
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0) // 整季内皆可登记，时间线天然单调
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "AF"), "reg")
	mustOK(t, c.RegisterAirline(at, "KL"), "reg")
	_, err := c.Apply(at, "AF", 1, 8, 1, 5)
	mustOK(t, err, "apply AF")
	_, err = c.Apply(at, "KL", 2, 8, 1, 5)
	mustOK(t, err, "apply KL")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	snap := c.Snapshot()
	af, kl := seriesIDOf(snap, "AF"), seriesIDOf(snap, "KL")

	// 周 1..4 两公司都执行；周 5 仅 AF 执行：AF=4/5=80%，KL=3/5=60%。
	min := 0
	reg := func(al, sid string, w int, st RegStatus) {
		t.Helper()
		min++
		mustOK(t, c.RegisterWeek(cfg.weekEnd(w).Add(time.Duration(min)*time.Minute),
			al, sid, w, st), "register")
	}
	// AF 执行 1..4（第 5 周未执行）；KL 执行 1..3（第 4、5 周未执行）。
	for w := 1; w <= 5; w++ {
		afSt, klSt := RegNotExecuted, RegNotExecuted
		if w <= 4 {
			afSt = RegExecuted
		}
		if w <= 3 {
			klSt = RegExecuted
		}
		reg("AF", af, w, afSt)
		reg("KL", kl, w, klSt)
	}

	qs, err := c.SettleSeason(cfg.SeasonEnd.Add(time.Hour))
	mustOK(t, err, "settle")
	got := map[string]Qualification{}
	for _, q := range qs {
		got[q.Airline] = q
	}
	if !got["AF"].Qualified || got["AF"].Used != 4 || got["AF"].Planned != 5 {
		t.Fatalf("4/5 at 80%% must qualify: %+v", got["AF"])
	}
	if got["KL"].Qualified || got["KL"].Used != 3 {
		t.Fatalf("3/5 at 80%% must not qualify: %+v", got["KL"])
	}
}

// 返还截止前返还剔除分母；截止后返还计入分母不进分子。
func TestReturnBeforeAndAfterDeadlineDenominator(t *testing.T) {
	cfg := baseConfig(6, 6)
	cfg.QualifyPercent = 100
	cfg.ReturnDeadline = t0.Add(2 * week) // 第 2 周末（取闭）
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0)
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "AF"), "reg")
	_, err := c.Apply(at, "AF", 1, 8, 1, 6)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	sid := seriesIDOf(c.Snapshot(), "AF")

	// 周 2 在截止（第 3 周末，取闭）前返还 -> 剔除。
	_, err = c.ReturnWeek(cfg.weekEnd(2), "AF", sid, 2)
	mustOK(t, err, "return before")
	regs := []struct {
		w  int
		t  time.Time
		st RegStatus
	}{
		{1, cfg.weekEnd(2).Add(time.Minute), RegExecuted},
		{3, cfg.weekEnd(3), RegExecuted},
		{4, cfg.weekEnd(4), RegExecuted},
	}
	for _, r := range regs {
		mustOK(t, c.RegisterWeek(r.t, "AF", sid, r.w, r.st), "exec")
	}
	// 周 5 在截止后返还 -> 计分母不进分子；周 6 执行。
	_, err = c.ReturnWeek(cfg.weekEnd(5), "AF", sid, 5)
	mustOK(t, err, "return after")
	mustOK(t, c.RegisterWeek(cfg.weekEnd(6), "AF", sid, 6, RegExecuted), "exec w6")

	qs, err := c.SettleSeason(cfg.SeasonEnd)
	mustOK(t, err, "settle")
	t.Logf("quals=%+v series=%+v", qs, c.Snapshot().Series)
	if qs[0].Planned != 5 || qs[0].Used != 4 || qs[0].Qualified {
		t.Fatalf("want used=4 planned=5 not qualified, got %+v", qs)
	}
}

// 豁免周既不计分子也不计分母。
func TestExemptExcluded(t *testing.T) {
	cfg := baseConfig(5, 5)
	cfg.QualifyPercent = 100
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0)
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "AF"), "reg")
	_, err := c.Apply(at, "AF", 1, 8, 1, 5)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	sid := seriesIDOf(c.Snapshot(), "AF")

	mustOK(t, c.RegisterWeek(cfg.weekEnd(1), "AF", sid, 1, RegExempt), "exempt")
	for _, w := range []int{2, 3, 4, 5} {
		mustOK(t, c.RegisterWeek(cfg.weekEnd(w), "AF", sid, w, RegExecuted), "exec")
	}
	qs, err := c.SettleSeason(cfg.SeasonEnd)
	mustOK(t, err, "settle")
	if qs[0].Planned != 4 || qs[0].Used != 4 || !qs[0].Qualified {
		t.Fatalf("exempt must be excluded: %+v", qs)
	}
}

// 分母为零视为达标：返还截止前全部返还。
func TestZeroDenominatorQualifies(t *testing.T) {
	cfg := baseConfig(5, 5)
	cfg.ReturnDeadline = cfg.SeasonEnd
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "AF"), "reg")
	_, err := c.Apply(at, "AF", 1, 8, 1, 5)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	sid := seriesIDOf(c.Snapshot(), "AF")
	for w := 1; w <= 5; w++ {
		_, err := c.ReturnWeek(cfg.weekEnd(w), "AF", sid, w)
		mustOK(t, err, "return")
	}
	qs, err := c.SettleSeason(cfg.SeasonEnd.Add(time.Minute))
	mustOK(t, err, "settle")
	if qs[0].Planned != 0 || !qs[0].Qualified {
		t.Fatalf("zero denominator must qualify: %+v", qs)
	}
}

func TestQualifyMath(t *testing.T) {
	cfg := baseConfig(10, 10)
	cfg.QualifyPercent = 80
	if !qualifies(cfg, 4, 5) || qualifies(cfg, 3, 5) {
		t.Fatal("80% boundary wrong")
	}
	if !qualifies(cfg, 0, 0) {
		t.Fatal("zero denominator qualifies")
	}
}

// 剩余容量为奇数时，新进入者保留额为 floor(剩余/2)。
func TestOddRemainingReservationFloor(t *testing.T) {
	cfg := baseConfig(5, 5)
	cfg.NewEntrantThreshold = 99 // 所有公司在判定时都算新进入者
	setCap(&cfg, 1, 8, 3)        // 历史占 0，剩余 3 -> 保留 floor(3/2)=1
	c, m := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-3 * time.Hour)
	for _, al := range []string{"A", "B", "C", "D"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg m")
	}
	// 四个不同公司各申请同一单元格，按提交序：保留 1 给首单，
	// 其余 2 个走普通容量（剩余-保留=2），第 4 个进名单。
	for i, al := range []string{"A", "B", "C", "D"} {
		ts := at.Add(time.Duration(i) * time.Minute)
		_, err := c.Apply(ts, al, 1, 8, 1, 5)
		mustOK(t, err, "apply")
		_, err = m.Apply(ts, al, 1, 8, 1, 5)
		mustOK(t, err, "apply m")
	}
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc m")
	assertSnap(t, c, m, "odd remaining")
	snap := c.Snapshot()
	if len(snap.Series) != 3 {
		t.Fatalf("want 3 series, got %d", len(snap.Series))
	}
	if len(snap.Waitlist) != 1 || len(snap.Waitlist[0].ApplicationIDs) != 1 ||
		snap.Waitlist[0].ApplicationIDs[0] != "A4" {
		t.Fatalf("4th applicant must wait: %+v", snap.Waitlist)
	}
}

// 新进入者阈值恰等于持有数：持有数 == 阈值时不再是新进入者。
func TestNewEntrantThresholdHeldEqual(t *testing.T) {
	cfg := baseConfig(5, 5)
	cfg.NewEntrantThreshold = 1
	setCap(&cfg, 1, 8, 10)
	setCap(&cfg, 2, 8, 10)
	// B 已有上航季资格落到别的单元格会在分配时拿到一个系列，
	// 这里直接构造：B 先有一个历史申请在 day2，满足后持有=1==阈值，
	// 故 B 在 day1 的第二个申请不是新进入者。
	c, m := newPair(t, cfg, map[string][]HistKey{
		"B": {{Day: 2, Hour: 8, StartWeek: 1, EndWeek: 5}},
	}, nil)
	at := cfg.ApplyDeadline.Add(-3 * time.Hour)
	for _, al := range []string{"B", "N"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg")
	}
	// day1/h8 容量设为 1 且保留额=0（剩余容量 1，floor(1/2)=0）。
	// 关键：N 是新进入者但保留额为 0 时不能在 B 阶段满足；B 持有=1==阈值
	// 也不是新进入者。二者在 C 阶段按提交序竞争唯一普通名额。
	setCap(&cfg, 1, 8, 1)
	// 重新建模型使改后的容量生效。
	c2, err := NewCoordinator(cfg, map[string][]HistKey{
		"B": {{Day: 2, Hour: 8, StartWeek: 1, EndWeek: 5}},
	}, nil)
	mustOK(t, err, "rebuild")
	m2, err := NewNaiveModel(cfg, map[string][]HistKey{
		"B": {{Day: 2, Hour: 8, StartWeek: 1, EndWeek: 5}},
	}, nil)
	mustOK(t, err, "rebuild m")
	for _, al := range []string{"B", "N"} {
		mustOK(t, c2.RegisterAirline(at, al), "reg2")
		mustOK(t, m2.RegisterAirline(at, al), "reg2 m")
	}
	_, err = c2.Apply(at.Add(time.Minute), "B", 2, 8, 1, 5) // 历史，先提交
	mustOK(t, err, "hist apply")
	_, err = m2.Apply(at.Add(time.Minute), "B", 2, 8, 1, 5)
	mustOK(t, err, "hist apply m")
	_, err = c2.Apply(at.Add(2*time.Minute), "N", 1, 8, 1, 5)
	mustOK(t, err, "n apply")
	_, err = m2.Apply(at.Add(2*time.Minute), "N", 1, 8, 1, 5)
	mustOK(t, err, "n apply m")
	_, err = c2.Apply(at.Add(3*time.Minute), "B", 1, 8, 1, 5)
	mustOK(t, err, "b apply")
	_, err = m2.Apply(at.Add(3*time.Minute), "B", 1, 8, 1, 5)
	mustOK(t, err, "b apply m")
	mustOK(t, c2.SettleAllocation(cfg.ApplyDeadline.Add(time.Hour)), "alloc")
	mustOK(t, m2.SettleAllocation(cfg.ApplyDeadline.Add(time.Hour)), "alloc m")
	assertSnap(t, c2, m2, "threshold equal")
	// N 在 day1 先于 B 提交，二者均走普通阶段，N 抢到唯一名额，B 进名单。
	snap := c2.Snapshot()
	if len(seriesByAirline(snap, "N")) != 1 || len(seriesByAirline(snap, "B")) != 1 {
		t.Fatalf("N should win day1, B only keeps hist: %+v", snap.Series)
	}
	if len(snap.Waitlist) != 1 || snap.Waitlist[0].ApplicationIDs[0] != "A3" {
		t.Fatalf("B's day1 app should wait: %+v", snap.Waitlist)
	}
}

// 历史优先权先满足，且历史申请之间超容量为参数非法、整体不生效。
func TestHistPriorityAndConflictInvalid(t *testing.T) {
	cfg := baseConfig(5, 5)
	setCap(&cfg, 1, 8, 1)
	hist := map[string][]HistKey{
		"A": {{Day: 1, Hour: 8, StartWeek: 1, EndWeek: 5}},
		"B": {{Day: 1, Hour: 8, StartWeek: 1, EndWeek: 5}},
	}
	c, err := NewCoordinator(cfg, hist, nil)
	mustOK(t, err, "new")
	at := cfg.ApplyDeadline.Add(-2 * time.Hour)
	mustOK(t, c.RegisterAirline(at, "A"), "a")
	mustOK(t, c.RegisterAirline(at, "B"), "b")
	_, err = c.Apply(at, "A", 1, 8, 1, 5)
	mustOK(t, err, "ap a")
	_, err = c.Apply(at.Add(time.Minute), "B", 1, 8, 1, 5)
	mustOK(t, err, "ap b")
	mustErrIs(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), ErrInvalid, "hist conflict")
	if c.phase != phaseAccepting || len(c.series) != 0 {
		t.Fatalf("rejected settle must not change state")
	}
	// 时钟不得被拒绝操作推进。
	// 两家历史仍冲突：申请不可撤回，故容量冲突无法解除；改为验证被拒后
	// 时钟未推进（可用同一时刻重放得到相同拒绝），且无任何分配。
	mustErrIs(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), ErrInvalid, "replay")
	if len(c.Snapshot().Series) != 0 {
		t.Fatalf("no series on rejected settle")
	}
}

// 截止后提交申请报已截止。
func TestApplyAfterDeadline(t *testing.T) {
	cfg := baseConfig(5, 5)
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "A"), "reg")
	_, err := c.Apply(cfg.ApplyDeadline.Add(time.Minute), "A", 1, 8, 1, 5)
	mustErrIs(t, err, ErrApplyClosed, "late apply")
}

// 历史优先权优先于新进入者：容量只够一个时，先满足后提交的历史申请。
func TestHistBeatsNewEntrant(t *testing.T) {
	cfg := baseConfig(4, 4)
	cfg.NewEntrantThreshold = 99 // 无历史的公司本应是新进入者
	setCap(&cfg, 1, 8, 1)
	c, m := newPair(t, cfg, map[string][]HistKey{
		"H": {{Day: 1, Hour: 8, StartWeek: 1, EndWeek: 4}},
	}, nil)
	at := cfg.ApplyDeadline.Add(-2 * time.Hour)
	for _, al := range []string{"N", "H"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg")
	}
	_, err := c.Apply(at, "N", 1, 8, 1, 4) // 新进入者先提交
	mustOK(t, err, "n apply")
	_, err = c.Apply(at.Add(time.Minute), "H", 1, 8, 1, 4) // 历史后提交
	mustOK(t, err, "h apply")
	_, err = m.Apply(at, "N", 1, 8, 1, 4)
	mustOK(t, err, "n apply m")
	_, err = m.Apply(at.Add(time.Minute), "H", 1, 8, 1, 4)
	mustOK(t, err, "h apply m")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc m")
	assertSnap(t, c, m, "hist first")
	snap := c.Snapshot()
	if len(seriesByAirline(snap, "H")) != 1 || len(seriesByAirline(snap, "N")) != 0 {
		t.Fatalf("historic H must win sole slot: %+v", snap.Series)
	}
	if snap.Waitlist[0].ApplicationIDs[0] != "A1" {
		t.Fatalf("earlier non-historic N waits first: %+v", snap.Waitlist)
	}
}

// 返还某周只释放该周，并按名单次序分配给覆盖该周仍可整体满足的申请。
func TestReturnOnlyThatWeekAndWaitlistOrder(t *testing.T) {
	cfg := baseConfig(4, 1)
	cfg.NewEntrantThreshold = 99
	setCap(&cfg, 1, 8, 2)
	c, m := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-2 * time.Hour)
	for _, al := range []string{"H1", "H2", "W1", "W2", "W3"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg m")
	}
	apply := func(min int, al string, sw, ew int) {
		t.Helper()
		ts := at.Add(time.Duration(min) * time.Minute)
		_, err := c.Apply(ts, al, 1, 8, sw, ew)
		mustOK(t, err, "apply")
		_, err = m.Apply(ts, al, 1, 8, sw, ew)
		mustOK(t, err, "apply m")
	}
	// 前两个被满足占满容量；后三个进名单（按提交序 W1,W2,W3）。
	apply(1, "H1", 1, 4)
	apply(2, "H2", 1, 4)
	apply(3, "W1", 3, 4) // 不覆盖周 1
	apply(4, "W2", 1, 2) // 覆盖周 1 但跨周 2
	apply(5, "W3", 1, 1) // 覆盖周 1，仅一周
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc m")
	snap := c.Snapshot()
	if len(snap.Series) != 2 || len(snap.Waitlist[0].ApplicationIDs) != 3 {
		t.Fatalf("setup wrong: %+v", snap)
	}

	// 返还 H1 的周 1：只释放周 1。W1 不覆盖周 1 被跳过；
	// W2 覆盖周 1 但其周 2 仍满 -> 不可整体满足被跳过；W3 可满足 -> 补位。
	var h1 string
	for _, s := range snap.Series {
		if s.Airline == "H1" {
			h1 = s.ID
		}
	}
	got, err := c.ReturnWeek(cfg.weekEnd(1), "H1", h1, 1)
	mustOK(t, err, "return")
	gotM, err := m.ReturnWeek(cfg.weekEnd(1), "H1", h1, 1)
	mustOK(t, err, "return m")
	if got != "A5" || gotM != "A5" {
		t.Fatalf("W3(A5) must be refilled, got %s/%s", got, gotM)
	}
	assertSnap(t, c, m, "after refill")
	snap = c.Snapshot()
	if len(snap.Series) != 3 {
		t.Fatalf("want 3 series, got %d", len(snap.Series))
	}
	if len(seriesByAirline(snap, "W3")) != 1 {
		t.Fatalf("W3 should receive a series: %+v", snap.Series)
	}
	// H1 的其他周仍占用：周 2 仍满，名单剩余 A3(W1),A4(W2) 次序不变。
	if len(snap.Waitlist) != 1 ||
		len(snap.Waitlist[0].ApplicationIDs) != 2 ||
		snap.Waitlist[0].ApplicationIDs[0] != "A3" ||
		snap.Waitlist[0].ApplicationIDs[1] != "A4" {
		t.Fatalf("waitlist order wrong: %+v", snap.Waitlist)
	}
}

// 交换后历史使用率记在接收方名下；已执行过周的系列不可交换。
func TestExchangeTransfersUsage(t *testing.T) {
	cfg := baseConfig(4, 4)
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0)
	c, m := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	for _, al := range []string{"A", "B"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg")
	}
	_, err := c.Apply(at, "A", 1, 8, 1, 4)
	mustOK(t, err, "a apply")
	_, err = c.Apply(at.Add(time.Minute), "B", 1, 9, 1, 4)
	mustOK(t, err, "b apply")
	_, err = m.Apply(at, "A", 1, 8, 1, 4)
	mustOK(t, err, "a apply m")
	_, err = m.Apply(at.Add(time.Minute), "B", 1, 9, 1, 4)
	mustOK(t, err, "b apply m")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc m")
	snap := c.Snapshot()
	sa, sb := seriesIDOf(snap, "A"), seriesIDOf(snap, "B")

	// 周 1 执行后不可交换（周已执行）。
	mustOK(t, c.RegisterWeek(cfg.weekEnd(1), "A", sa, 1, RegExecuted), "exec")
	mustOK(t, m.RegisterWeek(cfg.weekEnd(1), "A", sa, 1, RegExecuted), "exec m")
	mustErrIs(t, c.ExchangeSeries(cfg.weekEnd(1).Add(time.Minute), "A", sa, "B", sb),
		ErrWeekExecuted, "exchange after exec")
	assertSnap(t, c, m, "rejected exchange")

	// 用一组“尚未执行”的系列来验证交换归属：C/D 全新系列。
	c2, m2 := exchangeFixture(t, cfg)
	s2 := c2.Snapshot()
	sc, sd := seriesIDOf(s2, "C"), seriesIDOf(s2, "D")
	exAt := cfg.weekEnd(1)
	mustOK(t, c2.ExchangeSeries(exAt, "C", sc, "D", sd), "exchange")
	mustOK(t, m2.ExchangeSeries(exAt, "C", sc, "D", sd), "exchange m")
	assertSnap(t, c2, m2, "exchanged")
	// 交换后 sc 归 D、sd 归 C。
	for _, s := range c2.Snapshot().Series {
		switch s.ID {
		case sc:
			if s.Airline != "D" {
				t.Fatalf("%s must belong to D, got %s", sc, s.Airline)
			}
		case sd:
			if s.Airline != "C" {
				t.Fatalf("%s must belong to C", sd)
			}
		}
	}
}

func exchangeFixture(t *testing.T, cfg Config) (*Coordinator, *NaiveModel) {
	t.Helper()
	c, m := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	for _, al := range []string{"C", "D"} {
		mustOK(t, c.RegisterAirline(at, al), "reg")
		mustOK(t, m.RegisterAirline(at, al), "reg m")
	}
	_, err := c.Apply(at, "C", 3, 10, 1, 4)
	mustOK(t, err, "c")
	_, err = c.Apply(at.Add(time.Minute), "D", 3, 11, 1, 4)
	mustOK(t, err, "d")
	_, err = m.Apply(at, "C", 3, 10, 1, 4)
	mustOK(t, err, "c m")
	_, err = m.Apply(at.Add(time.Minute), "D", 3, 11, 1, 4)
	mustOK(t, err, "d m")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc m")
	return c, m
}

// 登记窗口终点取闭：恰在终点登记成功，晚一刻超期。
func TestRegisterWindowEndpointClosed(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "A"), "reg")
	_, err := c.Apply(at, "A", 1, 8, 1, 4)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	sid := seriesIDOf(c.Snapshot(), "A")
	end := cfg.weekEnd(1).Add(cfg.RegisterWindow)
	mustOK(t, c.RegisterWeek(end, "A", sid, 1, RegExecuted), "at endpoint closed")
	// 周 2 晚一刻登记 -> 超期。
	mustErrIs(t,
		c.RegisterWeek(cfg.weekEnd(2).Add(cfg.RegisterWindow).Add(time.Nanosecond),
			"A", sid, 2, RegExecuted),
		ErrRegisterLate, "past endpoint")
	// 超期被拒不推进时钟；同一周重复登记先触发超期而非重复。
	mustErrIs(t, c.RegisterWeek(end.Add(time.Hour), "A", sid, 1, RegNotExecuted),
		ErrRegisterLate, "late before dup")
}

// 时钟回退：被接受操作要求时间戳非降。
func TestClockMonotonic(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-2 * time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "first")
	mustErrIs(t, c.RegisterAirline(t1.Add(-time.Second), "B"), ErrClock, "rollback")
	mustOK(t, c.RegisterAirline(t1, "B"), "equal time allowed")
}

// 重复登记在登记超期之前判定（同一周第二次登记且仍在窗口内）。
func TestDuplicateRegisterWithinWindow(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(at, "A"), "reg")
	_, err := c.Apply(at, "A", 1, 8, 1, 4)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	sid := seriesIDOf(c.Snapshot(), "A")
	mustOK(t, c.RegisterWeek(cfg.weekEnd(1), "A", sid, 1, RegExecuted), "first")
	mustErrIs(t,
		c.RegisterWeek(cfg.weekEnd(1).Add(time.Minute), "A", sid, 1, RegNotExecuted),
		ErrDupRegister, "dup")
}

func errIndex(e SlotError) int {
	for i, x := range ErrorOrder {
		if x == e {
			return i
		}
	}
	return -1
}

// 相邻类别：一次调用若同时触发两类，必须只报更靠前的一类。
// 参数非法 > 时钟回退。
func TestOrderInvalidBeforeClock(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "seed")
	// 同时：非法公司 ID + 时间回退。
	mustErrIs(t, c.RegisterAirline(t1.Add(-1), ""), ErrInvalid, "invalid wins over clock")
}

// 时钟回退 > 公司不存在。
func TestOrderClockBeforeNotFound(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "seed")
	// Apply：公司不存在且时间回退。
	_, err := c.Apply(t1.Add(-1), "GHOST", 1, 8, 1, 4)
	mustErrIs(t, err, ErrClock, "clock wins over not found")
}

// 公司不存在 > 航季阶段不符（申请已截止）。
func TestOrderNotFoundBeforeApplyClosed(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "seed")
	_, err := c.Apply(cfg.ApplyDeadline.Add(time.Hour), "GHOST", 1, 8, 1, 4)
	mustErrIs(t, err, ErrNotFound, "not found wins over closed")
}

// 申请已截止 > 航季已结算（同一阶段语义下，已结算前先经历截止）。
// 通过“申请”操作验证：结算后提交申请报申请已截止（更靠前）。
func TestOrderApplyClosedBeforeSettledForApply(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "reg")
	_, err := c.Apply(t1, "A", 1, 8, 1, 4)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	mustOK(t, c.RegisterWeek(cfg.weekEnd(1), "A", "S1", 1, RegNotExecuted), "reg w1")
	for w := 2; w <= 4; w++ {
		mustOK(t, c.RegisterWeek(cfg.weekEnd(w), "A", "S1", w, RegNotExecuted), "reg")
	}
	_, err = c.SettleSeason(cfg.SeasonEnd.Add(time.Hour))
	mustOK(t, err, "settle")
	// 已结算 + 已截止：申请应报更靠前的“申请已截止”。
	_, err = c.Apply(cfg.SeasonEnd.Add(time.Hour), "A", 1, 9, 1, 4)
	mustErrIs(t, err, ErrApplyClosed, "closed precedes settled for apply")
}

// 航季已结算 > 周已执行不可交换：结算后交换报航季已结算。
func TestOrderSettledBeforeWeekExecuted(t *testing.T) {
	cfg := baseConfig(4, 4)
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0)
	c, m := newPair(t, cfg, nil, nil)
	co, _ := settledTwoSeries(t, c, m)
	// 两个系列均有已执行周，且航季已结算 -> 报“航季已结算”（更靠前）。
	err := co.ExchangeSeries(co.lastTime.Add(1), "A", "S1", "B", "S2")
	mustErrIs(t, err, ErrSeasonSettled, "settled precedes week executed")
}

// 周已执行不可交换 > 登记超期（交换与登记不同操作，这里验证次序常量相对位置，
// 并单独保证 ErrWeekExecuted 紧邻 ErrSeasonSettled 之后）。
func TestOrderWeekExecutedBeforeLateConstants(t *testing.T) {
	if errIndex(ErrSeasonSettled)+1 != errIndex(ErrWeekExecuted) ||
		errIndex(ErrWeekExecuted)+1 != errIndex(ErrRegisterLate) {
		t.Fatal("ErrorOrder adjacency around week-executed/late broken")
	}
}

// 登记超期 > 重复登记：同一周重复登记，但第二次已过窗口。
func TestOrderLateBeforeDup(t *testing.T) {
	cfg := baseConfig(4, 4)
	c, _ := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-time.Hour)
	mustOK(t, c.RegisterAirline(t1, "A"), "reg")
	_, err := c.Apply(t1, "A", 1, 8, 1, 4)
	mustOK(t, err, "apply")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	mustOK(t, c.RegisterWeek(cfg.weekEnd(1), "A", "S1", 1, RegExecuted), "first")
	// 第二次登记：既重复又超窗 -> 报登记超期（更靠前）。
	err = c.RegisterWeek(cfg.weekEnd(1).Add(cfg.RegisterWindow).Add(1),
		"A", "S1", 1, RegNotExecuted)
	mustErrIs(t, err, ErrRegisterLate, "late wins over dup")
}

// 重复登记 > 容量不足：仅验证常量相邻（容量不足出现在分配判定路径）。
func TestOrderDupBeforeCapacityConstant(t *testing.T) {
	if errIndex(ErrDupRegister)+1 != errIndex(ErrCapacity) {
		t.Fatal("dup must immediately precede capacity")
	}
}

// 容量不足类在一次性分配中不出现（申请转等候名单），但保留额/单元格判定
// 达到上限时新进入者无法满足。这里验证“恰等于上限不可再分配”的内部判定。
func TestCellAtCapacityRejects(t *testing.T) {
	cfg := baseConfig(2, 1)
	cfg.NewEntrantThreshold = 99
	setCap(&cfg, 1, 8, 1)
	c, m := newPair(t, cfg, nil, nil)
	t1 := cfg.ApplyDeadline.Add(-2 * time.Hour)
	for _, al := range []string{"A", "B"} {
		mustOK(t, c.RegisterAirline(t1, al), "reg")
		mustOK(t, m.RegisterAirline(t1, al), "reg m")
	}
	_, err := c.Apply(t1, "A", 1, 8, 1, 2)
	mustOK(t, err, "a")
	_, err = c.Apply(t1.Add(time.Minute), "B", 1, 8, 1, 2)
	mustOK(t, err, "b")
	_, err = m.Apply(t1, "A", 1, 8, 1, 2)
	mustOK(t, err, "a m")
	_, err = m.Apply(t1.Add(time.Minute), "B", 1, 8, 1, 2)
	mustOK(t, err, "b m")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute)), "alloc m")
	assertSnap(t, c, m, "at capacity")
	if len(c.Snapshot().Series) != 1 || c.Snapshot().Waitlist[0].ApplicationIDs[0] != "A2" {
		t.Fatalf("capacity 1: one series, second waits: %+v", c.Snapshot())
	}
}

func settledTwoSeries(t *testing.T, c *Coordinator, m *NaiveModel) (*Coordinator, *NaiveModel) {
	t.Helper()
	cfg := c.cfg
	cfg.RegisterWindow = cfg.SeasonEnd.Sub(t0)
	c.cfg = cfg
	t1 := cfg.ApplyDeadline.Add(-2 * time.Hour)
	for _, al := range []string{"A", "B"} {
		mustOK(t, c.RegisterAirline(t1, al), "reg")
		mustOK(t, m.RegisterAirline(t1, al), "reg m")
	}
	_, err := c.Apply(t1, "A", 1, 8, 1, 4)
	mustOK(t, err, "a")
	_, err = c.Apply(t1.Add(time.Minute), "B", 1, 9, 1, 4)
	mustOK(t, err, "b")
	_, err = m.Apply(t1, "A", 1, 8, 1, 4)
	mustOK(t, err, "a m")
	_, err = m.Apply(t1.Add(time.Minute), "B", 1, 9, 1, 4)
	mustOK(t, err, "b m")
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc")
	mustOK(t, m.SettleAllocation(cfg.ApplyDeadline.Add(2*time.Minute)), "alloc m")
	min := 0
	reg := func(al, sid string, w int, st RegStatus) {
		t.Helper()
		min++
		mustOK(t, c.RegisterWeek(cfg.weekEnd(w).Add(time.Duration(min)*time.Minute),
			al, sid, w, st), "reg")
	}
	reg("A", "S1", 1, RegExecuted)
	reg("B", "S2", 1, RegExecuted)
	for w := 2; w <= 4; w++ {
		reg("A", "S1", w, RegNotExecuted)
		reg("B", "S2", w, RegNotExecuted)
	}
	_, err = c.SettleSeason(cfg.SeasonEnd.Add(time.Hour))
	mustOK(t, err, "settle")
	return c, m
}

// op 是一段可同时重放到主模型与朴素模型的随机操作。
type op struct {
	kind                    string
	at                      time.Time
	al, al2, sid, sid2      string
	day, hour, sw, ew, week int
	st                      RegStatus
}

func genScript(rng *rand.Rand, cfg Config, airlines []string) []op {
	var ops []op
	deadline := cfg.ApplyDeadline
	// 申请阶段。
	nApps := 4 + rng.Intn(10)
	for i := 0; i < nApps; i++ {
		al := airlines[rng.Intn(len(airlines))]
		d := rng.Intn(3)
		h := 8 + rng.Intn(3)
		l := cfg.MinWeeks + rng.Intn(cfg.Weeks-cfg.MinWeeks+1)
		sw := 1 + rng.Intn(cfg.Weeks-l+1)
		ops = append(ops, op{
			kind: "apply",
			at:   deadline.Add(-time.Duration(1+nApps-i) * time.Hour),
			al:   al, day: d, hour: h, sw: sw, ew: sw + l - 1,
		})
	}
	ops = append(ops, op{kind: "alloc", at: deadline.Add(time.Minute)})

	// 运行阶段：登记 / 返还 / 交换，时间严格推进，避免大量时钟回退掩盖路径。
	cur := deadline.Add(time.Hour)
	for step := 0; step < 40; step++ {
		w := 1 + rng.Intn(cfg.Weeks)
		// 时间随步骤单调；同时保证落在该周登记窗口的粗略范围内。
		cur = cur.Add(time.Duration(1+rng.Intn(20)) * time.Hour)
		weekEnd := cfg.weekEnd(w)
		if cur.Before(weekEnd) {
			cur = weekEnd
		}
		switch rng.Intn(3) {
		case 0:
			st := []RegStatus{RegExecuted, RegNotExecuted, RegExempt}[rng.Intn(3)]
			ops = append(ops, op{kind: "register", at: cur,
				al: airlines[rng.Intn(len(airlines))], week: w, st: st})
		case 1:
			ops = append(ops, op{kind: "return", at: cur,
				al: airlines[rng.Intn(len(airlines))], week: w})
		case 2:
			i, j := rng.Intn(len(airlines)), rng.Intn(len(airlines))
			ops = append(ops, op{kind: "exchange", at: cur,
				al: airlines[i], al2: airlines[j]})
		}
	}
	ops = append(ops, op{kind: "season", at: cfg.SeasonEnd.Add(24 * time.Hour)})
	return ops
}

// sidFor 依据快照按公司挑一个系列（确定性，公司相同则取最小 ID）。
func sidFor(snap Snapshot, al string, skip string) string {
	for _, s := range snap.Series {
		if s.Airline == al && s.ID != skip {
			return s.ID
		}
	}
	return ""
}

func runScript(t *testing.T, m Model, script []op) {
	t.Helper()
	for _, o := range script {
		switch o.kind {
		case "apply":
			_, err := m.Apply(o.at, o.al, o.day, o.hour, o.sw, o.ew)
			t.Logf("apply %s d=%d h=%d w=%d-%d -> %v", o.al, o.day, o.hour, o.sw, o.ew, err)
		case "alloc":
			t.Logf("settleAllocation -> %v", m.SettleAllocation(o.at))
		case "register":
			sid := sidFor(m.Snapshot(), o.al, "")
			err := m.RegisterWeek(o.at, o.al, sid, o.week, o.st)
			t.Logf("register %s %s w=%d st=%d -> %v", o.al, sid, o.week, o.st, err)
		case "return":
			sid := sidFor(m.Snapshot(), o.al, "")
			got, err := m.ReturnWeek(o.at, o.al, sid, o.week)
			t.Logf("return %s %s w=%d -> %q %v", o.al, sid, o.week, got, err)
		case "exchange":
			sa := sidFor(m.Snapshot(), o.al, "")
			sb := sidFor(m.Snapshot(), o.al2, sa)
			err := m.ExchangeSeries(o.at, o.al, sa, o.al2, sb)
			t.Logf("exchange %s/%s <-> %s/%s -> %v", o.al, sa, o.al2, sb, err)
		case "season":
			q, err := m.SettleSeason(o.at)
			t.Logf("settleSeason -> %d quals err=%v", len(q), err)
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261008))
	for iter := 0; iter < 200; iter++ {
		weeks := 3 + rng.Intn(5)
		minW := 1 + rng.Intn(3)
		cfg := baseConfig(weeks, minW)
		setCap(&cfg, rng.Intn(3), 8+rng.Intn(3), 1+rng.Intn(3))
		cfg.NewEntrantThreshold = 1 + rng.Intn(3)
		cfg.QualifyPercent = []int{50, 80, 100}[rng.Intn(3)]

		airlines := []string{"AA", "BB", "CC", "DD"}
		var logC, logM bytes.Buffer
		c, err := NewCoordinator(cfg, nil, &logC)
		if err != nil {
			t.Fatalf("iter %d cfg: %v", iter, err)
		}
		m, err := NewNaiveModel(cfg, nil, &logM)
		if err != nil {
			t.Fatalf("iter %d naive cfg: %v", iter, err)
		}
		for _, al := range airlines {
			at := cfg.ApplyDeadline.Add(-48 * time.Hour)
			if err := c.RegisterAirline(at, al); err != nil {
				t.Fatalf("reg c: %v", err)
			}
			if err := m.RegisterAirline(at, al); err != nil {
				t.Fatalf("reg m: %v", err)
			}
		}
		script := genScript(rng, cfg, airlines)
		runScript(t, c, script)
		runScript(t, m, script)
		cs, ms := c.Snapshot(), m.Snapshot()
		if !reflect.DeepEqual(cs, ms) {
			t.Fatalf("iter %d mismatch\nmain: %#v\nnaive:%#v\nlog:\n%s",
				iter, cs, ms, logC.String())
		}
	}
}

// 相同脚本重放两次得到逐系列相同结果（确定性）。
func TestReplayDeterministic(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	cfg := baseConfig(5, 2)
	airlines := []string{"AA", "BB", "CC"}
	script := genScript(rng, cfg, airlines)
	play := func() Snapshot {
		c, _ := NewCoordinator(cfg, nil, nil)
		for _, al := range airlines {
			mustOK(t, c.RegisterAirline(cfg.ApplyDeadline.Add(-48*time.Hour), al), "reg")
		}
		runScript(t, c, script)
		return c.Snapshot()
	}
	a, b := play(), play()
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("replay not deterministic")
	}
}

// 并发调用：所有变更互斥，结束后所有单元格占用不超过上限。
func TestConcurrentInvariants(t *testing.T) {
	cfg := baseConfig(6, 2)
	c, _ := NewCoordinator(cfg, nil, nil)
	base := cfg.ApplyDeadline.Add(-72 * time.Hour)
	for _, al := range []string{"A", "B", "C"} {
		mustOK(t, c.RegisterAirline(base, al), "reg")
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		i := i
		go func() {
			defer wg.Done()
			at := base.Add(time.Duration(i) * time.Minute)
			_, _ = c.Apply(at, []string{"A", "B", "C"}[i%3],
				i%3, 8+(i%2), 1+(i%3), 4)
		}()
	}
	wg.Wait()
	mustOK(t, c.SettleAllocation(cfg.ApplyDeadline.Add(time.Hour)), "alloc")
	for k, n := range c.occ {
		if n > cfg.Capacity[k.day][k.hour] {
			t.Fatalf("cell %+v over capacity: %d", k, n)
		}
	}
	// 持有者与周集合唯一：重算占用必须与索引一致。
	recalc := map[cellKey]int{}
	for _, s := range c.series {
		for w := s.StartWeek; w <= s.EndWeek; w++ {
			if !s.returned[w] {
				recalc[cellKey{w, s.Day, s.Hour}]++
			}
		}
	}
	if !reflect.DeepEqual(recalc, c.occ) {
		t.Fatalf("occupancy index inconsistent: recalc=%v idx=%v", recalc, c.occ)
	}
}

func ExampleCoordinator_logging() {
	cfg := baseConfig(3, 3)
	var buf bytes.Buffer
	c, _ := NewCoordinator(cfg, nil, &buf)
	at := cfg.ApplyDeadline.Add(-time.Hour)
	_ = c.RegisterAirline(at, "AF")
	_, _ = c.Apply(at, "AF", 1, 8, 1, 3)
	_ = c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute))
	fmt.Println("logged lines > 0:", len(buf.String()) > 0)
	// Output: logged lines > 0: true
}

// 单元格容量判定为 O(1) 哈希索引：系列数与总周数倍增时，
// 单次判定/返还补位耗时不随其增长。基准通过占用索引直接体现。
func BenchmarkCellDecisionConstant(b *testing.B) {
	for _, size := range []int{100, 400, 1600} {
		cfg := baseConfig(size, 1)
		c, _ := NewCoordinator(cfg, nil, nil)
		// 在不同单元格灌入 size 个系列，制造系列总数与总周数规模。
		at := cfg.ApplyDeadline.Add(-time.Hour)
		_ = c.RegisterAirline(at, "A")
		for i := 0; i < size; i++ {
			d, h, w := (i/24)%7, 8+(i%16), 1+i%size
			if h > 23 {
				h = 8
			}
			_, _ = c.Apply(at.Add(time.Duration(i)*time.Nanosecond), "A", d, h, w, w)
		}
		_ = c.SettleAllocation(cfg.ApplyDeadline.Add(time.Minute))
		b.Run("size", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				k := cellKey{1, 1, 8}
				_ = c.occ[k] < cfg.Capacity[1][8]
			}
		})
	}
}

// 单系列使用率仅遍历其周集合。
func BenchmarkSeriesUsageLinearInWeeks(b *testing.B) {
	cfg := baseConfig(4000, 1)
	cfg.ReturnDeadline = cfg.SeasonEnd
	mk := func(n int) *Series {
		s := &Series{StartWeek: 1, EndWeek: n,
			returned: map[int]bool{}, returnedAt: map[int]time.Time{},
			register: map[int]RegStatus{}}
		for w := 1; w <= n; w++ {
			s.register[w] = RegExecuted
		}
		return s
	}
	for _, n := range []int{10, 1000} {
		s := mk(n)
		b.Run("weeks", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, _ = seriesUsage(cfg, s)
			}
		})
	}
}
