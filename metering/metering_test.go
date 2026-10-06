package metering

import (
	"errors"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func codeOf(t *testing.T, err error) ErrCode {
	t.Helper()
	if err == nil {
		return 0
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("非业务错误类型: %v", err)
	}
	return e.Code
}

func mustCode(t *testing.T, err error, want ErrCode) {
	t.Helper()
	if got := codeOf(t, err); got != want {
		t.Fatalf("错误类别 = %v(%v), 期望 %v", got, err, want)
	}
}

func mustAdd(t *testing.T, s *Service, now int64, id string, role Role, rng, area int64) {
	t.Helper()
	if err := s.AddMeter(now, id, role, rng, area); err != nil {
		t.Fatalf("AddMeter(%s): %v", id, err)
	}
}

func mustRecord(t *testing.T, s *Service, now int64, id string, tt, v int64, k Kind) {
	t.Helper()
	if _, err := s.Record(now, id, tt, v, k); err != nil {
		t.Fatalf("Record(%s t=%d v=%d): %v", id, tt, v, err)
	}
}

func mustSettle(t *testing.T, s *Service, now, start, end int64) *Bill {
	t.Helper()
	b, err := s.Settle(now, start, end)
	if err != nil {
		t.Fatalf("Settle([%d,%d)): %v", start, end, err)
	}
	return b
}

// 翻转差额恰为量程一半：视为翻转一次，按跨过上限计用量。
func TestRolloverExactlyHalfRange(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 100, 0)
	mustAdd(t, s, 0, "U1", Unit, 100, 10)
	mustRecord(t, s, 0, "M", 0, 80, Actual)
	mustRecord(t, s, 1, "M", 1, 30, Actual) // 跌落 50 == 100/2 → 翻转
	b := mustSettle(t, s, 2, 0, 2)
	if b.MasterUsage != 50 { // (100-80)+30
		t.Fatalf("总表用量 = %d, 期望 50", b.MasterUsage)
	}
	if b.SharedUsage != 50 || b.Units[0].Share != 50 {
		t.Fatalf("公摊分摊错误: %+v", b)
	}
}

// 翻转差额超过量程一半一个单位：读数非法，拒绝且不留痕。
func TestRolloverBeyondHalfRejected(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 100, 0)
	mustAdd(t, s, 0, "U1", Unit, 100, 10)
	mustRecord(t, s, 0, "M", 0, 80, Actual)
	_, err := s.Record(1, "M", 1, 29, Actual) // 跌落 51 > 100/2
	mustCode(t, err, ErrIllegalReading)
	if n := len(s.byID["M"].entries); n != 1 {
		t.Fatalf("拒绝后读数条目数 = %d, 期望 1", n)
	}
	b := mustSettle(t, s, 2, 0, 2)
	if b.MasterUsage != 0 {
		t.Fatalf("拒绝后总表用量 = %d, 期望 0", b.MasterUsage)
	}
}

// 换表当刻：旧表结算至终读数，新表自起始读数起算，当刻不计用量。
func TestReplacementInstant(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	if _, err := s.ReplaceMeter(10, "M", 10, 50, 5); err != nil {
		t.Fatalf("ReplaceMeter: %v", err)
	}
	mustRecord(t, s, 20, "M", 20, 25, Actual)
	if b := mustSettle(t, s, 20, 0, 10); b.MasterUsage != 50 {
		t.Fatalf("[0,10) 用量 = %d, 期望 50（旧表终读数结算）", b.MasterUsage)
	}
	if b := mustSettle(t, s, 20, 10, 20); b.MasterUsage != 20 {
		t.Fatalf("[10,20) 用量 = %d, 期望 20（新表自起始读数起算）", b.MasterUsage)
	}
	if b := mustSettle(t, s, 20, 0, 20); b.MasterUsage != 70 {
		t.Fatalf("[0,20) 用量 = %d, 期望 70（换表当刻不计用量）", b.MasterUsage)
	}
}

