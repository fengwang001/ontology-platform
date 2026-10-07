package billing

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func codeOf(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func wantCode(t *testing.T, err error, c ErrCode) {
	t.Helper()
	if got := codeOf(err); got != c {
		t.Fatalf("want error %v, got %v (%v)", c, got, err)
	}
}

func newBasicBuilding(t *testing.T, cap int64, areas ...int64) *Building {
	t.Helper()
	b := NewBuilding(2)
	must(t, b.AddMeter("M", cap, true, 0))
	for i, a := range areas {
		id := fmt.Sprintf("h%d", i)
		mid := fmt.Sprintf("m%d", i)
		must(t, b.AddMeter(mid, cap, false, 0))
		must(t, b.AddHousehold(id, mid, a, 0))
	}
	return b
}

func read(t *testing.T, b *Building, meter string, time, value, now int64) {
	t.Helper()
	_, err := b.EnterReading(ReadingInput{MeterID: meter, Time: time, Value: value, Now: now})
	must(t, err)
}

// 翻转差额恰为量程一半：视为翻转；超过一个单位即拒绝。
func TestRolloverHalfAndOver(t *testing.T) {
	b := newBasicBuilding(t, 100, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 5, 80, 5)
	read(t, b, "m0", 5, 80, 5)
	read(t, b, "M", 10, 30, 10)
	read(t, b, "m0", 10, 30, 10)
	s, err := b.Settle(0, 10, 11)
	must(t, err)
	if s.Master != 130 || s.Self["h0"] != 130 || s.Shared["h0"] != 0 {
		t.Fatalf("rollover at half: master=%d self=%v shared=%v", s.Master, s.Self, s.Shared)
	}
	read(t, b, "M", 15, 79, 15)
	read(t, b, "m0", 15, 79, 15)
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 20, Value: 28, Now: 20})
	wantCode(t, err, ErrReadingIllegal)
}

// 换表当刻不计用量；新表从起始读数起算。
func TestChangeMeterNoUsageAtInstant(t *testing.T) {
	b := newBasicBuilding(t, 100, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 10, 40, 10)
	read(t, b, "m0", 10, 30, 10)
	must(t, b.ChangeMeter("m0", 10, 30, 5, 100))
	must(t, b.ChangeMeter("M", 10, 40, 0, 100))
	read(t, b, "m0", 20, 25, 20)
	read(t, b, "M", 20, 35, 20)
	s, err := b.Settle(0, 10, 21)
	must(t, err)
	if s.Self["h0"] != 30 {
		t.Fatalf("old segment self = %d, want 30", s.Self["h0"])
	}
	s2, err := b.Settle(10, 20, 22)
	must(t, err)
	if s2.Self["h0"] != 20 || s2.Master != 35 {
		t.Fatalf("new segment self=%d master=%d", s2.Self["h0"], s2.Master)
	}
}

// 读数恰落账期边界。
func TestReadingOnBoundary(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 10, 50, 10)
	read(t, b, "m0", 10, 20, 10)
	read(t, b, "M", 20, 90, 20)
	read(t, b, "m0", 20, 40, 20)
	s1, err := b.Settle(0, 10, 21)
	must(t, err)
	s2, err := b.Settle(10, 20, 22)
	must(t, err)
	if s1.Master != 50 || s2.Master != 40 || s1.Self["h0"] != 20 || s2.Self["h0"] != 20 {
		t.Fatalf("boundary split wrong: %+v %+v", s1, s2)
	}
}

// 线性归属余数去向：读数时刻不与边界重合时，余数进较晚账期；
// 跨度末端落在更晚账期内部时，末端账期取余量并对早账期生成递延更正。
func TestProrationRemainderToLater(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	// 跨度 [0,9) 用量 10：[0,6) 得 floor(60/9)=6，[6,9) 得余量 4。
	read(t, b, "M", 9, 10, 9)
	read(t, b, "m0", 9, 10, 9)
	s1, err := b.Settle(0, 6, 16)
	must(t, err)
	s2, err := b.Settle(6, 9, 17)
	must(t, err)
	// 首结 [0,6) 得 floor(60/9)=6；结算 [6,9) 时末端点 9 恰为其终点，
	// [6,9) 即末端份，余量 4 归它；[0,6) 不再变化，无递延更正。
	corr := b.Corrections()
	if len(corr) != 0 {
		t.Fatalf("remainder-to-later needs no correction on earlier period, got %+v", corr)
	}
	if s1.Master != 6 || s2.Master != 4 {
		t.Fatalf("remainder: %d + %d, want 6 + 4", s1.Master, s2.Master)
	}
}

