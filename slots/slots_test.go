package slots

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)

// day 返回基准时刻后第 n 天的零时。
func day(n int) time.Time { return t0.Add(time.Duration(n) * 24 * time.Hour) }

// stdCfg 是多数用例共用的配置：航季第 10 天开始，共 4 周；
// 第 1..4 周分别结束于第 17/24/31/38 天。
func stdCfg() Config {
	return Config{
		SeasonStart:           day(10),
		TotalWeeks:            4,
		MinWeeks:              1,
		Capacity:              2,
		UsageThresholdPercent: 50,
		NewEntrantThreshold:   2,
		RequestDeadline:       day(9),
		ReturnDeadline:        day(20),
		RegisterWindow:        48 * time.Hour,
	}
}

func newCoord(t *testing.T, cfg Config, elig ...Eligibility) *Coordinator {
	t.Helper()
	c, err := NewCoordinator(cfg, elig)
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c
}

func mustSubmit(t *testing.T, c *Coordinator, at time.Time, carrier string, weekday, hour, sw, ew int) int {
	t.Helper()
	id, err := c.Submit(at, carrier, weekday, hour, sw, ew)
	if err != nil {
		t.Fatalf("Submit(%s,%d,%d,%d-%d): %v", carrier, weekday, hour, sw, ew, err)
	}
	return id
}

func mustAllocate(t *testing.T, c *Coordinator, at time.Time) *AllocationResult {
	t.Helper()
	res, err := c.RunAllocation(at)
	if err != nil {
		t.Fatalf("RunAllocation: %v", err)
	}
	return res
}

func mustRegister(t *testing.T, c *Coordinator, at time.Time, carrier string, id, week int, st WeekStatus) {
	t.Helper()
	if err := c.RegisterWeek(at, carrier, id, week, st); err != nil {
		t.Fatalf("RegisterWeek(series=%d week=%d): %v", id, week, err)
	}
}

func outcomeOf(res *AllocationResult, id int) RequestOutcome {
	for _, o := range res.Outcomes {
		if o.RequestID == id {
			return o
		}
	}
	panic("no such request")
}

func assertErr(t *testing.T, got, want error) {
	t.Helper()
	if got != want {
		t.Fatalf("got error %v, want %v", got, want)
	}
}

func assertUsage(t *testing.T, c *Coordinator, id, executed, planned int, qualified bool) {
	t.Helper()
	e, p, q, err := c.Usage(id)
	if err != nil {
		t.Fatalf("Usage(%d): %v", id, err)
	}
	if e != executed || p != planned || q != qualified {
		t.Fatalf("Usage(%d) = (%d,%d,%v), want (%d,%d,%v)", id, e, p, q, executed, planned, qualified)
	}
}

// dumpState 输出协调器全量可观测状态，用于确定性比对。
func dumpState(c *Coordinator) string {
	var b strings.Builder
	fmt.Fprintf(&b, "alloc=%v settled=%v last=%d\n", c.allocated, c.settled, c.last.Unix())
	for _, r := range c.requests {
		fmt.Fprintf(&b, "Q%d %s %d/%d %d-%d h=%v a=%v\n",
			r.ID, r.Carrier, r.Weekday, r.Hour, r.StartWeek, r.EndWeek, r.Historic, r.Allocated)
	}
	for _, s := range c.series {
		fmt.Fprintf(&b, "S%d %s %d/%d %d-%d ret=%v st=%v\n",
			s.ID, s.Holder, s.Weekday, s.Hour, s.StartWeek, s.EndWeek, s.returned, s.status)
	}
	keys := make([]int, 0, len(c.waitlist))
	for k := range c.waitlist {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	for _, k := range keys {
		if len(c.waitlist[k]) == 0 {
			continue
		}
		fmt.Fprintf(&b, "W%d=%v\n", k, c.waitlist[k])
	}
	for i, r := range c.rem {
		fmt.Fprintf(&b, "C%d=%d ", i, r)
	}
	b.WriteString("\n")
	return b.String()
}

// 使用率恰等于达标比例视为达标，低一个执行单位则不达标。
func TestUsageThresholdBoundary(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 4},
		Eligibility{Carrier: "B", Weekday: 2, Hour: 10, StartWeek: 1, EndWeek: 4})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 4)
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 2, 10, 1, 4)
	mustAllocate(t, c, day(9))

	// A：4 周计划执行 2 周，恰为 50%。
	// B：4 周计划执行 1 周，比达标线低一个执行单位。
	mustRegister(t, c, day(17).Add(time.Hour), "A", 0, 1, Executed)
	mustRegister(t, c, day(17).Add(2*time.Hour), "B", 1, 1, Executed)
	mustRegister(t, c, day(24).Add(time.Hour), "A", 0, 2, Executed)
	mustRegister(t, c, day(24).Add(2*time.Hour), "B", 1, 2, NotExecuted)
	mustRegister(t, c, day(31).Add(time.Hour), "A", 0, 3, NotExecuted)
	mustRegister(t, c, day(31).Add(2*time.Hour), "B", 1, 3, NotExecuted)
	mustRegister(t, c, day(38).Add(time.Hour), "A", 0, 4, NotExecuted)
	mustRegister(t, c, day(38).Add(2*time.Hour), "B", 1, 4, NotExecuted)

	assertUsage(t, c, 0, 2, 4, true)
	assertUsage(t, c, 1, 1, 4, false)

	list, err := c.Settle(day(38).Add(3 * time.Hour))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(list) != 1 || list[0].Carrier != "A" {
		t.Fatalf("eligibilities = %v, want only A", list)
	}
}

