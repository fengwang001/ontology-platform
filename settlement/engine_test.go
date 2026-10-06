package settlement

import (
	"errors"
	"testing"
	"time"
)

// 统一测试环境：1 小时间隔，网格原点 2026-01-01T00:00:00Z。
func newTestEngine() *Engine {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return NewEngine(3600, epoch)
}

func baseParams() Params {
	return Params{ContractPowerW: 5000, MonthlyCreditableW: 1_000_000, CreditValidMonths: 2}
}

func basePrices() Prices {
	return Prices{ImportPricePerWh: 10, SurplusPricePerWh: 4}
}

func setupProsumer(tb testing.TB, e *Engine, id string) {
	tb.Helper()
	if err := e.RegisterProsumer(id); err != nil {
		tb.Fatalf("register: %v", err)
	}
	if err := e.SetParams(id, "2026-01", baseParams()); err != nil {
		tb.Fatalf("params: %v", err)
	}
	if err := e.SetPrices(id, "2026-01", basePrices()); err != nil {
		tb.Fatalf("prices: %v", err)
	}
}

func atMonth(m Month, day, hour int) time.Time {
	return time.Date(m.Year, time.Month(m.Month), day, hour, 0, 0, 0, time.UTC)
}

func at(day, hour int) time.Time { return atMonth(MustMonth("2026-01"), day, hour) }

type dayHour struct{ day, hour int }

// fillMonth 用给定非零 (下网,上网) 列表填满 m 月；其余间隔填 (0,0)。
func fillMonth(tb testing.TB, e *Engine, id string, m Month, values map[dayHour][2]int) {
	tb.Helper()
	first := monthStartUTC(m)
	next := monthStartUTC(m.add(1))
	step := int64(3600)
	for ts := first.Unix(); ts < next.Unix(); ts += step {
		st := time.Unix(ts, 0).UTC()
		v := values[dayHour{st.Day(), st.Hour()}]
		if err := e.RegisterReading(id, Reading{Start: st, ImportWh: v[0], ExportWh: v[1]}); err != nil {
			tb.Fatalf("reading %s: %v", st.Format(time.RFC3339), err)
		}
	}
}

func closeMonth(tb testing.TB, e *Engine, id, month string) *MonthResult {
	tb.Helper()
	r, err := e.CloseMonth(id, month)
	if err != nil {
		tb.Fatalf("close %s: %v", month, err)
	}
	return r
}

// TestPowerExactlyAtCapNotCurtailed：上网功率恰等于上限（5kWh/间隔）不剔除。
func TestPowerExactlyAtCapNotCurtailed(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 12}: {0, 5000}})
	r := closeMonth(t, e, "p", "2026-01")
	if r.CurtailedWh != 0 || r.CreditableWh != 5000 || r.DepositedWh != 5000 {
		t.Fatalf("取等不应剔除, got %+v", r)
	}

	e2 := newTestEngine()
	setupProsumer(t, e2, "p")
	fillMonth(t, e2, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 12}: {0, 5001}})
	r2 := closeMonth(t, e2, "p", "2026-01")
	if r2.CurtailedWh != 1 || r2.CreditableWh != 5000 {
		t.Fatalf("超出1Wh应剔除, got %+v", r2)
	}
}

// TestMonthlyCapExactlyAtLimit：月度可计入恰等于上限；上限外按余电单价付款。
func TestMonthlyCapExactlyAtLimit(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	p := Params{ContractPowerW: 100000, MonthlyCreditableW: 10000, CreditValidMonths: 2}
	if err := e.SetParams("p", "2026-01", p); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-01"),
		map[dayHour][2]int{{1, 10}: {0, 5000}, {1, 11}: {0, 5000}, {1, 12}: {0, 1000}})
	r := closeMonth(t, e, "p", "2026-01")
	if r.CreditableWh != 10000 || r.AboveCapWh != 1000 || r.DepositedWh != 10000 {
		t.Fatalf("取等上限应全部可计入, got %+v", r)
	}
	if r.SurplusPayment != 1000*4 {
		t.Fatalf("上限外1000按余电单价付款, got %d", r.SurplusPayment)
	}
}