// 读数恰落账期边界：段用量完整归属，不发生线性拆分。
func TestReadingOnPeriodBoundary(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	mustRecord(t, s, 10, "M", 10, 100, Actual)
	mustRecord(t, s, 20, "M", 20, 300, Actual)
	if b := mustSettle(t, s, 20, 0, 10); b.MasterUsage != 100 {
		t.Fatalf("[0,10) 用量 = %d, 期望 100", b.MasterUsage)
	}
	if b := mustSettle(t, s, 20, 10, 20); b.MasterUsage != 200 {
		t.Fatalf("[10,20) 用量 = %d, 期望 200", b.MasterUsage)
	}
}

// 线性归属的余数计入较晚账期。
func TestLinearSplitRemainderGoesLater(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	mustRecord(t, s, 3, "M", 3, 10, Actual) // 用量 10 跨 3 个时刻
	if b := mustSettle(t, s, 3, 0, 1); b.MasterUsage != 3 {
		t.Fatalf("[0,1) 用量 = %d, 期望 3（floor(10/3)）", b.MasterUsage)
	}
	if b := mustSettle(t, s, 3, 1, 3); b.MasterUsage != 7 {
		t.Fatalf("[1,3) 用量 = %d, 期望 7（余数归较晚账期）", b.MasterUsage)
	}
}

// 估抄被时刻更晚的实抄替代（实抄小于估抄），估抄从序列移除，
// 多估部分经负更正退回，原账单不变。
func TestEstimateReplacedByLowerActual(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 100, Actual)
	mustRecord(t, s, 10, "M", 10, 150, Estimate)
	b := mustSettle(t, s, 20, 0, 20) // 用量 50，全部计入公摊
	if b.SharedUsage != 50 || b.Units[0].Share != 50 {
		t.Fatalf("结算结果错误: %+v", b)
	}
	corrs, err := s.Record(20, "M", 15, 120, Actual) // 实抄 120 < 估抄 150
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if n := len(s.byID["M"].entries); n != 2 {
		t.Fatalf("估抄未被移除，条目数 = %d, 期望 2", n)
	}
	if len(corrs) != 1 {
		t.Fatalf("更正数 = %d, 期望 1", len(corrs))
	}
	c := corrs[0]
	if c.SharedDelta != -30 { // 50 → 20，多估 30 退回
		t.Fatalf("公摊差额 = %d, 期望 -30", c.SharedDelta)
	}
	if len(c.Deltas) != 1 || c.Deltas[0].UnitID != "U1" || c.Deltas[0].Delta != -30 {
		t.Fatalf("分户差额错误: %+v", c.Deltas)
	}
	old, _ := s.Bill(0, 20)
	if old.SharedUsage != 50 {
		t.Fatalf("原账单被改动: 公摊 = %d, 期望 50", old.SharedUsage)
	}
}

// 部分在住按比例精确分摊，空置分户全额参与，分摊之和恒等于公摊。
func TestPartialOccupancyPrecisionAndConservation(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 10000, 0)
	mustAdd(t, s, 0, "A", Unit, 10000, 10)
	mustAdd(t, s, 0, "B", Unit, 10000, 10)
	mustAdd(t, s, 0, "C", Unit, 10000, 10)
	if err := s.AddOccupancy(0, "B", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddOccupancy(0, "C", 0, 2); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	mustRecord(t, s, 3, "M", 3, 100, Actual)
	b := mustSettle(t, s, 3, 0, 3)
	// 权重：A 空置 10*3=30，B 在住 1/3 → 10，C 在住 2/3 → 20；公摊 100
	want := []int64{50, 17, 33} // floor 50/16/33，余数名额归余数最大的 B
	var sum int64
	for i, u := range b.Units {
		if u.Share != want[i] {
			t.Fatalf("%s 分摊 = %d, 期望 %d", u.UnitID, u.Share, want[i])
		}
		sum += u.Share
	}
	if sum != b.SharedUsage {
		t.Fatalf("分摊之和 %d != 公摊 %d", sum, b.SharedUsage)
	}

	// 极小公摊在相等权重下的守恒：1 必须完整落给某一户。
	s2 := New(Config{Gap: 5})
	mustAdd(t, s2, 0, "M", Master, 10000, 0)
	mustAdd(t, s2, 0, "X1", Unit, 10000, 10)
	mustAdd(t, s2, 0, "X2", Unit, 10000, 10)
	mustAdd(t, s2, 0, "X3", Unit, 10000, 10)
	mustRecord(t, s2, 0, "M", 0, 0, Actual)
	mustRecord(t, s2, 1, "M", 1, 1, Actual)
	b2 := mustSettle(t, s2, 1, 0, 1)
	var sum2 int64
	for _, u := range b2.Units {
		sum2 += u.Share
	}
	if sum2 != 1 {
		t.Fatalf("公摊 1 的分摊之和 = %d, 期望 1", sum2)
	}
	if b2.Units[0].Share != 1 || b2.Units[1].Share != 0 || b2.Units[2].Share != 0 {
		t.Fatalf("相等余数时应按户号升序分配: %+v", b2.Units)
	}
}