// 跨度末端落在后账期内部：先结 [0,6) 得 floor，再结 [6,10) 时
// [6,10) 成为末端份拿余量，同时对 [0,6) 生成递延更正（若其 floor 改变）。
func TestDeferredRemainderCorrection(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	// 跨度 [0,8) 用量 10；t=10 补同值锚点使账期 [6,10) 读数充分。
	read(t, b, "M", 8, 10, 9)
	read(t, b, "m0", 8, 10, 9)
	read(t, b, "M", 10, 10, 12)
	read(t, b, "m0", 10, 10, 12)
	s1, err := b.Settle(0, 6, 13)
	must(t, err)
	// [0,6) 得 floor(60/8)=7，余量 3 未归属。
	if s1.Master != 7 {
		t.Fatalf("first provisional = %d, want 7", s1.Master)
	}
	s2, err := b.Settle(6, 10, 14)
	must(t, err)
	// 末端点 8 落在 [6,10) 内部：[6,10) 拿余量 3。
	if s2.Master != 3 {
		t.Fatalf("end period = %d, want 3", s2.Master)
	}
	corr := b.Corrections()
	// [0,6) 的 floor(60/8)=7 在新网格下不变（前面没有其它账期），无更正。
	for _, c := range corr {
		var sum int64
		for _, d := range c.Deltas {
			sum += d
		}
		if sum != 0 {
			t.Fatalf("correction deltas must net 0: %+v", c)
		}
	}
}

// 三个账期、跨度末端落在最后账期内部：余量经递延更正在账期间移动，守恒。
func TestDeferredRemainderAcrossThreePeriods(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	// 跨度 [0,7) 用量 10；t=9 零增量锚点使账期 [6,9) 可结算。
	read(t, b, "M", 7, 10, 9)
	read(t, b, "m0", 7, 10, 9)
	read(t, b, "M", 9, 10, 10)
	read(t, b, "m0", 9, 10, 10)
	s1, err := b.Settle(0, 3, 11)
	must(t, err) // floor(30/7)=4
	s2, err := b.Settle(3, 6, 12)
	must(t, err) // floor(60/7)-4 = 8-4=4
	if s1.Master != 4 || s2.Master != 4 {
		t.Fatalf("provisional %d + %d, want 4 + 4", s1.Master, s2.Master)
	}
	s3, err := b.Settle(6, 9, 13)
	must(t, err) // 末端 7 落在内部：余量 2。
	if s3.Master != 2 {
		t.Fatalf("end period = %d, want 2", s3.Master)
	}
	var total int64 = s1.Master + s2.Master + s3.Master
	for _, c := range b.Corrections() {
		for _, d := range c.Deltas {
			total += d
		}
	}
	if total != 10 {
		t.Fatalf("conservation across deferred chain = %d, want 10", total)
	}
}

// 估抄被更晚实抄替代，且实抄小于估抄；多估部分经更正退回。
func TestEstimateReplacedByLowerActual(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 10, 30, 10)
	read(t, b, "m0", 10, 10, 10)
	read(t, b, "M", 20, 80, 20)
	// 分户估抄 40@18：跨度 [10,18)，[10,15) 得 floor(30*5/8)=18，
	// 余量 12 留待末端落在 18 内部的账期取得。
	_, err := b.EnterReading(ReadingInput{MeterID: "m0", Time: 18, Value: 40, Estimated: true, Now: 21})
	must(t, err)
	s1, err := b.Settle(0, 10, 22)
	must(t, err)
	s2, err := b.Settle(10, 15, 23)
	must(t, err)
	if s1.Self["h0"] != 10 || s2.Self["h0"] != 18 {
		t.Fatalf("estimated split self: %d + %d, want 10 + 18", s1.Self["h0"], s2.Self["h0"])
	}
	// 更晚实抄 t=20 值 20 < 估抄 40：估抄被替代；新跨度 [10,20) 用量 10，
	// [10,15) 得 floor(10*5/10)=5，多估 13 从第二账期退回（18->5）。
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 20, Value: 20, Now: 24})
	must(t, err)
	corr := b.Corrections()
	var target *Correction
	for i := range corr {
		if corr[i].Period == ([2]int64{10, 15}) {
			target = &corr[i]
		}
	}
	if target == nil {
		t.Fatalf("want correction on [10,15), got %+v", corr)
	}
	var sum int64
	for _, d := range target.Deltas {
		sum += d
	}
	if sum != 0 || target.SharedDelta != 13 {
		t.Fatalf("refund correction = %+v, want [10,15) shared +13 (18->5) and payable net 0", target)
	}
}

