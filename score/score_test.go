package score

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/quote"
)

func mustNew(t *testing.T, open, close, qmin, spread, grace, req, k int64) *Engine {
	t.Helper()
	e, err := New(open, close, qmin, spread, grace, req, k)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestSpecExample 精确复现题目示例：D=900，A=810，810*100==90*900 取等达标。
func TestSpecExample(t *testing.T) {
	e := mustNew(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Register(0, "mm", "s"))
	must(t, e.Quote(100, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Fill(400, "mm", "s", Buy, 15))
	must(t, e.Quote(405, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Fill(700, "mm", "s", Sell, 15))
	must(t, e.Quote(706, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Exempt(750, "s", 800, 900))
	must(t, e.Withdraw(1016, "mm", "s"))
	res, err := e.Settle(1100, "mm", "s", 0)
	must(t, err)
	if res.D != 900 || res.A != 810 || !res.Passed || res.Skipped || res.Consec != 0 {
		t.Fatalf("res=%+v, want D=900 A=810 Passed", res)
	}
	t.Logf("判定依据: A*100=%d, R*D=%d, 取等达标", res.A*100, 90*res.D)
}

// TestSpecExampleLateWithdraw 撤单提前 1 秒则 A=809 不达标。
func TestSpecExampleLateWithdraw(t *testing.T) {
	e := mustNew(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Register(0, "mm", "s"))
	must(t, e.Quote(100, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Fill(400, "mm", "s", Buy, 15))
	must(t, e.Quote(405, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Fill(700, "mm", "s", Sell, 15))
	must(t, e.Quote(706, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Exempt(750, "s", 800, 900))
	must(t, e.Withdraw(1015, "mm", "s"))
	res, err := e.Settle(1100, "mm", "s", 0)
	must(t, err)
	if res.A != 809 || res.Passed || res.Consec != 1 {
		t.Fatalf("res=%+v, want A=809 不达标 Consec=1", res)
	}
}

// TestNoGraceOnActiveQuote 主动改量（Quote 造成不合格）不享受宽限。
func TestNoGraceOnActiveQuote(t *testing.T) {
	e := mustNew(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Register(0, "mm", "s"))
	must(t, e.Quote(100, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Quote(400, "mm", "s", 9950, 5, 10050, 20)) // 主动改小买侧量
	must(t, e.Quote(405, "mm", "s", 9950, 20, 10050, 20))
	res, err := e.Settle(1100, "mm", "s", 0)
	must(t, err)
	if res.A != 995 {
		t.Fatalf("A=%d, want 995（[400,405) 不补记）", res.A)
	}
}

// TestExemptIntersectsUnqualified 豁免与不合格段相交：D 减少，A 不变。
func TestExemptIntersectsUnqualified(t *testing.T) {
	e := mustNew(t, 100, 1100, 10, 100, 5, 90, 3)
	must(t, e.Register(0, "mm", "s"))
	must(t, e.Quote(100, "mm", "s", 9950, 20, 10050, 20))
	must(t, e.Withdraw(300, "mm", "s"))
	must(t, e.Exempt(350, "s", 400, 500))
	res, err := e.Settle(1100, "mm", "s", 0)
	must(t, err)
	if res.D != 900 || res.A != 200 {
		t.Fatalf("res=%+v, want D=900 A=200", res)
	}
}

// TestZeroDutySkipped D=0 时跳过，连续不达标数不变。
func TestZeroDutySkipped(t *testing.T) {
	e := mustNew(t, 100, 200, 10, 100, 5, 90, 3)
	must(t, e.Register(0, "mm", "s"))
	res0, err := e.Settle(200, "mm", "s", 0) // 无报价，不达标
	must(t, err)
	if res0.Consec != 1 {
		t.Fatalf("day0 后 Consec=%d, want 1", res0.Consec)
	}
	must(t, e.Exempt(300, "s", 86400+100, 86400+200)) // 覆盖 day1 整个时段
	res1, err := e.Settle(86400+200, "mm", "s", 1)
	must(t, err)
	if !res1.Skipped || res1.D != 0 || res1.Consec != 1 {
		t.Fatalf("day1 res=%+v, want Skipped 且 Consec 不变", res1)
	}
}

// TestSuspendAfterK 连续 K 日不达标即暂停并清除报价。
func TestSuspendAfterK(t *testing.T) {
	e := mustNew(t, 100, 200, 10, 100, 5, 90, 2)
	must(t, e.Register(0, "mm", "s"))
	must(t, e.Quote(100, "mm", "s", 9950, 5, 10050, 5)) // 量不足，全程不合格
	res0, err := e.Settle(200, "mm", "s", 0)
	must(t, err)
	if res0.Consec != 1 || res0.Suspended {
		t.Fatalf("day0 res=%+v, want Consec=1 未暂停", res0)
	}
	res1, err := e.Settle(86400+200, "mm", "s", 1)
	must(t, err)
	if res1.Consec != 2 || !res1.Suspended {
		t.Fatalf("day1 res=%+v, want Consec=2 已暂停", res1)
	}
	for name, err := range map[string]error{
		"Quote":    e.Quote(86400+300, "mm", "s", 1, 1, 2, 1),
		"Withdraw": e.Withdraw(86400+300, "mm", "s"),
		"Fill":     e.Fill(86400+300, "mm", "s", Buy, 1),
	} {
		if !errors.Is(err, ErrSuspended) {
			t.Fatalf("暂停后 %s 应报 ErrSuspended, got %v", name, err)
		}
	}
	if _, err := e.Settle(2*86400+200, "mm", "s", 2); !errors.Is(err, ErrSuspended) {
		t.Fatalf("暂停后 Settle 应报 ErrSuspended, got %v", err)
	}
	// 恢复后连续数清零，首个应结算日为恢复所在日；报价已在暂停时清除。
	must(t, e.Reinstate(2*86400+300, "mm", "s"))
	if err := e.Withdraw(2*86400+301, "mm", "s"); !errors.Is(err, ErrState) {
		t.Fatalf("报价应已在暂停时清除, got %v", err)
	}
	res2, err := e.Settle(3*86400+200, "mm", "s", 2)
	must(t, err)
	if res2.Consec != 1 || res2.Suspended {
		t.Fatalf("恢复后 day2 res=%+v, want Consec=1", res2)
	}
}

// TestSettleDayOrder 结算日必须自登记日起逐日加 1。
func TestSettleDayOrder(t *testing.T) {
	e := mustNew(t, 100, 200, 10, 100, 5, 90, 3)
	must(t, e.Register(50, "mm", "s"))
	if _, err := e.Settle(86400+200, "mm", "s", 1); !errors.Is(err, ErrState) {
		t.Fatalf("首次结算非登记日应报状态不符, got %v", err)
	}
	if _, err := e.Settle(150, "mm", "s", 0); !errors.Is(err, ErrNotClosed) {
		t.Fatalf("未收盘应报 ErrNotClosed, got %v", err)
	}
	if _, err := e.Settle(200, "mm", "s", 0); err != nil {
		t.Fatalf("day0 结算失败: %v", err)
	}
	if _, err := e.Settle(86400+200, "mm", "s", 0); !errors.Is(err, ErrState) {
		t.Fatalf("重复结算 day0 应报状态不符, got %v", err)
	}
	if _, err := e.Settle(2*86400+200, "mm", "s", 2); !errors.Is(err, ErrState) {
		t.Fatalf("跳日结算应报状态不符, got %v", err)
	}
	if _, err := e.Settle(86400+200, "mm", "s", 1); err != nil {
		t.Fatalf("day1 结算失败: %v", err)
	}
}

// TestRejectOrder 拒绝按次序只报第一个，且被拒绝操作不改状态（含时钟）。
func TestRejectOrder(t *testing.T) {
	newEng := func(t *testing.T) *Engine {
		e := mustNew(t, 100, 200, 10, 100, 5, 90, 2)
		must(t, e.Register(100, "mm", "s"))
		return e
	}
	cases := []struct {
		name string
		op   func(e *Engine) error
		want error
	}{
		{"参数非法优先于时钟", func(e *Engine) error {
			return e.Quote(50, "mm", "s", 0, 1, 2, 1) // bid<=0 且时钟回退
		}, ErrParam},
		{"时钟优先于未登记", func(e *Engine) error {
			return e.Quote(50, "ghost", "s", 1, 1, 2, 1)
		}, ErrClock},
		{"未登记", func(e *Engine) error {
			return e.Quote(150, "ghost", "s", 1, 1, 2, 1)
		}, ErrNotRegistered},
		{"重复登记报状态不符", func(e *Engine) error {
			return e.Register(150, "mm", "s")
		}, ErrState},
		{"无报价撤单报状态不符", func(e *Engine) error {
			return e.Withdraw(150, "mm", "s")
		}, ErrState},
		{"无报价成交报状态不符", func(e *Engine) error {
			return e.Fill(150, "mm", "s", Buy, 1)
		}, ErrState},
		{"Fill数量超该侧报状态不符", func(e *Engine) error {
			must(t, e.Quote(150, "mm", "s", 100, 10, 101, 10))
			return e.Fill(160, "mm", "s", Buy, 11)
		}, ErrState},
		{"非暂停时恢复报状态不符", func(e *Engine) error {
			return e.Reinstate(150, "mm", "s")
		}, ErrState},
		{"豁免参数非法", func(e *Engine) error {
			return e.Exempt(150, "s", 140, 160) // from<now
		}, ErrParam},
		{"豁免区间非法", func(e *Engine) error {
			return e.Exempt(150, "s", 160, 160)
		}, ErrParam},
		{"豁免重叠报冲突", func(e *Engine) error {
			must(t, e.Exempt(150, "s", 300, 400))
			return e.Exempt(160, "s", 350, 450)
		}, ErrConflict},
		{"豁免首尾相接不算重叠", func(e *Engine) error {
			must(t, e.Exempt(150, "s", 300, 400))
			return e.Exempt(160, "s", 400, 500)
		}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEng(t)
			err := c.op(e)
			if c.want == nil {
				must(t, err)
				return
			}
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

// TestRejectedOpKeepsClock 被拒绝的操作不推进时钟。
func TestRejectedOpKeepsClock(t *testing.T) {
	e := mustNew(t, 100, 200, 10, 100, 5, 90, 2)
	must(t, e.Register(100, "mm", "s"))
	if err := e.Quote(500, "mm", "s", 0, 1, 2, 1); !errors.Is(err, ErrParam) {
		t.Fatalf("应报参数非法, got %v", err)
	}
	if err := e.Quote(50, "mm", "s", 1, 1, 2, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("应报时钟回退, got %v", err)
	}
	// 时钟仍停在 100：now=150 应被接受。
	must(t, e.Quote(150, "mm", "s", 100, 10, 101, 10))
}

// TestSettleNoReplay Settle 访问的报价事件记录数为 0，
// 与当日 Quote 次数无关（10 次与 10000 次两档对照）。
func TestSettleNoReplay(t *testing.T) {
	for _, n := range []int{10, 10000} {
		e := mustNew(t, 100, 1100, 10, 100, 5, 90, 3)
		must(t, e.Register(0, "mm", "s"))
		for i := 0; i < n; i++ {
			must(t, e.Quote(int64(100+i), "mm", "s", 9950, 20, 10050, 20))
		}
		settleAt := int64(100 + n)
		if settleAt < 1100 {
			settleAt = 1100
		}
		res, err := e.Settle(settleAt, "mm", "s", 0)
		must(t, err)
		if !res.Passed {
			t.Fatalf("n=%d 应达标: %+v", n, res)
		}
		if e.replayed != 0 {
			t.Fatalf("n=%d 时 replayed=%d, 应为 0", n, e.replayed)
		}
		if e.quotes != n {
			t.Fatalf("quotes=%d, want %d", e.quotes, n)
		}
	}
}

// ---------- 朴素逐秒参考实现（结算时重放事件、逐秒判定） ----------

const (
	evQuote = iota
	evWithdraw
	evFill
)

type nEvent struct {
	t           int64
	kind        int
	bid, bidQty int64
	ask, askQty int64
	side        Side
	qty         int64
}

type nMM struct {
	q         quote.State
	events    []nEvent
	suspended bool
	consec    int
	nextDay   int64
}

type naive struct {
	open, close int64
	qmin        int64
	spread      int64
	grace       int64
	req         int64
	k           int64
	started     bool
	maxNow      int64
	syms        map[string][][2]int64
	mms         map[key]*nMM
}

func newNaive(open, close, qmin, spread, grace, req, k int64) *naive {
	return &naive{
		open: open, close: close, qmin: qmin, spread: spread,
		grace: grace, req: req, k: k,
		syms: map[string][][2]int64{},
		mms:  map[key]*nMM{},
	}
}

func (n *naive) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrParam
	}
	if n.started && now < n.maxNow {
		return ErrClock
	}
	return nil
}

func (n *naive) accept(now int64) { n.maxNow, n.started = now, true }

func (n *naive) register(now int64, mm, sym string) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	kk := key{mm: mm, sym: sym}
	if _, ok := n.mms[kk]; ok {
		return ErrState
	}
	n.mms[kk] = &nMM{nextDay: now / secPerDay}
	n.accept(now)
	return nil
}

func (n *naive) quote(now int64, mm, sym string, bid, bidQty, ask, askQty int64) error {
	if bid <= 0 || bid >= ask || ask > maxQty ||
		bidQty < 0 || bidQty > maxQty || askQty < 0 || askQty > maxQty {
		return ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	m.q.Set(bid, bidQty, ask, askQty)
	m.events = append(m.events, nEvent{t: now, kind: evQuote, bid: bid, bidQty: bidQty, ask: ask, askQty: askQty})
	n.accept(now)
	return nil
}

func (n *naive) withdraw(now int64, mm, sym string) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	if !m.q.Clear() {
		return ErrState
	}
	m.events = append(m.events, nEvent{t: now, kind: evWithdraw})
	n.accept(now)
	return nil
}

func (n *naive) fill(now int64, mm, sym string, side Side, qty int64) error {
	if (side != Buy && side != Sell) || qty < 0 || qty > maxQty {
		return ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	if !m.q.Fill(side, qty) {
		return ErrState
	}
	m.events = append(m.events, nEvent{t: now, kind: evFill, side: side, qty: qty})
	n.accept(now)
	return nil
}

func (n *naive) exempt(now int64, sym string, from, to int64) error {
	if from < now || from >= to || to > maxNow {
		return ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	for _, w := range n.syms[sym] {
		if from < w[1] && w[0] < to {
			return ErrConflict
		}
	}
	n.syms[sym] = append(n.syms[sym], [2]int64{from, to})
	n.accept(now)
	return nil
}

func exemptAt(ws [][2]int64, s int64) bool {
	for _, w := range ws {
		if s >= w[0] && s < w[1] {
			return true
		}
	}
	return false
}

func exemptOverlap(ws [][2]int64, lo, hi int64) int64 {
	var total int64
	for _, w := range ws {
		if w[1] <= lo || w[0] >= hi {
			continue
		}
		total += min(hi, w[1]) - max(lo, w[0])
	}
	return total
}

// settle 逐秒重放当日事件：先应用开盘前事件得到初始报价，
// 再逐秒应用当日事件并判定合格，宽限补记段单独记录后统一计数。
func (n *naive) settle(now int64, mm, sym string, day int64) (Result, error) {
	var res Result
	if day < 0 || day > maxNow/secPerDay {
		return res, ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return res, err
	}
	m, ok := n.mms[key{mm: mm, sym: sym}]
	if !ok {
		return res, ErrNotRegistered
	}
	if m.suspended {
		return res, ErrSuspended
	}
	if now < day*secPerDay+n.close {
		return res, ErrNotClosed
	}
	if day != m.nextDay {
		return res, ErrState
	}
	dayOpen := day*secPerDay + n.open
	dayClose := day*secPerDay + n.close
	var q quote.State
	ei := 0
	for ei < len(m.events) && m.events[ei].t < dayOpen {
		applyEvent(&q, m.events[ei])
		ei++
	}
	raw := make([]bool, 0, dayClose-dayOpen)
	var credited [][2]int64
	graceOn := false
	var t0 int64
	for s := dayOpen; s < dayClose; s++ {
		for ei < len(m.events) && m.events[ei].t == s {
			ev := m.events[ei]
			switch ev.kind {
			case evQuote:
				applyEvent(&q, ev)
				if graceOn {
					if q.Qualified(n.qmin, n.spread) && s <= t0+n.grace {
						credited = append(credited, [2]int64{t0, s})
					}
					graceOn = false
				}
			case evWithdraw:
				applyEvent(&q, ev)
				graceOn = false
			case evFill:
				before := q.Qualified(n.qmin, n.spread)
				applyEvent(&q, ev)
				if before && !q.Qualified(n.qmin, n.spread) && !graceOn {
					graceOn = true
					t0 = s
				}
			}
			ei++
		}
		raw = append(raw, q.Qualified(n.qmin, n.spread))
	}
	ws := n.syms[sym]
	var a int64
	for i, qual := range raw {
		s := dayOpen + int64(i)
		ok := qual
		if !ok {
			for _, c := range credited {
				if s >= c[0] && s < c[1] {
					ok = true
					break
				}
			}
		}
		if ok && !exemptAt(ws, s) {
			a++
		}
	}
	d := (n.close - n.open) - exemptOverlap(ws, dayOpen, dayClose)
	res = Result{Day: day, D: d, A: a}
	m.nextDay = day + 1
	switch {
	case d == 0:
		res.Skipped = true
	case a*100 >= n.req*d:
		res.Passed = true
		m.consec = 0
	default:
		m.consec++
		if int64(m.consec) >= n.k {
			m.suspended = true
			m.q.Clear()
			m.events = append(m.events, nEvent{t: now, kind: evWithdraw})
		}
	}
	res.Consec = m.consec
	res.Suspended = m.suspended
	n.accept(now)
	return res, nil
}

func (n *naive) reinstate(now int64, mm, sym string) error {
	if err := n.checkClock(now); err != nil {
		return err
	}
	m, ok := n.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if !m.suspended {
		return ErrState
	}
	m.suspended = false
	m.consec = 0
	m.nextDay = now / secPerDay
	n.accept(now)
	return nil
}

func applyEvent(q *quote.State, ev nEvent) {
	switch ev.kind {
	case evQuote:
		q.Set(ev.bid, ev.bidQty, ev.ask, ev.askQty)
	case evWithdraw:
		q.Clear()
	case evFill:
		q.Fill(ev.side, ev.qty)
	}
}

// ---------- 随机序列对照 ----------

type genPair struct {
	mm, sym   string
	hasQuote  bool
	bidQty    int64
	askQty    int64
	suspended bool
	nextDay   int64
}

// runSequence 用同一种子在真实引擎与朴素引擎上并行执行随机操作序列，
// 逐步比对错误与结算结果，返回真实引擎的全部结算结果。
func runSequence(t *testing.T, seed int64, verbose bool) []Result {
	r := rand.New(rand.NewSource(seed))
	open := r.Int63n(100)
	close := open + 60 + r.Int63n(300)
	qmin := 1 + r.Int63n(20)
	spread := int64(1 + r.Intn(200))
	grace := r.Int63n(8)
	req := int64(50 + r.Intn(51))
	kk := int64(1 + r.Intn(3))
	real, err := New(open, close, qmin, spread, grace, req, kk)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nav := newNaive(open, close, qmin, spread, grace, req, kk)
	if verbose {
		t.Logf("seed=%d 参数: 时段=[%d,%d) Qmin=%d S=%d G=%d R=%d K=%d",
			seed, open, close, qmin, spread, grace, req, kk)
	}
	pairs := []genPair{{mm: "mm0", sym: "s0"}}
	switch r.Intn(3) {
	case 0:
		pairs = append(pairs, genPair{mm: "mm1", sym: "s0"}) // 共享标的与豁免
	case 1:
		pairs = append(pairs, genPair{mm: "mm0", sym: "s1"})
	}
	now := r.Int63n(50)
	for i := range pairs {
		p := &pairs[i]
		errR := real.Register(now, p.mm, p.sym)
		errN := nav.register(now, p.mm, p.sym)
		checkSameErr(t, seed, "Register", errR, errN)
		p.nextDay = now / secPerDay
	}
	var results []Result
	steps := 40 + r.Intn(40)
	for step := 0; step < steps; step++ {
		now += r.Int63n(15)
		p := &pairs[r.Intn(len(pairs))]
		op := r.Intn(100)
		switch {
		case op < 35: // Quote
			bid := 1 + r.Int63n(20000)
			ask := bid + 1 + r.Int63n(300)
			bq := r.Int63n(2*qmin + 5)
			aq := r.Int63n(2*qmin + 5)
			errR := real.Quote(now, p.mm, p.sym, bid, bq, ask, aq)
			errN := nav.quote(now, p.mm, p.sym, bid, bq, ask, aq)
			checkSameErr(t, seed, "Quote", errR, errN)
			if verbose {
				t.Logf("seed=%d step=%d Quote(now=%d,%s,%s,bid=%d,bq=%d,ask=%d,aq=%d) -> %v",
					seed, step, now, p.mm, p.sym, bid, bq, ask, aq, errR)
			}
			if errR == nil {
				p.hasQuote, p.bidQty, p.askQty = true, bq, aq
			}
		case op < 45: // Withdraw
			errR := real.Withdraw(now, p.mm, p.sym)
			errN := nav.withdraw(now, p.mm, p.sym)
			checkSameErr(t, seed, "Withdraw", errR, errN)
			if verbose {
				t.Logf("seed=%d step=%d Withdraw(now=%d,%s,%s) -> %v", seed, step, now, p.mm, p.sym, errR)
			}
			if errR == nil {
				p.hasQuote = false
			}
		case op < 65: // Fill
			if !p.hasQuote {
				continue
			}
			side := Side(r.Intn(2))
			limit := p.bidQty
			if side == Sell {
				limit = p.askQty
			}
			qty := r.Int63n(limit + 3) // 偶尔超限，触发状态不符
			errR := real.Fill(now, p.mm, p.sym, side, qty)
			errN := nav.fill(now, p.mm, p.sym, side, qty)
			checkSameErr(t, seed, "Fill", errR, errN)
			if verbose {
				t.Logf("seed=%d step=%d Fill(now=%d,%s,%s,side=%d,qty=%d) -> %v",
					seed, step, now, p.mm, p.sym, side, qty, errR)
			}
			if errR == nil {
				if side == Buy {
					p.bidQty -= qty
				} else {
					p.askQty -= qty
				}
			}
		case op < 75: // Exempt
			from := now + r.Int63n(120)
			to := from + 1 + r.Int63n(200)
			errR := real.Exempt(now, p.sym, from, to)
			errN := nav.exempt(now, p.sym, from, to)
			checkSameErr(t, seed, "Exempt", errR, errN)
			if verbose {
				t.Logf("seed=%d step=%d Exempt(now=%d,%s,[%d,%d)) -> %v", seed, step, now, p.sym, from, to, errR)
			}
		case op < 90: // Settle（到期才结算，否则跳过）
			due := p.nextDay*secPerDay + close
			if now < due {
				if r.Intn(3) != 0 {
					continue
				}
				now = due + r.Int63n(30)
			}
			resR, errR := real.Settle(now, p.mm, p.sym, p.nextDay)
			resN, errN := nav.settle(now, p.mm, p.sym, p.nextDay)
			checkSameErr(t, seed, "Settle", errR, errN)
			if errR == nil {
				if resR != resN {
					t.Fatalf("seed=%d step=%d Settle(day=%d): real=%+v naive=%+v",
						seed, step, p.nextDay, resR, resN)
				}
				if verbose {
					t.Logf("seed=%d step=%d Settle(now=%d,%s,%s,day=%d) -> %+v; 判定: A*100=%d vs R*D=%d",
						seed, step, now, p.mm, p.sym, p.nextDay, resR, resR.A*100, req*resR.D)
				}
				if resR.A < 0 || resR.A > resR.D || resR.D > close-open {
					t.Fatalf("seed=%d 不变量被破坏: %+v", seed, resR)
				}
				results = append(results, resR)
				p.nextDay++
				p.suspended = resR.Suspended
			}
		default: // Reinstate
			if !p.suspended {
				continue
			}
			errR := real.Reinstate(now, p.mm, p.sym)
			errN := nav.reinstate(now, p.mm, p.sym)
			checkSameErr(t, seed, "Reinstate", errR, errN)
			if verbose {
				t.Logf("seed=%d step=%d Reinstate(now=%d,%s,%s) -> %v", seed, step, now, p.mm, p.sym, errR)
			}
			if errR == nil {
				p.suspended = false
				p.nextDay = now / secPerDay
				p.hasQuote = false
			}
		}
	}
	return results
}

func checkSameErr(t *testing.T, seed int64, op string, errR, errN error) {
	t.Helper()
	if (errR == nil) != (errN == nil) {
		t.Fatalf("seed=%d %s: real=%v naive=%v", seed, op, errR, errN)
	}
	if errR != nil && !errors.Is(errR, errN) {
		t.Fatalf("seed=%d %s: real=%v naive=%v", seed, op, errR, errN)
	}
}

// TestRandomVsNaive 1000 组随机操作序列与逐秒朴素实现对照；
// 同一序列重放两次，结算结果必须一致。
func TestRandomVsNaive(t *testing.T) {
	total, passed, failed, skipped, suspended := 0, 0, 0, 0, 0
	for seed := int64(0); seed < 1000; seed++ {
		first := runSequence(t, seed, seed < 3)
		second := runSequence(t, seed, false)
		if len(first) != len(second) {
			t.Fatalf("seed=%d 重放结果数不同: %d vs %d", seed, len(first), len(second))
		}
		for i := range first {
			if first[i] != second[i] {
				t.Fatalf("seed=%d 重放第 %d 次结算不一致: %+v vs %+v", seed, i, first[i], second[i])
			}
		}
		for _, res := range first {
			total++
			switch {
			case res.Skipped:
				skipped++
			case res.Passed:
				passed++
			default:
				failed++
			}
			if res.Suspended {
				suspended++
			}
		}
	}
	t.Logf("1000 组序列共 %d 次结算: 达标=%d 不达标=%d 跳过=%d 触发暂停=%d",
		total, passed, failed, skipped, suspended)
	if total < 500 || failed == 0 || passed == 0 {
		t.Fatalf("随机覆盖不足: total=%d passed=%d failed=%d", total, passed, failed)
	}
}

// TestConcurrent 并发调用等价于某个串行顺序（-race 下验证），
// 且结算结果满足 0<=A<=D<=close-open、连续不达标数不超过 K。
func TestConcurrent(t *testing.T) {
	e := mustNew(t, 100, 1000, 5, 100, 5, 80, 3)
	pairs := []genPair{
		{mm: "mm0", sym: "s0"}, {mm: "mm1", sym: "s0"},
		{mm: "mm0", sym: "s1"}, {mm: "mm2", sym: "s2"},
	}
	var counter int64
	now := func() int64 { return atomic.AddInt64(&counter, 1) }
	for _, p := range pairs {
		must(t, e.Register(now(), p.mm, p.sym))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < 2000; i++ {
				p := pairs[r.Intn(len(pairs))]
				switch r.Intn(4) {
				case 0, 1:
					_ = e.Quote(now(), p.mm, p.sym, 100, 10, 101, 10)
				case 2:
					_ = e.Fill(now(), p.mm, p.sym, Side(r.Intn(2)), int64(r.Intn(4)))
				case 3:
					_ = e.Withdraw(now(), p.mm, p.sym)
				}
			}
		}(g)
	}
	wg.Wait()
	for _, p := range pairs {
		res, err := e.Settle(now(), p.mm, p.sym, 0)
		must(t, err)
		if res.A < 0 || res.A > res.D || res.D > 900 || res.Consec > 3 {
			t.Fatalf("并发后不变量被破坏: %+v", res)
		}
	}
}