// 返还截止前返还的周不计入分母，截止后返还的周计入分母但不计分子。
func TestReturnBeforeVsAfterDeadline(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 4})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 4)
	mustAllocate(t, c, day(9))

	if err := c.Return(day(15), "A", 0, []int{1}); err != nil { // 截止前
		t.Fatalf("Return before deadline: %v", err)
	}
	if err := c.Return(day(21), "A", 0, []int{2}); err != nil { // 截止后
		t.Fatalf("Return after deadline: %v", err)
	}
	if got := c.Remaining(1, 1, 10); got != 2 {
		t.Fatalf("Remaining(week1) = %d, want 2 (released)", got)
	}
	if got := c.Remaining(3, 1, 10); got != 1 {
		t.Fatalf("Remaining(week3) = %d, want 1 (untouched)", got)
	}
	mustRegister(t, c, day(31).Add(time.Hour), "A", 0, 3, Executed)
	mustRegister(t, c, day(38).Add(time.Hour), "A", 0, 4, Executed)
	// 分母 = 4 - 1（截止前返还）= 3，含截止后返还的第 2 周；分子 = 2。
	assertUsage(t, c, 0, 2, 3, true)
}

// 豁免周既不计入分子也不计入分母。
func TestExemptWeeksExcluded(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 4})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 4)
	mustAllocate(t, c, day(9))

	mustRegister(t, c, day(17).Add(time.Hour), "A", 0, 1, Exempt)
	mustRegister(t, c, day(24).Add(time.Hour), "A", 0, 2, Executed)
	mustRegister(t, c, day(31).Add(time.Hour), "A", 0, 3, Executed)
	mustRegister(t, c, day(38).Add(time.Hour), "A", 0, 4, NotExecuted)
	assertUsage(t, c, 0, 2, 3, true)
}

// 分母为零时视为达标。
func TestZeroDenominatorQualified(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	mustAllocate(t, c, day(9))

	if err := c.Return(day(15), "A", 0, []int{1, 2}); err != nil {
		t.Fatalf("Return: %v", err)
	}
	assertUsage(t, c, 0, 0, 0, true)
	list, err := c.Settle(day(38))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if len(list) != 1 || list[0].Carrier != "A" {
		t.Fatalf("eligibilities = %v, want A qualified", list)
	}
}

// 剩余容量为奇数时，新进入者保留额向下取整：容量 3 保留 1，
// 无新进入者时其余申请只能用到剩余容量减保留额为限。
func TestNewEntrantReserveRounding(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 3
	cfg.NewEntrantThreshold = 0 // 没有新进入者
	c := newCoord(t, cfg)
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 1, 10, 1, 2)
	mustSubmit(t, c, day(1).Add(2*time.Hour), "C", 1, 10, 1, 2)
	res := mustAllocate(t, c, day(9))

	if !outcomeOf(res, 0).Allocated || !outcomeOf(res, 1).Allocated {
		t.Fatalf("first two requests should be allocated: %+v", res.Outcomes)
	}
	if outcomeOf(res, 2).Allocated || outcomeOf(res, 2).Reason != ErrCapacity {
		t.Fatalf("third request should wait with ErrCapacity: %+v", outcomeOf(res, 2))
	}
	if got := c.Remaining(1, 1, 10); got != 1 {
		t.Fatalf("Remaining = %d, want 1 (reserved half of 3, floored)", got)
	}
	if got := c.Waitlist(1, 10); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Waitlist = %v, want [2]", got)
	}
}