// 部分在住比例精度与守恒；空置户仍参与公摊。
func TestPartialOccupancyPrecision(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "m1", 0, 0, 0)
	must(t, b.AddOccupancy("h0", 0, 10, 1))
	must(t, b.AddOccupancy("h1", 0, 3, 1))
	read(t, b, "M", 10, 13, 10)
	s, err := b.Settle(0, 10, 11)
	must(t, err)
	if s.Shared["h0"] != 10 || s.Shared["h1"] != 3 {
		t.Fatalf("shared = %v", s.Shared)
	}
	if s.Self["h0"]+s.Self["h1"]+s.Shared["h0"]+s.Shared["h1"] != s.Master {
		t.Fatalf("conservation broken: %+v", s)
	}
	b2 := newBasicBuilding(t, 1000, 100, 100)
	read(t, b2, "M", 0, 0, 0)
	read(t, b2, "m0", 0, 0, 0)
	read(t, b2, "m1", 0, 0, 0)
	read(t, b2, "M", 10, 10, 10)
	s2, err := b2.Settle(0, 10, 11)
	must(t, err)
	if s2.Shared["h0"]+s2.Shared["h1"] != 10 {
		t.Fatalf("vacant households must share: %v", s2.Shared)
	}
}

var _ = sync.Mutex{}
var _ = fmt.Sprintf

// 更正链式触发多个账期。
func TestCorrectionChain(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 10, 10, 10)
	read(t, b, "m0", 10, 10, 10)
	read(t, b, "M", 20, 20, 20)
	read(t, b, "m0", 20, 10, 20)
	_, err := b.Settle(0, 10, 21)
	must(t, err)
	_, err = b.Settle(10, 20, 22)
	must(t, err)
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 5, Value: 8, Now: 23})
	must(t, err)
	corr := b.Corrections()
	if len(corr) != 2 {
		t.Fatalf("want chained corrections for 2 periods, got %d: %+v", len(corr), corr)
	}
	for _, c := range corr {
		var s int64
		for _, d := range c.Deltas {
			s += d
		}
		if s != 0 {
			t.Fatalf("per-period payable deltas must net zero (master unchanged): %+v", c)
		}
	}
	if len(b.Bills()) != 2 {
		t.Fatalf("bills mutated: %v", b.Bills())
	}
}

// 公摊为负：拒绝且不留任何痕迹（账期、时钟均不变）。
func TestNegativeSharedRejectedNoTrace(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	read(t, b, "M", 10, 5, 10)
	read(t, b, "m0", 10, 9, 10)
	_, err := b.Settle(0, 10, 11)
	wantCode(t, err, ErrSharedNegative)
	if len(b.Bills()) != 0 {
		t.Fatalf("rejected settlement left a bill: %v", b.Bills())
	}
	// 时钟未被拒绝推进：把账期延到 11，新增读数使总表用量追平分户，公摊归零后可结算。
	_, errM := b.EnterReading(ReadingInput{MeterID: "M", Time: 11, Value: 9, Now: 11})
	must(t, errM)
	_, errM = b.EnterReading(ReadingInput{MeterID: "m0", Time: 11, Value: 9, Now: 11})
	must(t, errM)
	_, err = b.Settle(0, 11, 12)
	must(t, err)
	if len(b.Corrections()) != 0 {
		t.Fatalf("rejected settle left corrections: %+v", b.Corrections())
	}
}