// 一次实抄替代多条估抄，链式触发多个已结算账期的更正；
// 每个账期更正金额之和恒等于该账期公摊重算前后之差。
func TestCorrectionChainAcrossPeriods(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 10000, 0)
	mustAdd(t, s, 0, "U1", Unit, 10000, 10)
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	mustRecord(t, s, 60, "M", 60, 600, Estimate)
	mustRecord(t, s, 120, "M", 120, 1300, Estimate)
	mustRecord(t, s, 250, "M", 250, 2000, Estimate)
	wantShared := map[[2]int64]int64{{0, 100}: 1066, {100, 200}: 664, {200, 300}: 270}
	for _, p := range [][2]int64{{0, 100}, {100, 200}, {200, 300}} {
		b := mustSettle(t, s, 300, p[0], p[1])
		if b.SharedUsage != wantShared[p] {
			t.Fatalf("[%d,%d) 公摊 = %d, 期望 %d", p[0], p[1], b.SharedUsage, wantShared[p])
		}
	}
	corrs, err := s.Record(300, "M", 280, 1000, Actual) // 替代全部估抄
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	wantDelta := map[[2]int64]int64{{0, 100}: -709, {100, 200}: -307, {200, 300}: 16}
	if len(corrs) != 3 {
		t.Fatalf("更正数 = %d, 期望 3（链式）", len(corrs))
	}
	for _, c := range corrs {
		want := wantDelta[[2]int64{c.Start, c.End}]
		if c.SharedDelta != want {
			t.Fatalf("[%d,%d) 公摊差额 = %d, 期望 %d", c.Start, c.End, c.SharedDelta, want)
		}
		var sum int64
		for _, d := range c.Deltas {
			sum += d.Delta
		}
		if sum != c.SharedDelta {
			t.Fatalf("[%d,%d) 各户差额之和 %d != 公摊差额 %d", c.Start, c.End, sum, c.SharedDelta)
		}
	}
	// 原账单不被改动。
	for p, shared := range wantShared {
		b, _ := s.Bill(p[0], p[1])
		if b.SharedUsage != shared {
			t.Fatalf("原账单 [%d,%d) 被改动: 公摊 = %d", p[0], p[1], b.SharedUsage)
		}
	}
}

// 公摊为负时结算被拒绝且不留痕：无账单、无更正、读数不变，可再次尝试。
func TestNegativeSharedRejectedNoTrace(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 0, Actual)
	mustRecord(t, s, 10, "M", 10, 10, Actual)
	mustRecord(t, s, 10, "U1", 0, 0, Actual)
	mustRecord(t, s, 10, "U1", 10, 50, Actual)
	_, err := s.Settle(10, 0, 10) // 公摊 = 10-50 = -40
	mustCode(t, err, ErrNegativeShared)
	if _, ok := s.Bill(0, 10); ok {
		t.Fatal("拒绝后账期被标记为已结算")
	}
	if len(s.Corrections()) != 0 {
		t.Fatal("拒绝后产生了更正")
	}
	if _, err := s.Settle(10, 0, 10); codeOf(t, err) != ErrNegativeShared {
		t.Fatalf("再次结算应仍报公摊为负, 得到 %v", err)
	}
	// 补足总表读数后更大账期可正常结算，服务不受拒绝影响。
	mustRecord(t, s, 15, "M", 15, 200, Actual)
	b := mustSettle(t, s, 15, 0, 15)
	if b.SharedUsage != 150 {
		t.Fatalf("公摊 = %d, 期望 150", b.SharedUsage)
	}
}