// 新进入者阈值恰等于持有数时不算新进入者（须严格少于阈值）。
func TestNewEntrantThresholdBoundary(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 4
	cfg.NewEntrantThreshold = 2
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)                  // 历史，持有数变 1
	mustSubmit(t, c, day(1).Add(time.Hour), "A", 1, 10, 1, 2)   // 历史，持有数变 2
	mustSubmit(t, c, day(1).Add(2*time.Hour), "A", 1, 10, 1, 2) // req2：非新进入者
	mustSubmit(t, c, day(1).Add(3*time.Hour), "A", 1, 10, 1, 2) // req3：非新进入者
	mustSubmit(t, c, day(1).Add(4*time.Hour), "B", 2, 10, 1, 2) // req4：新进入者
	res := mustAllocate(t, c, day(9))

	// 历史两条占用后 rem=2，保留额=1。B 作为新进入者用保留额得到满足；
	// A 持有数恰等于阈值 2，只能走“剩余-保留>0”：req2 满足（2-1>0），req3 被保留额挡住。
	if !outcomeOf(res, 4).Allocated {
		t.Fatalf("new entrant B should be allocated: %+v", res.Outcomes)
	}
	if !outcomeOf(res, 2).Allocated {
		t.Fatalf("req2 should be allocated: %+v", res.Outcomes)
	}
	if outcomeOf(res, 3).Allocated {
		t.Fatalf("req3 should be waitlisted (held==threshold is not a new entrant)")
	}
}

// 返还某周只释放该周，并按等候名单次序分配给覆盖该周且仍有效的申请。
func TestReturnReleasesOnlyThatWeekAndWaitlistOrder(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 1
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 3})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 3)                  // req0：历史，占用 1-3 周
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 1, 10, 1, 3)   // req1：等候
	mustSubmit(t, c, day(1).Add(2*time.Hour), "C", 1, 10, 2, 2) // req2：等候
	mustSubmit(t, c, day(1).Add(3*time.Hour), "D", 1, 10, 1, 3) // req3：等候
	mustSubmit(t, c, day(1).Add(4*time.Hour), "G", 1, 10, 1, 1) // req4：等候
	mustSubmit(t, c, day(1).Add(5*time.Hour), "H", 1, 10, 1, 1) // req5：等候
	mustAllocate(t, c, day(9))

	// 返还第 2 周：req1/req3 需 1-3 周全有空余（不满足，留在名单），
	// req2 只需第 2 周，按名单次序得到满足。
	if err := c.Return(day(15), "A", 0, []int{2}); err != nil {
		t.Fatalf("Return week2: %v", err)
	}
	if got := c.Waitlist(1, 10); fmt.Sprint(got) != "[1 3 4 5]" {
		t.Fatalf("Waitlist = %v, want [1 3 4 5]", got)
	}
	for _, w := range []int{1, 2, 3} {
		if got := c.Remaining(w, 1, 10); got != 0 {
			t.Fatalf("Remaining(week%d) = %d, want 0 (only week2 was released and re-taken)", w, got)
		}
	}

	// 返还第 1 周：req1/req3 仍不满足，req4 先于 req5 得到满足（名单次序）。
	if err := c.Return(day(15).Add(time.Hour), "A", 0, []int{1}); err != nil {
		t.Fatalf("Return week1: %v", err)
	}
	if got := c.Waitlist(1, 10); fmt.Sprint(got) != "[1 3 5]" {
		t.Fatalf("Waitlist = %v, want [1 3 5]", got)
	}
	if !c.requests[4].Allocated || c.requests[5].Allocated {
		t.Fatalf("req4 should be allocated before req5")
	}
}