// 错误固定次序：参数非法 < 时钟回退 < 表不存在 < 读数乱序 < 读数非法
// < 估抄条件 < 已结算 < 公摊为负。构造同时触发多个错误的输入只报告第一个。
func TestErrorOrdering(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "M", 0, 0, 0)
	read(t, b, "m0", 0, 0, 0)
	// 非法参数（时间/值为负）先于时钟判定。
	_, err := b.EnterReading(ReadingInput{MeterID: "m0", Time: -1, Value: 0, Now: -5})
	wantCode(t, err, ErrInvalidArgument)
	// 时钟回退优先于表不存在：当前 now=0，传入 -1（合法非负参数之外，先经时钟检查）。
	// 用先把时钟推进，再传更小 now 的方式触发回退。
	read(t, b, "M", 1, 1, 5)
	_, err = b.EnterReading(ReadingInput{MeterID: "nope", Time: 0, Value: 0, Now: 4})
	wantCode(t, err, ErrClockRollback)
	// 表不存在优先于乱序等后续判定。
	_, err = b.EnterReading(ReadingInput{MeterID: "nope", Time: 0, Value: 0, Now: 6})
	wantCode(t, err, ErrMeterNotFound)
	// 乱序优先于读数非法：同时刻读数（即使值超量程也先报乱序）。
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 0, Value: 9999, Now: 7})
	wantCode(t, err, ErrReadingOutOfOrder)
	// 读数非法：从 0 掉到 0 合法，需制造超半量程下降（cap=1000）。
	read(t, b, "m0", 5, 900, 8)
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 6, Value: 100, Now: 9})
	wantCode(t, err, ErrReadingIllegal)
	// 估抄条件不满足（距上次实抄不足 G）。
	read(t, b, "m0", 7, 950, 10)
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 8, Value: 951, Estimated: true, Now: 11})
	wantCode(t, err, ErrEstimateNotAllowed)
	// 已结算账期重复结算，优先于“非相邻”这类参数问题之外的既定顺序。
	read(t, b, "M", 10, 1000, 12)
	read(t, b, "m0", 10, 1000, 12)
	_, err = b.Settle(0, 10, 13)
	must(t, err)
	_, err = b.Settle(0, 10, 14)
	wantCode(t, err, ErrPeriodAlreadySettled)
}

// 估抄条件：超过 G 才允许；估抄值不得小于上一条读数。
func TestEstimateConditions(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "m0", 0, 10, 0)
	_, err := b.EnterReading(ReadingInput{MeterID: "m0", Time: 2, Value: 10, Estimated: true, Now: 1})
	wantCode(t, err, ErrEstimateNotAllowed) // 恰为 G=2，不算“超过”
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 3, Value: 9, Estimated: true, Now: 2})
	wantCode(t, err, ErrEstimateNotAllowed) // 小于上一条读数
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 3, Value: 12, Estimated: true, Now: 3})
	must(t, err)
	// 同时刻实抄永远按乱序拒绝（即使在估抄存在时）。
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 3, Value: 13, Now: 4})
	wantCode(t, err, ErrReadingOutOfOrder)
}

// 并发：同一表并发读数不可能出现两条同时刻读数，且无崩溃、无数据竞争。
func TestConcurrentNoSameTimestamp(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "m0", 0, 0, 0)
	var wg sync.WaitGroup
	var wg2 sync.WaitGroup
	start := make(chan struct{})
	var winners int32
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		wg2.Add(1)
		go func() {
			defer wg.Done()
			wg2.Done()
			<-start
			_, err := b.EnterReading(ReadingInput{MeterID: "m0", Time: 5, Value: 1, Now: 5})
			if err == nil {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}()
	}
	wg2.Wait()
	close(start)
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly one same-timestamp reading must win, got %d", winners)
	}
}

// 拒绝操作不改时钟。
func TestRejectedDoesNotMoveClock(t *testing.T) {
	b := newBasicBuilding(t, 1000, 100)
	read(t, b, "m0", 0, 900, 0)
	_, err := b.EnterReading(ReadingInput{MeterID: "m0", Time: 5, Value: 0, Now: 10})
	wantCode(t, err, ErrReadingIllegal)
	// 被拒后 now 仍为 0，now=0 合法（不回退）；now<0 才报回退。
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 1, Value: 1, Now: 0})
	if err == nil {
		t.Fatalf("illegal drop must be rejected; got acceptance")
	}
	// 合法读数 now=0 仍可接受（时钟未被拒绝操作推进）。
	_, err = b.EnterReading(ReadingInput{MeterID: "m0", Time: 1, Value: 910, Now: 0})
	must(t, err)
}