// 固定次序只报告第一个错误。
func TestErrorPrecedence(t *testing.T) {
	setup := func() *Service {
		s := New(Config{Gap: 5})
		mustAdd(t, s, 0, "M", Master, 100, 0)
		mustAdd(t, s, 0, "U1", Unit, 10000, 10)
		mustRecord(t, s, 10, "M", 5, 90, Actual) // 时钟推进到 10
		return s
	}
	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		s := setup()
		_, err := s.Record(5, "", 6, 1, Actual) // 空表号 + now 回退
		mustCode(t, err, ErrInvalidParam)
	})
	t.Run("时钟回退优先于表不存在", func(t *testing.T) {
		s := setup()
		_, err := s.Record(5, "ZZ", 4, 1, Actual)
		mustCode(t, err, ErrClockRollback)
	})
	t.Run("表不存在优先于读数乱序", func(t *testing.T) {
		s := setup()
		_, err := s.Record(10, "ZZ", 0, 1, Actual)
		mustCode(t, err, ErrMeterNotFound)
	})
	t.Run("读数乱序优先于读数非法", func(t *testing.T) {
		s := setup()
		_, err := s.Record(10, "M", 3, 10, Actual) // t 乱序且跌落 80>50 非法
		mustCode(t, err, ErrOutOfOrder)
	})
	t.Run("读数非法优先于估抄条件", func(t *testing.T) {
		s := setup()
		_, err := s.Record(10, "M", 8, 10, Actual) // 跌落 80 > 50
		mustCode(t, err, ErrIllegalReading)
	})
	t.Run("估抄条件不满足", func(t *testing.T) {
		s := setup()
		_, err := s.Record(10, "M", 8, 95, Estimate) // 距实抄 3 <= 5
		mustCode(t, err, ErrEstimateNotAllowed)
	})
	t.Run("已结算优先于公摊为负", func(t *testing.T) {
		s := setup()
		if _, err := s.Settle(10, 0, 10); err != nil { // 用量均为 0，可结算
			t.Fatal(err)
		}
		mustRecord(t, s, 10, "U1", 8, 500, Actual) // 使 [0,10) 重算为负公摊
		_, err := s.Settle(10, 0, 10)
		mustCode(t, err, ErrPeriodSettled)
	})
	t.Run("公摊为负", func(t *testing.T) {
		s := setup()
		mustRecord(t, s, 10, "U1", 6, 0, Actual)
		mustRecord(t, s, 10, "U1", 8, 500, Actual)
		_, err := s.Settle(10, 0, 10)
		mustCode(t, err, ErrNegativeShared)
	})
}

// 估抄准入：距上一次实抄须严格超过 G；估抄值不得小于上一次读数；无实抄基线不可估抄。
func TestEstimateConditions(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 0, "M", 0, 10, Actual)
	_, err := s.Record(5, "M", 5, 20, Estimate) // 5-0=5 未超过 5
	mustCode(t, err, ErrEstimateNotAllowed)
	_, err = s.Record(6, "M", 6, 5, Estimate) // 估抄值小于上一次读数
	mustCode(t, err, ErrEstimateNotAllowed)
	if _, err := s.Record(6, "M", 6, 20, Estimate); err != nil {
		t.Fatalf("合法估抄被拒绝: %v", err)
	}
	_, err = s.Record(7, "U1", 7, 1, Estimate) // U1 无实抄基线
	mustCode(t, err, ErrEstimateNotAllowed)
}

// 乱序与时钟回退：被拒绝后不改变读数与时钟。
func TestOutOfOrderAndClockRollback(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustRecord(t, s, 5, "M", 5, 1, Actual)
	_, err := s.Record(6, "M", 5, 2, Actual) // 同时刻
	mustCode(t, err, ErrOutOfOrder)
	_, err = s.Record(6, "M", 4, 2, Actual) // 更早时刻
	mustCode(t, err, ErrOutOfOrder)
	mustRecord(t, s, 10, "M", 10, 9, Actual)
	_, err = s.Record(9, "M", 8, 10, Actual) // now 回退
	mustCode(t, err, ErrClockRollback)
	// 拒绝不改变读数与时钟，后续操作照常接受。
	mustRecord(t, s, 11, "M", 11, 10, Actual)
	if n := len(s.byID["M"].entries); n != 3 {
		t.Fatalf("读数条目数 = %d, 期望 3", n)
	}
}