// 交换后系列的历史使用率记在接收方名下。
func TestSwapAttributesUsageToReceiver(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
		Eligibility{Carrier: "B", Weekday: 2, Hour: 11, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 2, 11, 1, 2)
	mustAllocate(t, c, day(9))

	if err := c.Swap(day(15), "A", 0, "B", 1); err != nil {
		t.Fatalf("Swap: %v", err)
	}
	// 系列 0 现归 B：由 B 登记，两周均执行。
	mustRegister(t, c, day(17).Add(time.Hour), "B", 0, 1, Executed)
	// 系列 1 现归 A：执行一周。
	mustRegister(t, c, day(17).Add(2*time.Hour), "A", 1, 1, Executed)
	mustRegister(t, c, day(24).Add(time.Hour), "B", 0, 2, Executed)
	mustRegister(t, c, day(24).Add(2*time.Hour), "A", 1, 2, NotExecuted)

	list, err := c.Settle(day(38))
	if err != nil {
		t.Fatalf("Settle: %v", err)
	}
	want := []Eligibility{
		{Carrier: "B", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
		{Carrier: "A", Weekday: 2, Hour: 11, StartWeek: 1, EndWeek: 2},
	}
	if fmt.Sprint(list) != fmt.Sprint(want) {
		t.Fatalf("eligibilities = %v, want %v (usage attributed to receiver)", list, want)
	}
}

// 双方系列所在周都未执行过才可交换；同公司或周数范围不同属参数非法。
func TestSwapRejections(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
		Eligibility{Carrier: "B", Weekday: 2, Hour: 11, StartWeek: 1, EndWeek: 2},
		Eligibility{Carrier: "B", Weekday: 3, Hour: 11, StartWeek: 1, EndWeek: 3})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 2, 11, 1, 2)
	mustSubmit(t, c, day(1).Add(2*time.Hour), "B", 3, 11, 1, 3)
	mustAllocate(t, c, day(9))

	assertErr(t, c.Swap(day(15), "A", 0, "A", 0), ErrInvalidParam)
	assertErr(t, c.Swap(day(15), "A", 0, "B", 2), ErrInvalidParam) // 周数范围不同
	assertErr(t, c.Swap(day(15), "A", 0, "B", 9), ErrNotFound)
	mustRegister(t, c, day(17).Add(time.Hour), "A", 0, 1, Executed)
	assertErr(t, c.Swap(day(18), "A", 0, "B", 1), ErrWeekExecuted)
}

// 登记窗口终点取闭：恰在终点可登记，过终点报登记超期。
func TestRegisterWindowEndpointInclusive(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 1},
		Eligibility{Carrier: "B", Weekday: 2, Hour: 10, StartWeek: 1, EndWeek: 1})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 1)
	mustSubmit(t, c, day(1).Add(time.Hour), "B", 2, 10, 1, 1)
	mustAllocate(t, c, day(9))

	end := day(19) // 第 1 周结束于第 17 天，窗口 48h，终点第 19 天（取闭）
	if err := c.RegisterWeek(end, "A", 0, 1, Executed); err != nil {
		t.Fatalf("register at window endpoint should succeed: %v", err)
	}
	assertErr(t, c.RegisterWeek(end.Add(time.Nanosecond), "B", 1, 1, Executed), ErrRegistrationLate)
}

// 同一周只能登记一次；登记超期先于重复登记报告。
func TestDuplicateAndLateOrder(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 1})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 1)
	mustAllocate(t, c, day(9))

	mustRegister(t, c, day(18), "A", 0, 1, Executed)
	assertErr(t, c.RegisterWeek(day(18).Add(time.Hour), "A", 0, 1, NotExecuted), ErrDuplicateRegister)
	assertErr(t, c.RegisterWeek(day(20), "A", 0, 1, NotExecuted), ErrRegistrationLate)
}

