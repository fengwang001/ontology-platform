package ontology

import (
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"
)

const hour = time.Hour

func testEngine(t *testing.T) (*Engine, *TimeGrid) {
	t.Helper()
	e, err := NewEngine(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return e, e.g
}

func baseContract() ContractParams {
	return ContractParams{ContractExportWatts: 5000, MonthlyCreditableKWh: 100, CreditValidMonths: 2}
}

func basePrices() Prices { return Prices{ImportPrice: 10, ExportPrice: 3} }

// startOfMonth 返回 YYYY-MM 月份的 UTC 月初。
func startOfMonth(m MonthKey) time.Time {
	y := int(int64(m) / 12)
	mo := time.Month(int64(m)%12 + 1)
	return time.Date(y, mo, 1, 0, 0, 0, 0, time.UTC)
}

// fillMonth 用 gen 生成该月全部间隔读数并登记。
func fillMonth(t *testing.T, e *Engine, id string, m MonthKey, gen func(idx int, start time.Time) (imp, exp int64)) {
	t.Helper()
	for i, st := range e.g.IntervalStarts(m) {
		imp, exp := gen(i, st)
		if err := e.PutReading(id, st, Readings{ImportKWh: imp, ExportKWh: exp}); err != nil {
			t.Fatalf("put %s: %v", st, err)
		}
	}
}

func month(t *testing.T, s string) MonthKey {
	t.Helper()
	m, err := ParseMonth(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func regConsumer(t *testing.T, e *Engine, id string, p ContractParams, pr Prices) {
	t.Helper()
	if err := e.RegisterConsumer(id, p, pr); err != nil {
		t.Fatal(err)
	}
}

func mustSeal(t *testing.T, e *Engine, id string, m MonthKey) {
	t.Helper()
	if err := e.SealMonth(id, m); err != nil {
		t.Fatalf("seal %s: %v", m, err)
	}
}

func errKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var se *SettError
	if !errors.As(err, &se) {
		t.Fatalf("not SettError: %v", err)
	}
	return se.Kind
}

// 场景1：单间隔上网功率恰等于上限不剔除。
func TestExportExactlyAtLimitNotRejected(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	// 全部 744 个间隔上网 5 度（=5kW*1h，取等），下网 0。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i == 10 {
			return 0, 5 // 恰好等于上限
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	r, _ := e.MonthResult("c", m0)
	if r.RejectedExport != 0 || r.RawExport != 5 {
		t.Fatalf("rejected=%d raw=%d, want 0/5", r.RejectedExport, r.RawExport)
	}
}

// 场景2：超出功率上限的间隔剔除超出部分，且剔除量不付款不抵扣。
func TestExportOverLimitRejected(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 0, 8 // 上限 5：剔除 3
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	r, _ := e.MonthResult("c", m0)
	if r.RejectedExport != 3 || r.CreditableExport != 5 || r.NetExportCredited != 5 {
		t.Fatalf("got %+v", r)
	}
	if r.ExportPayment != 0 {
		t.Fatalf("rejected part must not be paid, payment=%d", r.ExportPayment)
	}
}

// 场景3：月度可计入恰等于上限；以上部分按余电单价付款且不进抵扣。
func TestMonthlyCapExactly(t *testing.T) {
	e, _ := testEngine(t)
	p := baseContract()
	p.MonthlyCreditableKWh = 100
	regConsumer(t, e, "c", p, basePrices())
	m0 := month(t, "2025-01")
	// 前 21 个间隔各 5 度 = 105：可计入 100，5 度付款。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i < 21 {
			return 0, 5
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	r, _ := e.MonthResult("c", m0)
	if r.CreditableExport != 100 || r.AboveCapPaidKWh != 5 {
		t.Fatalf("creditable=%d above=%d", r.CreditableExport, r.AboveCapPaidKWh)
	}
	if r.ExportPayment != 5*3 || r.NetExportCredited != 100 {
		t.Fatalf("payment=%d credited=%d", r.ExportPayment, r.NetExportCredited)
	}
}

// 场景4：当月可计入上网恰好逐度抵扣完当月下网。
func TestSelfOffsetExhausted(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	// 96 下网 / 96 上网（4 度 * 24 个间隔），其余为 0。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i < 24 {
			return 4, 4
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	r, _ := e.MonthResult("c", m0)
	if r.SelfOffset != 96 || r.NetExportCredited != 0 || r.PriorCreditUsed != 0 ||
		r.BilledImportKWh != 0 || r.ImportBill != 0 {
		t.Fatalf("got %+v", r)
	}
}

// 场景5：到期月恰等于封账月的额度，先在该月抵扣，余额再到期付款。
func TestCreditUsedInExpiryMonthThenExpires(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices()) // 有效月数 2
	m0 := month(t, "2025-01")
	m1 := month(t, "2025-02")
	m2 := month(t, "2025-03")
	// 1 月：净上网 10 存入，到期月 = 3 月。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i < 2 {
			return 0, 5
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	// 2 月无数据月份也需填满 0。
	fillMonth(t, e, "c", m1, func(int, time.Time) (int64, int64) { return 0, 0 })
	mustSeal(t, e, "c", m1)
	// 3 月：净下网 4。到期额度 10 先用 4，余 6 到期付款。
	fillMonth(t, e, "c", m2, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 4, 0
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m2)
	r, _ := e.MonthResult("c", m2)
	if r.PriorCreditUsed != 4 || r.ExpiredCreditKWh != 6 || r.ExportPayment != 6*3 ||
		r.BilledImportKWh != 0 {
		t.Fatalf("got %+v", r)
	}
	if bal, _ := e.CreditBalance("c"); bal != 0 {
		t.Fatalf("balance=%d want 0", bal)
	}
}

// 场景6：同到期月不同存入月，按存入月从早到晚使用。
func TestSameExpiryFIFOByDeposit(t *testing.T) {
	e, _ := testEngine(t)
	p := baseContract()
	p.CreditValidMonths = 3
	regConsumer(t, e, "c", p, basePrices())
	m0 := month(t, "2025-01")
	m1 := month(t, "2025-02")
	m2 := month(t, "2025-03")
	m3 := month(t, "2025-04")
	// 1 月存入 5（到期4月）；2 月存入 5（到期5月）。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 0, 5
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	fillMonth(t, e, "c", m1, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 0, 5
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m1)
	// 3 月：使 1 月额度有效期在 4 月、2 月额度在 5 月；3 月无净差。
	fillMonth(t, e, "c", m2, func(int, time.Time) (int64, int64) { return 0, 0 })
	mustSeal(t, e, "c", m2)
	// 4 月：先让 1 月额度到期前用掉（净下网 7）：用到期4月的5，再用到期5月的2。
	fillMonth(t, e, "c", m3, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 7, 0
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m3)
	r, _ := e.MonthResult("c", m3)
	if r.PriorCreditUsed != 7 {
		t.Fatalf("prior used=%d want 7", r.PriorCreditUsed)
	}
	if r.ExpiredCreditKWh != 0 {
		t.Fatalf("nothing should expire in April (earliest deposit exhausted): %d", r.ExpiredCreditKWh)
	}
	if bal, _ := e.CreditBalance("c"); bal != 3 {
		t.Fatalf("balance=%d want 3", bal)
	}
}

// 场景7：额度不得在存入当月使用（即使当月先上网后下网也只允许当月互抵）。
func TestCreditNotUsableInDepositMonth(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	// 前半个间隔上网 5 产生净上网，另一间隔下网 3：二者只在当月互抵，
	// 不涉及“历史额度”。当月汇总：上网5、下网3 -> 自抵3，存额度2。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i == 0 {
			return 0, 5
		}
		if i == 1 {
			return 3, 0
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	r, _ := e.MonthResult("c", m0)
	if r.SelfOffset != 3 || r.NetExportCredited != 2 || r.PriorCreditUsed != 0 ||
		r.BilledImportKWh != 0 {
		t.Fatalf("got %+v", r)
	}
}

// 场景8：缺失间隔报「数据缺失」且指出最早缺失间隔；补齐后可封账。
func TestSealMissingIntervalsReportsEarliest(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	starts := e.g.IntervalStarts(m0)
	// 缺失第 0、2 个间隔。
	if err := e.PutReading("c", starts[1], Readings{ImportKWh: 1}); err != nil {
		t.Fatal(err)
	}
	err := e.SealMonth("c", m0)
	if errKind(t, err) != KindDataMissing {
		t.Fatalf("kind=%v want DataMissing", err)
	}
	var se *SettError
	errors.As(err, &se)
	if !se.EarliestMissing.Equal(starts[0]) {
		t.Fatalf("earliest=%v want %v", se.EarliestMissing, starts[0])
	}
	// 拒绝操作不改状态：补齐全部间隔后封账成功。
	for _, st := range starts {
		if err := e.PutReading("c", st, Readings{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.SealMonth("c", m0); err != nil {
		t.Fatalf("seal after fill: %v", err)
	}
}

// 场景9：封账必须按月份顺序（首月之后不得跳月）。
func TestSealOutOfOrderRejected(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	m2 := month(t, "2025-03")
	fillMonth(t, e, "c", m0, func(int, time.Time) (int64, int64) { return 0, 0 })
	mustSeal(t, e, "c", m0)
	if err := e.SealMonth("c", m2); err == nil || errKind(t, err) != KindOrderError {
		t.Fatalf("err=%v want OrderError", err)
	}
}

// 场景10：未封账月参数更改使预览结果变化，但不改变历史封账结果与额度余额。
func TestUnsealedParamChangeChangesPreviewOnly(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	m1 := month(t, "2025-02")
	// 1 月净上网 10 存入额度。
	fillMonth(t, e, "c", m0, func(i int, _ time.Time) (int64, int64) {
		if i < 2 {
			return 0, 5
		}
		return 0, 0
	})
	mustSeal(t, e, "c", m0)
	// 2 月有一个间隔上网 8（旧上限 5 剔除 3）。
	over := startOfMonth(m1)
	if err := e.PutReading("c", over, Readings{ExportKWh: 8}); err != nil {
		t.Fatal(err)
	}
	before, _ := e.MonthResult("c", m1)
	balBefore, _ := e.CreditBalance("c")
	if before.RejectedExport != 3 || before.CreditableExport != 5 {
		t.Fatalf("preview before=%+v", before)
	}
	// 2 月未封账，允许更改参数：上限提到 8000W，不再剔除。
	newP := baseContract()
	newP.ContractExportWatts = 8000
	if err := e.SetParams("c", m1, newP); err != nil {
		t.Fatal(err)
	}
	after, _ := e.MonthResult("c", m1)
	balAfter, _ := e.CreditBalance("c")
	if after.RejectedExport != 0 || after.CreditableExport != 8 {
		t.Fatalf("preview after=%+v", after)
	}
	if balBefore != 10 || balAfter != 10 {
		t.Fatalf("balance changed: %d -> %d", balBefore, balAfter)
	}
	// 预览不存入额度：2 月的净上网没有进入真实余额。
	// 封账月结果不可变。
	r0, _ := e.MonthResult("c", m0)
	if r0.NetExportCredited != 10 {
		t.Fatalf("sealed month changed: %+v", r0)
	}
}

// 场景11：拒绝次序——参数非法先于封账等一切。
func TestRejectionOrdering(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	// 对已封账月做「未对齐 + 已封账」操作：必须报参数非法。
	fillMonth(t, e, "c", m0, func(int, time.Time) (int64, int64) { return 0, 0 })
	mustSeal(t, e, "c", m0)
	bad := startOfMonth(m0).Add(30 * time.Minute)
	err := e.PutReading("c", bad, Readings{ImportKWh: -1})
	if errKind(t, err) != KindIllegalParameter {
		t.Fatalf("aligned+negative on sealed month: kind=%v want Illegal", err)
	}
	// 对齐但负数：参数非法优先于已封账。
	aligned := startOfMonth(m0)
	if err := e.PutReading("c", aligned, Readings{ImportKWh: -1}); err == nil ||
		errKind(t, err) != KindIllegalParameter {
		t.Fatalf("negative on sealed: %v", err)
	}
	// 对齐、非负、已封账：月份已封账。
	if err := e.PutReading("c", aligned, Readings{ImportKWh: 1}); err == nil ||
		errKind(t, err) != KindMonthSealed {
		t.Fatalf("sealed: %v", err)
	}
	// 封账：顺序错误先于数据缺失。
	m2 := month(t, "2025-03")
	if err := e.SealMonth("c", m2); err == nil || errKind(t, err) != KindOrderError {
		t.Fatalf("seal skip: %v", err)
	}
	// 单价为负：参数非法先于已封账。
	if err := e.SetPrices("c", m0, Prices{ImportPrice: -1}); err == nil ||
		errKind(t, err) != KindIllegalParameter {
		t.Fatalf("negative price sealed month: %v", err)
	}
	// 月份格式错误。
	if _, err := ParseMonth("2025/01"); err == nil || errKind(t, err) != KindIllegalParameter {
		t.Fatalf("bad month parse: %v", err)
	}
}

// 场景12：幂等登记与封账后修正被拒。
func TestIdempotentAndSealedCorrection(t *testing.T) {
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	st := startOfMonth(m0)
	if err := e.PutReading("c", st, Readings{ImportKWh: 2, ExportKWh: 3}); err != nil {
		t.Fatal(err)
	}
	if err := e.PutReading("c", st, Readings{ImportKWh: 2, ExportKWh: 3}); err != nil {
		t.Fatalf("identical rewrite should be idempotent: %v", err)
	}
	for _, s := range e.g.IntervalStarts(m0) {
		if err := e.PutReading("c", s, Readings{}); err != nil {
			t.Fatal(err)
		}
	}
	mustSeal(t, e, "c", m0)
	// 封账后同值重放仍报「月份已封账」（按题意封账后数据不可变）。
	if err := e.PutReading("c", st, Readings{}); err == nil || errKind(t, err) != KindMonthSealed {
		t.Fatalf("post-seal write: %v", err)
	}
}
func TestCreditStoreOrderingExpiryThenFIFO(t *testing.T) {
	s := &creditStore{}
	// 同到期月不同存入顺序；不同到期月。键序：到期月升序，同到期月存入序号升序。
	s.deposit(1, 5, 10)
	s.deposit(2, 3, 7)
	s.deposit(3, 5, 4)
	s.deposit(4, 3, 1)
	if got := s.balance(); got != 22 {
		t.Fatalf("balance=%d want 22", got)
	}
	var used []int64
	drain := func(want int64) {
		got := s.use(1, want)
		used = append(used, got)
	}
	drain(5) // 到期3: 7 用 5 -> 剩2
	drain(5) // 到期3: 2 用尽；到期5第一笔10用3
	drain(20)
	// 期望消费序列：5, 5(到期3余2 + 到期5首笔3), 12(到期5首笔7 + 次笔4+剩余1)
	wantUsed := []int64{5, 5, 12}
	for i := range wantUsed {
		if used[i] != wantUsed[i] {
			t.Fatalf("use[%d]=%d want %d; seq=%v", i, used[i], wantUsed[i], used)
		}
	}
	if got := s.balance(); got != 0 {
		t.Fatalf("balance after drain=%d want 0", got)
	}
	if got := s.expire(5); got != 0 {
		t.Fatalf("expire=%d want 0", got)
	}
	if got := s.balance(); got != 0 {
		t.Fatalf("balance after expire=%d want 0", got)
	}
	if got := s.expire(99); got != 0 {
		t.Fatalf("expire on empty=%d want 0", got)
	}
}

func TestCreditStoreExpiryBoundaryUseThenExpire(t *testing.T) {
	s := &creditStore{}
	s.deposit(1, 3, 6)
	// minExpiry=3：到期月恰等于 3 的额度本月可用。
	if got := s.use(3, 4); got != 4 {
		t.Fatalf("use in expiry month=%d want 4", got)
	}
	if got := s.expire(3); got != 2 {
		t.Fatalf("expire same month=%d want 2", got)
	}
}

func TestCreditStoreSnapshotIsolation(t *testing.T) {
	s := &creditStore{}
	s.deposit(1, 4, 5)
	clone := s.snapshot()
	if got := clone.use(1, 9); got != 5 {
		t.Fatalf("clone use=%d want 5", got)
	}
	if got := s.balance(); got != 5 {
		t.Fatalf("original changed by clone use: %d want 5", got)
	}
}

// TestConcurrentSafety 在 -race 下对同一产消者并发执行各类操作：
// 只需保证无数据竞争、不崩溃、错误类别合法，以及最终状态可串行解释。
func TestConcurrentSafety(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	m0 := month(t, "2025-01")
	m1 := month(t, "2025-02")
	starts := e.g.IntervalStarts(m0)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for i := 0; i < 300; i++ {
				switch rng.Intn(6) {
				case 0:
					st := starts[rng.Intn(len(starts))]
					_ = e.PutReading("c", st, Readings{ImportKWh: int64(rng.Intn(4)), ExportKWh: int64(rng.Intn(7))})
				case 1:
					_ = e.PutReading("c", starts[0].Add(30*time.Minute), Readings{ImportKWh: -1})
				case 2:
					_ = e.SealMonth("c", m0)
				case 3:
					_ = e.SealMonth("c", m1)
				case 4:
					_, _ = e.MonthResult("c", m0)
					_, _ = e.MonthResult("c", m1)
				case 5:
					_, _ = e.CreditBalance("c")
				}
			}
		}(w + 1)
	}
	wg.Wait()
	// 无论交错如何，引擎内部不变量必须成立。
	if _, err := e.MonthResult("c", m1); err != nil {
		t.Fatal(err)
	}
}

// TestReplayDeterminism：相同操作序列重放得到完全相同的月结果。
func TestReplayDeterminism(t *testing.T) {
	build := func() []MonthlyResult {
		e, _ := testEngine(t)
		regConsumer(t, e, "c", baseContract(), basePrices())
		var out []MonthlyResult
		for k, ms := range []string{"2025-01", "2025-02", "2025-03"} {
			m := month(t, ms)
			for i, st := range e.g.IntervalStarts(m) {
				imp := int64((i + k) % 5)
				exp := int64((i*2 + k) % 7)
				if err := e.PutReading("c", st, Readings{ImportKWh: imp, ExportKWh: exp}); err != nil {
					t.Fatal(err)
				}
			}
			if err := e.SealMonth("c", m); err != nil {
				t.Fatal(err)
			}
			r, _ := e.MonthResult("c", m)
			out = append(out, r)
		}
		return out
	}
	a := build()
	b := build()
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("month %d differs on replay:\n%+v\n%+v", i, a[i], b[i])
		}
	}
}