// TestSelfOffsetExactlyExhausts：当月抵扣恰好用尽。
func TestSelfOffsetExactlyExhausts(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"),
		map[dayHour][2]int{{1, 10}: {3000, 0}, {1, 11}: {0, 3000}})
	r := closeMonth(t, e, "p", "2026-01")
	if r.SelfOffsetWh != 3000 || r.DepositedWh != 0 || r.BilledWh != 0 ||
		r.HistoryUsedWh != 0 || r.ImportBill != 0 || r.SurplusPayment != 0 {
		t.Fatalf("应当月恰好抵扣完, got %+v", r)
	}
}

func TestBasicValidation(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	if err := e.RegisterReading("p", Reading{Start: at(1, 10).Add(30 * time.Minute)}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("未对齐应报参数非法, got %v", err)
	}
	if err := e.RegisterReading("p", Reading{Start: at(1, 10), ImportWh: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("负电量应报参数非法, got %v", err)
	}
	if err := e.SetPrices("p", "2026-13", basePrices()); !errors.Is(err, ErrInvalid) {
		t.Fatalf("坏月份格式应报参数非法, got %v", err)
	}
	if err := e.SetPrices("p", "2026-01", Prices{ImportPricePerWh: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("负单价应报参数非法, got %v", err)
	}
}

func TestIdempotentAndCorrection(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	rd := Reading{Start: at(1, 10), ImportWh: 100, ExportWh: 200}
	if err := e.RegisterReading("p", rd); err != nil {
		t.Fatal(err)
	}
	if err := e.RegisterReading("p", rd); err != nil {
		t.Fatalf("完全重复应幂等成功, got %v", err)
	}
	if err := e.RegisterReading("p", Reading{Start: at(1, 10), ImportWh: 101, ExportWh: 200}); err != nil {
		t.Fatalf("未封账应允许修正, got %v", err)
	}
	r, err := e.Query("p", "2026-01")
	if err != nil {
		t.Fatal(err)
	}
	if r.ImportWh != 101 {
		t.Fatalf("修正应反映在试算, got %d", r.ImportWh)
	}
}

func TestRejectionOrder(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"), nil)
	closeMonth(t, e, "p", "2026-01")

	// 1) 非法优先于已封账。
	if err := e.RegisterReading("p", Reading{Start: at(1, 10), ImportWh: -1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("非法应先于封账, got %v", err)
	}
	// 2) 已封账：同间隔不同读数。
	if err := e.RegisterReading("p", Reading{Start: at(1, 10), ImportWh: 2}); !errors.Is(err, ErrClosed) {
		t.Fatalf("应报月份已封账, got %v", err)
	}
	// 3) 顺序错误优先于数据缺失：3 月未满足前提且缺数据。
	_, err := e.CloseMonth("p", "2026-03")
	if !errors.Is(err, ErrOrder) {
		t.Fatalf("应报顺序错误, got %v", err)
	}
	// 4) 数据缺失：2 月一个间隔都没填。
	_, err = e.CloseMonth("p", "2026-02")
	var se *SettlementError
	if !errors.As(err, &se) || se.Kind != KindMissing {
		t.Fatalf("应报数据缺失, got %v", err)
	}
}

func TestMissingFirstInterval(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	for _, h := range []int{2, 3} {
		if err := e.RegisterReading("p", Reading{Start: at(1, h)}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := e.CloseMonth("p", "2026-01")
	var se *SettlementError
	if !errors.As(err, &se) || se.Kind != KindMissing {
		t.Fatalf("应报数据缺失, got %v", err)
	}
	if !se.Missing.Equal(at(1, 0)) {
		t.Fatalf("最早缺失应为 2026-01-01T00:00Z, got %s", se.Missing)
	}
}

func TestClosedImmutable(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 10}: {100, 0}})
	r1 := closeMonth(t, e, "p", "2026-01")
	if err := e.SetParams("p", "2026-01", baseParams()); !errors.Is(err, ErrClosed) {
		t.Fatalf("改封账月参数应拒绝, got %v", err)
	}
	if err := e.SetPrices("p", "2026-01", basePrices()); !errors.Is(err, ErrClosed) {
		t.Fatalf("改封账月单价应拒绝, got %v", err)
	}
	if err := e.RegisterReading("p", Reading{Start: at(1, 10), ImportWh: 100}); err != nil {
		t.Fatalf("完全相同的迟到读数应幂等成功, got %v", err)
	}
	r2, ok := e.ClosedResult("p", "2026-01")
	if !ok || *r1 != *r2 {
		t.Fatal("封账结果应不可变")
	}
}

func assertInvariants(t *testing.T, e *Engine, id, firstMonth string) {
	t.Helper()
	idx0 := MustMonth(firstMonth).index()
	totalCred, totalSelf, totalHist, totalExp := 0, 0, 0, 0
	for idx := idx0; e.prosumers[id].closed[idx]; idx++ {
		r := e.prosumers[id].results[idx]
		totalCred += r.CreditableWh
		totalSelf += r.SelfOffsetWh
		totalHist += r.HistoryUsedWh
		totalExp += r.ExpiredPaidWh
		if r.ImportWh != r.SelfOffsetWh+r.HistoryUsedWh+r.BilledWh {
			t.Fatalf("月下网恒等式破坏 %s: %d != %d+%d+%d",
				monthFromIndex(idx), r.ImportWh, r.SelfOffsetWh, r.HistoryUsedWh, r.BilledWh)
		}
	}
	active, _ := e.ActiveCreditWh(id)
	if totalCred != totalSelf+totalHist+totalExp+active {
		t.Fatalf("可计入恒等式破坏: %d != %d+%d+%d+%d",
			totalCred, totalSelf, totalHist, totalExp, active)
	}
}

// TestCreditOrderAndExpiry 覆盖：
//   - 额度不得在存入当月使用；
//   - 到期月恰等于封账月：先抵扣、抵扣后剩余到期付款清零；
//   - 试算不改变持久额度余额；
//   - 封账结果重放不变。
func TestCreditOrderAndExpiry(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	// 有效期 1：1 月存入的额度 2 月到期。
	if err := e.SetParams("p", "2026-01", Params{5000, 1_000_000, 1}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-01"),
		map[dayHour][2]int{{1, 1}: {0, 100}})
	jan := closeMonth(t, e, "p", "2026-01")
	if jan.DepositedWh != 100 || jan.HistoryUsedWh != 0 || jan.ExpiredPaidWh != 0 {
		t.Fatalf("1月应净存100、当月不可自用、无到期, got %+v", jan)
	}
	// 2 月（到期月）下网 70：该额度先抵扣 70，剩余 30 在本月抵扣之后到期付款。
	fillMonth(t, e, "p", MustMonth("2026-02"),
		map[dayHour][2]int{{1, 2}: {70, 0}})
	feb := closeMonth(t, e, "p", "2026-02")
	if feb.HistoryUsedWh != 70 || feb.BilledWh != 0 || feb.ExpiredPaidWh != 30 ||
		feb.SurplusPayment != 30*4 {
		t.Fatalf("2月应先用70、余30到期付款, got %+v", feb)
	}
	assertInvariants(t, e, "p", "2026-01")
	active, _ := e.ActiveCreditWh("p")
	if active != 0 {
		t.Fatalf("到期清零后活动额度应为0, got %d", active)
	}
	// 3 月没有任何历史额度可用，下网全额计费。
	fillMonth(t, e, "p", MustMonth("2026-03"),
		map[dayHour][2]int{{1, 3}: {10, 0}})
	mar := closeMonth(t, e, "p", "2026-03")
	if mar.HistoryUsedWh != 0 || mar.BilledWh != 10 || mar.ImportBill != 10*10 {
		t.Fatalf("3月应无历史额度、全额计费, got %+v", mar)
	}

	// 重放确定性：同一输入序列在第二个引擎得到完全相同的月结果。
	e2 := replayEngine(t)
	for _, mo := range []string{"2026-01", "2026-02", "2026-03"} {
		a, _ := e.ClosedResult("p", mo)
		b, _ := e2.ClosedResult("p", mo)
		if *a != *b {
			t.Fatalf("重放结果不一致 %s: %+v vs %+v", mo, *a, *b)
		}
	}
}

// replayEngine 按相同输入序列重放，用于确定性对照。
func replayEngine(t *testing.T) *Engine {
	t.Helper()
	e := newTestEngine()
	setupProsumer(t, e, "p")
	if err := e.SetParams("p", "2026-01", Params{5000, 1_000_000, 1}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 100}})
	closeMonth(t, e, "p", "2026-01")
	fillMonth(t, e, "p", MustMonth("2026-02"), map[dayHour][2]int{{1, 2}: {70, 0}})
	closeMonth(t, e, "p", "2026-02")
	fillMonth(t, e, "p", MustMonth("2026-03"), map[dayHour][2]int{{1, 3}: {10, 0}})
	closeMonth(t, e, "p", "2026-03")
	return e
}