// 拒绝次序中每一对可共现的相邻类别，只报最靠前的一类。
func TestRejectionOrderAdjacentPairs(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 1})
	mustSubmit(t, c, day(5), "A", 1, 10, 1, 1)

	// 参数非法 > 时钟回退：星期几非法且时刻回退。
	if _, err := c.Submit(day(4), "B", 7, 10, 1, 1); err != ErrInvalidParam {
		t.Fatalf("param vs clock: got %v, want ErrInvalidParam", err)
	}
	// 时钟回退 > 公司或系列不存在：时刻回退且系列不存在。
	assertErr(t, c.Return(day(4), "ghost", 99, []int{1}), ErrClockRegression)

	mustAllocate(t, c, day(9))
	if _, err := c.Settle(day(38)); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// 公司或系列不存在 > 航季阶段不符：已结算且系列不存在。
	assertErr(t, c.RegisterWeek(day(39), "A", 99, 1, Executed), ErrNotFound)
	// 航季阶段不符 > 登记超期：已结算且超出登记窗口。
	assertErr(t, c.RegisterWeek(day(39), "A", 0, 1, Executed), ErrSeasonSettled)
	// 登记超期 > 重复登记：见 TestDuplicateAndLateOrder。
	// 重复登记 > 容量不足：容量不足不产生于登记类操作，
	// 由 TestRejectionRankOrder 与 TestRetryRequest 分别验证次序与容量拒绝。
}

// 拒绝类别序号严格按统一次序递增。
func TestRejectionRankOrder(t *testing.T) {
	chain := []error{
		ErrInvalidParam, ErrClockRegression, ErrNotFound, ErrSeasonSettled,
		ErrRegistrationLate, ErrDuplicateRegister, ErrCapacity,
	}
	for i := 0; i+1 < len(chain); i++ {
		if Rank(chain[i]) >= Rank(chain[i+1]) {
			t.Fatalf("Rank(%v)=%d should precede Rank(%v)=%d",
				chain[i], Rank(chain[i]), chain[i+1], Rank(chain[i+1]))
		}
	}
	for _, e := range []error{ErrRequestsClosed, ErrRequestsOpen, ErrNotAllocated,
		ErrAlreadyAllocated, ErrSeasonNotEnded, ErrWeekExecuted, ErrWeekNotEnded} {
		if !IsPhase(e) {
			t.Fatalf("%v should be a phase error", e)
		}
	}
}

// 被拒绝的操作不推进时钟、不改变状态。
func TestRejectedOpNoSideEffect(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg)
	mustSubmit(t, c, day(5), "A", 1, 10, 1, 1)

	// 参数非法但时刻“未来”的申请被拒绝后，时钟不得推进到该时刻。
	if _, err := c.Submit(day(6), "B", 7, 10, 1, 1); err != ErrInvalidParam {
		t.Fatalf("got %v, want ErrInvalidParam", err)
	}
	if _, err := c.Submit(day(5).Add(time.Hour), "B", 2, 10, 1, 1); err != nil {
		t.Fatalf("clock must not advance on rejection: %v", err)
	}
	// 时钟回退与不存在被拒绝后状态不变。
	before := dumpState(c)
	if _, err := c.Submit(day(3), "C", 3, 10, 1, 1); err != ErrClockRegression {
		t.Fatalf("got %v, want ErrClockRegression", err)
	}
	assertErr(t, c.Return(day(6), "ghost", 99, []int{1}), ErrNotFound)
	after := dumpState(c)
	if after != before {
		t.Fatalf("rejected ops must not change state:\n%s\n---\n%s", before, after)
	}
}

// 航季阶段相关拒绝。
func TestPhaseRejections(t *testing.T) {
	cfg := stdCfg()

	c1 := newCoord(t, cfg)
	if _, err := c1.RunAllocation(day(8)); err != ErrRequestsOpen {
		t.Fatalf("allocate before deadline: got %v, want ErrRequestsOpen", err)
	}
	if _, err := c1.Submit(day(9), "A", 1, 10, 1, 1); err != ErrRequestsClosed {
		t.Fatalf("submit at deadline: got %v, want ErrRequestsClosed", err)
	}

	c2 := newCoord(t, cfg)
	if _, err := c2.Settle(day(9)); err != ErrNotAllocated {
		t.Fatalf("settle before allocation: got %v, want ErrNotAllocated", err)
	}

	c3 := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 1},
		Eligibility{Carrier: "B", Weekday: 2, Hour: 10, StartWeek: 1, EndWeek: 1})
	mustSubmit(t, c3, day(1), "A", 1, 10, 1, 1)
	mustSubmit(t, c3, day(1).Add(time.Hour), "B", 2, 10, 1, 1)
	mustAllocate(t, c3, day(9))
	if _, err := c3.RunAllocation(day(10)); err != ErrAlreadyAllocated {
		t.Fatalf("double allocation: got %v, want ErrAlreadyAllocated", err)
	}
	if _, err := c3.Settle(day(20)); err != ErrSeasonNotEnded {
		t.Fatalf("settle before season end: got %v, want ErrSeasonNotEnded", err)
	}
	if _, err := c3.Settle(day(38)); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	assertErr(t, c3.RegisterWeek(day(38).Add(time.Hour), "A", 0, 1, Executed), ErrSeasonSettled)
	assertErr(t, c3.Return(day(38).Add(time.Hour), "A", 0, []int{1}), ErrSeasonSettled)
	assertErr(t, c3.Swap(day(38).Add(time.Hour), "A", 0, "B", 1), ErrSeasonSettled)
	if _, err := c3.Submit(day(38).Add(time.Hour), "C", 3, 10, 1, 1); err != ErrSeasonSettled {
		t.Fatalf("submit after settle: got %v, want ErrSeasonSettled", err)
	}
	if _, err := c3.Settle(day(39)); err != ErrSeasonSettled {
		t.Fatalf("double settle: got %v, want ErrSeasonSettled", err)
	}
}