// 账期不可重复结算。
func TestDuplicateSettleRejected(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	if _, err := s.Settle(5, 0, 5); err != nil {
		t.Fatal(err)
	}
	_, err := s.Settle(5, 0, 5)
	mustCode(t, err, ErrPeriodSettled)
}

// 守恒：各户分摊之和加各户自用之和恒等于总表用量（含翻转与部分在住）。
func TestConservationWithRolloverAndOccupancy(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	mustAdd(t, s, 0, "U2", Unit, 1000, 20)
	mustAdd(t, s, 0, "U3", Unit, 1000, 30)
	if err := s.AddOccupancy(0, "U3", 0, 10); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, s, 0, "M", 0, 900, Actual)
	mustRecord(t, s, 10, "M", 10, 450, Actual) // 翻转：用量 550
	mustRecord(t, s, 20, "M", 20, 700, Actual) // 用量 250
	mustRecord(t, s, 20, "U1", 0, 0, Actual)
	mustRecord(t, s, 20, "U1", 20, 100, Actual)
	mustRecord(t, s, 20, "U2", 0, 0, Actual)
	mustRecord(t, s, 20, "U2", 20, 200, Actual)
	b := mustSettle(t, s, 20, 0, 20)
	if b.MasterUsage != 800 || b.SharedUsage != 500 {
		t.Fatalf("总表 %d / 公摊 %d, 期望 800 / 500", b.MasterUsage, b.SharedUsage)
	}
	// 权重：U1 空置 200，U2 空置 400，U3 在住 10/20 → 300
	wantShare := map[string]int64{"U1": 111, "U2": 222, "U3": 167}
	var ownSum, shareSum int64
	for _, u := range b.Units {
		if u.Share != wantShare[u.UnitID] {
			t.Fatalf("%s 分摊 = %d, 期望 %d", u.UnitID, u.Share, wantShare[u.UnitID])
		}
		ownSum += u.Own
		shareSum += u.Share
	}
	if shareSum != b.SharedUsage {
		t.Fatalf("分摊之和 %d != 公摊 %d", shareSum, b.SharedUsage)
	}
	if ownSum+shareSum != b.MasterUsage {
		t.Fatalf("不守恒: 自用 %d + 分摊 %d != 总表 %d", ownSum, shareSum, b.MasterUsage)
	}
}

// 相同操作序列重放得到完全相同的账单与更正。
func TestReplayDeterminism(t *testing.T) {
	run := func() (*Service, [][2]int64) {
		s := New(Config{Gap: 5})
		r := rand.New(rand.NewSource(7))
		s.AddMeter(0, "M", Master, 1000, 0)
		for _, id := range []string{"U1", "U2", "U3"} {
			s.AddMeter(0, id, Unit, 1000, int64(10+r.Intn(30)))
		}
		ids := []string{"M", "U1", "U2", "U3"}
		last := map[string]int64{}
		var now int64
		var settled [][2]int64
		for i := 0; i < 200; i++ {
			id := ids[r.Intn(len(ids))]
			switch r.Intn(10) {
			case 0, 1, 2, 3, 4:
				tt := last[id] + 1 + int64(r.Intn(20))
				now = max(now, tt)
				if _, err := s.Record(now, id, tt, int64(r.Intn(1001)), Actual); err == nil {
					last[id] = tt
				}
			case 5, 6:
				tt := last[id] + 1 + int64(r.Intn(20))
				now = max(now, tt)
				if _, err := s.Record(now, id, tt, int64(r.Intn(1001)), Estimate); err == nil {
					last[id] = tt
				}
			case 7:
				tt := last[id] + 1 + int64(r.Intn(20))
				now = max(now, tt)
				if _, err := s.ReplaceMeter(now, id, tt, int64(r.Intn(1001)), int64(r.Intn(1001))); err == nil {
					last[id] = tt
				}
			default:
				st := int64(r.Intn(int(now) + 1))
				en := st + 1 + int64(r.Intn(30))
				if en > now {
					en = now
				}
				if st < en {
					if _, err := s.Settle(now, st, en); err == nil {
						settled = append(settled, [2]int64{st, en})
					}
				}
			}
		}
		return s, settled
	}
	s1, p1 := run()
	s2, p2 := run()
	if !reflect.DeepEqual(p1, p2) {
		t.Fatal("重放账期集合不一致")
	}
	for _, p := range p1 {
		b1, _ := s1.Bill(p[0], p[1])
		b2, _ := s2.Bill(p[0], p[1])
		if !reflect.DeepEqual(b1, b2) {
			t.Fatalf("账期 %v 重放不一致:\n%+v\n%+v", p, b1, b2)
		}
	}
	if !reflect.DeepEqual(s1.Corrections(), s2.Corrections()) {
		t.Fatal("重放更正不一致")
	}
}