// TestSameExpiryDifferentDepositOrder：同到期月按存入月先后使用。
func TestSameExpiryDifferentDepositOrder(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	// 让 2 月存入的额度也在 4 月到期：1 月时有效月数 3（到期4月），
	// 2 月改成有效月数 2（到期4月）。参数更改作用于未封账月份。
	if err := e.SetParams("p", "2026-01", Params{5000, 1_000_000, 3}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 40}})
	closeMonth(t, e, "p", "2026-01")
	if err := e.SetParams("p", "2026-02", Params{5000, 1_000_000, 2}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-02"), map[dayHour][2]int{{1, 1}: {0, 50}})
	closeMonth(t, e, "p", "2026-02")
	fillMonth(t, e, "p", MustMonth("2026-03"), nil)
	mar := closeMonth(t, e, "p", "2026-03")
	if mar.ExpiredPaidWh != 0 {
		t.Fatalf("3月无额度到期, got %+v", mar)
	}
	// 4 月下网 60：两笔同于 4 月到期，先用存入月更早的 1 月笔 40，再用 2 月笔 20，
	// 2 月笔余 30 在本月抵扣后到期付款。
	fillMonth(t, e, "p", MustMonth("2026-04"), map[dayHour][2]int{{1, 1}: {60, 0}})
	apr := closeMonth(t, e, "p", "2026-04")
	if apr.HistoryUsedWh != 60 || apr.ExpiredPaidWh != 30 {
		t.Fatalf("应先旧后新抵60、余30到期付款, got %+v", apr)
	}
}