// 历史优先权要求同星期几、同小时段、同等周数范围。
func TestHistoricMatchRequiresSameRange(t *testing.T) {
	cfg := stdCfg()
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 3)                  // 周数范围不同
	mustSubmit(t, c, day(1).Add(time.Hour), "A", 1, 11, 1, 2)   // 小时段不同
	mustSubmit(t, c, day(1).Add(2*time.Hour), "A", 1, 10, 1, 2) // 完全匹配
	res := mustAllocate(t, c, day(9))
	if outcomeOf(res, 0).Historic || outcomeOf(res, 1).Historic || !outcomeOf(res, 2).Historic {
		t.Fatalf("historic flags wrong: %+v", res.Outcomes)
	}
}

// 周数不足配置最少周数的系列为参数非法。
func TestMinWeeksParam(t *testing.T) {
	cfg := stdCfg()
	cfg.MinWeeks = 2
	c := newCoord(t, cfg)
	if _, err := c.Submit(day(1), "A", 1, 10, 1, 1); err != ErrInvalidParam {
		t.Fatalf("got %v, want ErrInvalidParam", err)
	}
	if _, err := c.Submit(day(1), "A", 1, 10, 1, 2); err != nil {
		t.Fatalf("two-week request should be accepted: %v", err)
	}
	if _, err := c.Submit(day(1), "A", 1, 10, 3, 9); err != ErrInvalidParam {
		t.Fatalf("out-of-range weeks: got %v, want ErrInvalidParam", err)
	}
}