// 并发录入同一只表的同一时刻读数：恰有一条成功。
func TestConcurrentSameTimestamp(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1000, 0)
	mustAdd(t, s, 0, "U1", Unit, 1000, 10)
	var ok, rejected atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 10; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.Record(100, "M", 50, 10, Actual); err == nil {
				ok.Add(1)
			} else {
				rejected.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 || rejected.Load() != 9 {
		t.Fatalf("同时刻并发录入: 成功 %d 拒绝 %d, 期望 1/9", ok.Load(), rejected.Load())
	}
}

// 并发烟雾测试（配合 -race）：所有失败都必须是乱序，最终序列严格递增。
func TestConcurrentSmoke(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1<<40, 0)
	mustAdd(t, s, 0, "U1", Unit, 1<<40, 10)
	mustAdd(t, s, 0, "U2", Unit, 1<<40, 20)
	ids := []string{"M", "U1", "U2"}
	var successes atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := ids[(g+i)%len(ids)]
				tt := int64(1000 + g*1000 + i)
				_, err := s.Record(1<<40, id, tt, tt, Actual)
				if err == nil {
					successes.Add(1)
					continue
				}
				var e *Error
				if !errors.As(err, &e) || e.Code != ErrOutOfOrder {
					t.Errorf("并发录入出现非乱序错误: %v", err)
				}
			}
		}(g)
	}
	wg.Wait()
	var total int64
	for _, id := range ids {
		es := s.byID[id].entries
		for i := 1; i < len(es); i++ {
			if es[i].time <= es[i-1].time {
				t.Fatalf("表 %s 序列非严格递增: %v", id, es)
			}
		}
		total += int64(len(es))
	}
	if total != successes.Load() {
		t.Fatalf("序列条目 %d != 成功数 %d", total, successes.Load())
	}
}

// 结算开销与历史读数总量无关：扫描触碰的读数只与账期涉及读数有关。
func TestSettleCostIndependentOfHistory(t *testing.T) {
	s := New(Config{Gap: 5})
	mustAdd(t, s, 0, "M", Master, 1<<40, 0)
	mustAdd(t, s, 0, "U1", Unit, 1<<40, 10)
	for tt := int64(0); tt < 20000; tt++ {
		mustRecord(t, s, tt, "M", tt, tt, Actual)
	}
	st0 := s.Stats()
	mustSettle(t, s, 19999, 100, 110)
	st1 := s.Stats()
	scan1 := st1.ScanEntries - st0.ScanEntries
	for tt := int64(20000); tt < 100000; tt++ {
		mustRecord(t, s, tt, "M", tt, tt, Actual)
	}
	mustSettle(t, s, 99999, 200, 210)
	st2 := s.Stats()
	scan2 := st2.ScanEntries - st1.ScanEntries
	if scan1 != scan2 {
		t.Fatalf("历史 2 万条时扫描 %d 条，10 万条时扫描 %d 条，开销随历史增长", scan1, scan2)
	}
	if scan1 > 2*12 { // 10 个账期内段 + 边界段，每段触碰 2 条
		t.Fatalf("扫描条数 %d 超出账期涉及读数规模", scan1)
	}
	if probes := st2.SearchProbes - st0.SearchProbes; probes > 64 {
		t.Fatalf("二分定位比较次数 %d 异常", probes)
	}
}