// TestTentativeQueryReflectsChangesWithoutMutation：
// 未封账月改参数会改变试算结果，但持久额度余额不变。
func TestTentativeQueryReflectsChangesWithoutMutation(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 50}})
	closeMonth(t, e, "p", "2026-01")

	fillMonth(t, e, "p", MustMonth("2026-02"), map[dayHour][2]int{{1, 1}: {100, 0}})
	q1, err := e.Query("p", "2026-02")
	if err != nil {
		t.Fatal(err)
	}
	if q1.HistoryUsedWh != 50 || q1.BilledWh != 50 {
		t.Fatalf("试算: 历史抵50计费50, got %+v", q1)
	}
	// 试算不得动持久额度：2 月仍未封账，活动额度仍为 50。
	active, _ := e.ActiveCreditWh("p")
	if active != 50 {
		t.Fatalf("试算后持久额度不应变化, got %d", active)
	}
	// 提高 2 月下网电价后试算金额变化，额度余额依旧不变。
	if err := e.SetPrices("p", "2026-02", Prices{ImportPricePerWh: 20, SurplusPricePerWh: 4}); err != nil {
		t.Fatal(err)
	}
	q2, err := e.Query("p", "2026-02")
	if err != nil {
		t.Fatal(err)
	}
	if q2.ImportBill != 50*20 || q2.ImportBill == q1.ImportBill {
		t.Fatalf("改单价应改变试算金额, got %+v vs %+v", q1, q2)
	}
	active2, _ := e.ActiveCreditWh("p")
	if active2 != 50 {
		t.Fatalf("再次试算后持久额度仍不应变化, got %d", active2)
	}
}