// TestInvariantsOnRandomHistory 在一段随机历史上校验题面两条全局不变量。
func TestInvariantsOnRandomHistory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e, _ := testEngine(t)
	regConsumer(t, e, "c", baseContract(), basePrices())
	rng := newLockedRNG(7)
	monthStrs := []string{"2025-01", "2025-02", "2025-03", "2025-04", "2025-05"}
	var results []MonthlyResult
	for _, ms := range monthStrs {
		m := month(t, ms)
		for _, st := range e.g.IntervalStarts(m) {
			if err := e.PutReading("c", st, Readings{
				ImportKWh: int64(rng.Intn(12)),
				ExportKWh: int64(rng.Intn(12)),
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.SealMonth("c", m); err != nil {
			t.Fatal(err)
		}
		r, _ := e.MonthResult("c", m)
		results = append(results, r)
	}
	// 不变量 1：自始至今 可计入上网总量 = 已抵扣总量(自抵+历史额度) + 未到期余额 + 已到期付款电量。
	var creditable, offsetUsed, expired int64
	for _, r := range results {
		creditable += r.CreditableExport
		offsetUsed += r.SelfOffset + r.PriorCreditUsed
		expired += r.ExpiredCreditKWh
	}
	balance, _ := e.CreditBalance("c")
	if got := offsetUsed + balance + expired; got != creditable {
		t.Fatalf("invariant1: creditable=%d != offset=%d+balance=%d+expired=%d=%d",
			creditable, offsetUsed, balance, expired, got)
	}
	// 不变量 2：每月 下网 = 当月自抵 + 历史额度抵扣 + 计费电量。
	for _, r := range results {
		if r.RawImport != r.SelfOffset+r.PriorCreditUsed+r.BilledImportKWh {
			t.Fatalf("invariant2 month %s: %d != %d+%d+%d",
				r.Month, r.RawImport, r.SelfOffset, r.PriorCreditUsed, r.BilledImportKWh)
		}
	}
}

// TestSealCostIndependentOfHistory 证明封账开销不随已封账月份数量增长：
// 已到期/已用尽的额度被物理摘除，封账只触碰当月数据与当前存活额度。
func TestSealCostIndependentOfHistory(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	e, _ := testEngine(t)
	p := baseContract()
	p.CreditValidMonths = 1 // 每月额度次月即到期，历史笔快速累积并被摘除
	regConsumer(t, e, "c", p, basePrices())

	sealMonthZero := func(ms string) {
		m := month(t, ms)
		fillMonth(t, e, "c", m, func(int, time.Time) (int64, int64) { return 0, 0 })
		if err := e.SealMonth("c", m); err != nil {
			t.Fatal(err)
		}
	}
	for k := 0; k < 60; k++ {
		sealMonthZero(monthKeyString(2025, 1+k))
	}
	// 60 个历史月后，额度树应为空（全部到期摘除）；封账第 61 个月不遍历任何历史笔。
	if bal, _ := e.CreditBalance("c"); bal != 0 {
		t.Fatalf("balance=%d want 0", bal)
	}
	if treeNodes(e.consumers["c"].credits.root) != 0 {
		t.Fatalf("expired credits were not physically removed: nodes=%d", treeNodes(e.consumers["c"].credits.root))
	}
}

func monthKeyString(year, oneBased int) string {
	return monthStr(year, oneBased)
}

func monthStr(year, oneBased int) string {
	y := year + (oneBased-1)/12
	m := (oneBased-1)%12 + 1
	ms := []string{"", "01", "02", "03", "04", "05", "06", "07", "08", "09", "10", "11", "12"}
	s := itoa(y) + "-" + ms[m]
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func treeNodes(t *ctNode) int {
	if t == nil {
		return 0
	}
	return 1 + treeNodes(t.left) + treeNodes(t.right)
}

// --- tiny deterministic locked RNG for tests ---

type lockedRNG struct{ state uint64 }

func newLockedRNG(seed int64) *lockedRNG { return &lockedRNG{state: uint64(seed) | 1} }

func (r *lockedRNG) Intn(n int) int {
	// xorshift64*
	x := r.state
	x ^= x >> 12
	x ^= x << 25
	x ^= x >> 27
	r.state = x
	return int((x * 2685821657736338717) % uint64(n))
}