// 历史申请之间超过容量属参数非法；被拒绝后状态不变、时钟不推进。
func TestHistoricOverflowParamIllegal(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 1
	c := newCoord(t, cfg,
		Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
		Eligibility{Carrier: "B", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	mustSubmit(t, c, day(2), "B", 1, 10, 1, 2)
	if _, err := c.RunAllocation(day(9)); err != ErrInvalidParam {
		t.Fatalf("got %v, want ErrInvalidParam", err)
	}
	if c.allocated {
		t.Fatalf("rejected allocation must not take effect")
	}
	if got := c.Remaining(1, 1, 10); got != 1 {
		t.Fatalf("Remaining = %d, want 1 (unchanged)", got)
	}
	// 时钟未推进：早于第 9 天、晚于上次被接受操作的提交仍可被接受。
	if _, err := c.Submit(day(3), "C", 2, 10, 1, 2); err != nil {
		t.Fatalf("clock must not advance on rejection: %v", err)
	}
}

// 等候中的申请可重试：容量不足报容量不足，容量足够则转为系列。
func TestRetryRequest(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 1
	c := newCoord(t, cfg, Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2})
	mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
	reqB := mustSubmit(t, c, day(1).Add(time.Hour), "B", 1, 10, 1, 2)
	mustAllocate(t, c, day(9))

	assertErr(t, c.RetryRequest(day(10), "B", reqB), ErrCapacity)
	// 只返还第 1 周不够：申请覆盖 1-2 周，全有或全无。
	if err := c.Return(day(15), "A", 0, []int{1}); err != nil {
		t.Fatalf("Return: %v", err)
	}
	assertErr(t, c.RetryRequest(day(16), "B", reqB), ErrCapacity)
	if err := c.Return(day(16).Add(time.Hour), "A", 0, []int{2}); err != nil {
		t.Fatalf("Return: %v", err)
	}
	// 返还第 2 周时等候名单已自动满足 B。
	if !c.requests[reqB].Allocated {
		t.Fatalf("waitlisted request should be promoted after return")
	}
	assertErr(t, c.RetryRequest(day(17), "B", reqB), ErrInvalidParam)
	assertErr(t, c.RetryRequest(day(17), "B", 99), ErrNotFound)
	assertErr(t, c.RetryRequest(day(10), "B", reqB), ErrClockRegression)
}

// 相同申请与操作序列重放得到逐系列相同的结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() string {
		cfg := stdCfg()
		c := newCoord(t, cfg,
			Eligibility{Carrier: "A", Weekday: 1, Hour: 10, StartWeek: 1, EndWeek: 2},
			Eligibility{Carrier: "B", Weekday: 2, Hour: 11, StartWeek: 1, EndWeek: 2})
		mustSubmit(t, c, day(1), "A", 1, 10, 1, 2)
		mustSubmit(t, c, day(1).Add(time.Hour), "B", 2, 11, 1, 2)
		mustSubmit(t, c, day(1).Add(2*time.Hour), "C", 1, 10, 1, 2)
		mustSubmit(t, c, day(1).Add(3*time.Hour), "D", 1, 10, 2, 2)
		mustAllocate(t, c, day(9))
		_ = c.Return(day(15), "A", 0, []int{1})
		_ = c.Swap(day(18), "B", 1, "C", 2)
		mustRegister(t, c, day(19), "C", 1, 1, Executed)
		mustRegister(t, c, day(24).Add(time.Hour), "A", 0, 2, Executed)
		mustRegister(t, c, day(25), "C", 1, 2, NotExecuted)
		if _, err := c.Settle(day(38)); err != nil {
			t.Fatalf("Settle: %v", err)
		}
		return dumpState(c)
	}
	if first, second := run(), run(); first != second {
		t.Fatalf("replay mismatch:\n%s\n---\n%s", first, second)
	}
}

// 并发调用等价于某个串行顺序：不变量保持，重复登记只有一个成功。
func TestConcurrentOps(t *testing.T) {
	cfg := stdCfg()
	cfg.Capacity = 8
	var elig []Eligibility
	for i := 0; i < 8; i++ {
		elig = append(elig, Eligibility{
			Carrier: fmt.Sprintf("C%d", i), Weekday: i % 7, Hour: i, StartWeek: 1, EndWeek: 2})
	}
	c := newCoord(t, cfg, elig...)
	for i := 0; i < 8; i++ {
		mustSubmit(t, c, day(1).Add(time.Duration(i)*time.Hour), fmt.Sprintf("C%d", i), i%7, i, 1, 2)
	}
	mustAllocate(t, c, day(9))

	var wg sync.WaitGroup
	var mu sync.Mutex
	success := map[int]int{}
	reg := func(id, week int, at time.Time, st WeekStatus) {
		defer wg.Done()
		carrier := fmt.Sprintf("C%d", id)
		if err := c.RegisterWeek(at, carrier, id, week, st); err == nil {
			mu.Lock()
			success[id]++
			mu.Unlock()
		} else if err != ErrDuplicateRegister && err != ErrClockRegression {
			t.Errorf("unexpected error: %v", err)
		}
	}
	// 同一时刻并发登记第 1 周（含一次重复），再并发登记第 2 周。
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go reg(i, 1, day(18), Executed)
	}
	wg.Add(1)
	go reg(3, 1, day(18), Executed) // 重复登记：只有一个成功
	wg.Wait()
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go reg(i, 2, day(25), NotExecuted)
	}
	wg.Wait()

	for i := 0; i < 8; i++ {
		if success[i] != 2 {
			t.Fatalf("series %d registered %d times, want 2", i, success[i])
		}
	}
	for w := 1; w <= cfg.TotalWeeks; w++ {
		for wd := 0; wd < 7; wd++ {
			for h := 0; h < 24; h++ {
				if got := c.Remaining(w, wd, h); got < 0 {
					t.Fatalf("capacity violated at week %d weekday %d hour %d", w, wd, h)
				}
			}
		}
	}
}