// TestNoCreditUsedInDepositMonth：额度在存入当月不可被「同月晚些的下网」使用。
// 规则按月度聚合：可计入上网先逐度抵扣当月下网，不存在月内顺序问题，
// 本用例验证净余当月不会又回过头产生任何历史抵扣/付款组合异常。
func TestNoCreditUsedInDepositMonth(t *testing.T) {
	e := newTestEngine()
	setupProsumer(t, e, "p")
	fillMonth(t, e, "p", MustMonth("2026-01"),
		map[dayHour][2]int{{1, 1}: {40, 100}})
	r := closeMonth(t, e, "p", "2026-01")
	if r.SelfOffsetWh != 40 || r.DepositedWh != 60 || r.HistoryUsedWh != 0 || r.ExpiredPaidWh != 0 {
		t.Fatalf("当月只做自抵与净存, got %+v", r)
	}
}

// TestHeapContainsOnlyLiveLots：已用尽与已到期的笔立即出堆，
// 历史抵扣路径永远不会扫描它们（性能不变量的可验证证据）。
func TestHeapContainsOnlyLiveLots(t *testing.T) {
	// 场景一：到期月部分使用，剩余 60 在本月抵扣之后到期付款，笔立即出堆。
	e := newTestEngine()
	setupProsumer(t, e, "p")
	if err := e.SetParams("p", "2026-01", Params{5000, 1_000_000, 1}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e, "p", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 100}})
	closeMonth(t, e, "p", "2026-01")
	fillMonth(t, e, "p", MustMonth("2026-02"), map[dayHour][2]int{{1, 1}: {40, 0}})
	feb := closeMonth(t, e, "p", "2026-02")
	if feb.HistoryUsedWh != 40 || feb.ExpiredPaidWh != 60 {
		t.Fatalf("2月应先用40后到期付款60, got %+v", feb)
	}
	if got := e.prosumers["p"].heap.Len(); got != 0 {
		t.Fatalf("到期笔付款后应立即出堆, got %d", got)
	}

	// 场景二：未到期且未用尽的笔留在堆中；到期月恰好用尽时出堆。
	e2 := newTestEngine()
	setupProsumer(t, e2, "r")
	if err := e2.SetParams("r", "2026-01", Params{5000, 1_000_000, 2}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e2, "r", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 100}})
	closeMonth(t, e2, "r", "2026-01")
	fillMonth(t, e2, "r", MustMonth("2026-02"), map[dayHour][2]int{{1, 1}: {40, 0}})
	closeMonth(t, e2, "r", "2026-02")
	if got := e2.prosumers["r"].heap.Len(); got != 1 || e2.prosumers["r"].heap[0].RemainingWh != 60 {
		t.Fatalf("2月后应仍有1笔余60, got %+v", e2.prosumers["r"].heap)
	}
	fillMonth(t, e2, "r", MustMonth("2026-03"), map[dayHour][2]int{{1, 1}: {60, 0}})
	mar := closeMonth(t, e2, "r", "2026-03")
	if mar.HistoryUsedWh != 60 || mar.ExpiredPaidWh != 0 {
		t.Fatalf("3月到期笔应被用尽、无到期付款, got %+v", mar)
	}
	if got := e2.prosumers["r"].heap.Len(); got != 0 {
		t.Fatalf("用尽笔应立即出堆, got %d", got)
	}

	// 场景三：到期未用，封账到期月后付款清零且出堆。
	e3 := newTestEngine()
	setupProsumer(t, e3, "q")
	if err := e3.SetParams("q", "2026-01", Params{5000, 1_000_000, 1}); err != nil {
		t.Fatal(err)
	}
	fillMonth(t, e3, "q", MustMonth("2026-01"), map[dayHour][2]int{{1, 1}: {0, 50}})
	closeMonth(t, e3, "q", "2026-01")
	fillMonth(t, e3, "q", MustMonth("2026-02"), nil)
	closeMonth(t, e3, "q", "2026-02")
	if got := e3.prosumers["q"].heap.Len(); got != 0 {
		t.Fatalf("到期付款后不应留堆, got %d", got)
	}
}
