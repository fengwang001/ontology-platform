// Package score 做市商报价义务考核引擎：登记做市关系、接受报价事件、
// 登记豁免窗口、日终结算与资格暂停。
//
// 所有操作可并发调用，内部经单把互斥锁串行化，结果等价于某个串行顺序。
// 被拒绝的操作按 参数非法>时钟回退>未登记>已暂停>未收盘>状态不符/冲突
// 的次序只报第一个错误，且不改任何状态（含时钟）。
package score

import (
	"errors"
	"fmt"
	"sync"

	"ontology/obligation"
	"ontology/quote"
)

// 各类拒绝原因，可用 errors.Is 区分。
var (
	ErrInvalidParam  = errors.New("score: 参数非法")
	ErrClock         = errors.New("score: 时钟回退")
	ErrNotRegistered = errors.New("score: 未登记")
	ErrSuspended     = errors.New("score: 已暂停")
	ErrNotClosed     = errors.New("score: 未收盘")
	ErrState         = errors.New("score: 状态不符")
	ErrConflict      = errors.New("score: 豁免窗口冲突")
)

const (
	maxNow   = int64(1_000_000_000_000)
	maxPrice = int64(1_000_000_000)
)

// Outcome 是单日考核结果。
type Outcome int

const (
	// Skip 应报时长为 0，跳过，连续不达标数不变。
	Skip Outcome = iota
	// Pass 达标，连续不达标数清零。
	Pass
	// Fail 不达标，连续不达标数加 1。
	Fail
)

func (o Outcome) String() string {
	switch o {
	case Skip:
		return "skip"
	case Pass:
		return "pass"
	default:
		return "fail"
	}
}

// Result 是单日结算结果。
type Result struct {
	Outcome   Outcome // 达标判定
	A         int64   // 当日合格时长（秒）
	D         int64   // 当日应报时长（秒）
	Misses    int64   // 结算后的连续不达标数
	Suspended bool    // 本次结算是否触发暂停
}

// Engine 是考核引擎，并发安全。
type Engine struct {
	mu sync.Mutex
	p  obligation.Params
	r  int64 // 达标线（百分比）
	k  int64 // 连续不达标上限

	maxT int64 // 已接受的最大 now
	hasT bool

	accts map[key]*account
	wins  map[string]*obligation.Windows

	replayed int // Settle 重放的报价事件记录数，恒为 0（增量累计）
}

type key struct{ mm, sym string }

type account struct {
	tr        *obligation.Tracker
	nextDay   int64 // 下一个应结算的日号
	misses    int64 // 连续不达标数
	suspended bool
}

// New 创建引擎。每日交易时段为日内秒 [open, close)。
func New(open, close, qmin, s, g, r, k int64) (*Engine, error) {
	if !(0 <= open && open < close && close <= obligation.DayLen) ||
		qmin < 1 || qmin > maxPrice ||
		s < 1 || s > 10000 ||
		g < 0 || g > 3600 ||
		r < 1 || r > 100 ||
		k < 1 || k > 100 {
		return nil, ErrInvalidParam
	}
	return &Engine{
		p:     obligation.Params{Open: open, Close: close, Qmin: qmin, S: s, G: g},
		r:     r,
		k:     k,
		accts: map[key]*account{},
		wins:  map[string]*obligation.Windows{},
	}, nil
}

// checkClock 校验 now 的取值范围与时钟单调性。
func (e *Engine) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if e.hasT && now < e.maxT {
		return ErrClock
	}
	return nil
}

// accept 在操作被接受后推进时钟。
func (e *Engine) accept(now int64) {
	e.maxT, e.hasT = now, true
}

func (e *Engine) account(mm, sym string) (*account, error) {
	a, ok := e.accts[key{mm, sym}]
	if !ok {
		return nil, fmt.Errorf("%s/%s: %w", mm, sym, ErrNotRegistered)
	}
	return a, nil
}

func (e *Engine) windows(sym string) *obligation.Windows {
	w := e.wins[sym]
	if w == nil {
		w = &obligation.Windows{}
		e.wins[sym] = w
	}
	return w
}

// Register 登记做市关系；重复登记报状态不符。
func (e *Engine) Register(now int64, mm, sym string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if mm == "" || sym == "" {
		return ErrInvalidParam
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	k := key{mm, sym}
	if _, ok := e.accts[k]; ok {
		return fmt.Errorf("register %s/%s: %w", mm, sym, ErrState)
	}
	e.accts[k] = &account{
		tr:      obligation.NewTracker(e.p, e.windows(sym), now),
		nextDay: now / obligation.DayLen,
	}
	e.accept(now)
	return nil
}

// Quote 整体替换报价，要求 0<bid<ask<=1e9，数量为 0 到 1e9。
func (e *Engine) Quote(now int64, mm, sym string, bid, bidQty, ask, askQty int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !(0 < bid && bid < ask && ask <= maxPrice) ||
		bidQty < 0 || bidQty > maxPrice || askQty < 0 || askQty > maxPrice {
		return ErrInvalidParam
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	a, err := e.account(mm, sym)
	if err != nil {
		return err
	}
	if a.suspended {
		return ErrSuspended
	}
	a.tr.OnQuote(now, quote.Quote{Bid: bid, BidQty: bidQty, Ask: ask, AskQty: askQty})
	e.accept(now)
	return nil
}

// Withdraw 清除报价；无报价时报状态不符。
func (e *Engine) Withdraw(now int64, mm, sym string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	a, err := e.account(mm, sym)
	if err != nil {
		return err
	}
	if a.suspended {
		return ErrSuspended
	}
	if !a.tr.HasQuote() {
		return fmt.Errorf("withdraw %s/%s: %w", mm, sym, ErrState)
	}
	a.tr.OnWithdraw(now)
	e.accept(now)
	return nil
}

// Fill 从该侧数量中扣减；无报价或 qty 超过该侧数量报状态不符。
func (e *Engine) Fill(now int64, mm, sym string, side quote.Side, qty int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !side.Valid() || qty < 0 {
		return ErrInvalidParam
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	a, err := e.account(mm, sym)
	if err != nil {
		return err
	}
	if a.suspended {
		return ErrSuspended
	}
	if !a.tr.HasQuote() || qty > a.tr.Qty(side) {
		return fmt.Errorf("fill %s/%s %s: %w", mm, sym, side, ErrState)
	}
	a.tr.OnFill(now, side, qty)
	e.accept(now)
	return nil
}

// Exempt 登记豁免窗口 [from, to)，要求 now<=from<to<=1e12，
// 且与该标的已有窗口不重叠（首尾相接不算重叠）。不要求登记做市关系。
func (e *Engine) Exempt(now int64, sym string, from, to int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sym == "" || !(now <= from && from < to && to <= maxNow) {
		return ErrInvalidParam
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !e.windows(sym).Add(from, to) {
		return fmt.Errorf("exempt %s [%d,%d): %w", sym, from, to, ErrConflict)
	}
	e.accept(now)
	return nil
}

// Settle 结算某日：要求 now>=day*86400+close；首次结算的 day 须为
// Register（或最近一次 Reinstate）所在日，其后逐日加 1。
// D=0 跳过（连续数不变）；A*100>=R*D 达标（取等达标，连续数清零）；
// 否则连续数加 1，达到 K 即暂停并在 now 清除现有报价。
func (e *Engine) Settle(now int64, mm, sym string, day int64) (Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if day < 0 {
		return Result{}, ErrInvalidParam
	}
	if err := e.checkClock(now); err != nil {
		return Result{}, err
	}
	a, err := e.account(mm, sym)
	if err != nil {
		return Result{}, err
	}
	if a.suspended {
		return Result{}, ErrSuspended
	}
	dayStart := day * obligation.DayLen
	if now < dayStart+e.p.Close {
		return Result{}, fmt.Errorf("settle day %d: %w", day, ErrNotClosed)
	}
	if day != a.nextDay {
		return Result{}, fmt.Errorf("settle day %d, want %d: %w", day, a.nextDay, ErrState)
	}
	a.tr.Sync(now)
	D := (e.p.Close - e.p.Open) -
		e.windows(sym).Intersection(dayStart+e.p.Open, dayStart+e.p.Close)
	A := a.tr.A(day)
	res := Result{A: A, D: D}
	switch {
	case D == 0:
		res.Outcome = Skip
	case A*100 >= e.r*D:
		res.Outcome = Pass
		a.misses = 0
	default:
		res.Outcome = Fail
		a.misses++
		if a.misses >= e.k {
			a.suspended = true
			a.tr.Clear(now)
		}
	}
	for range a.tr.DayEvents(day) { // 增量累计，无当日报价事件可重放
		e.replayed++
	}
	res.Misses = a.misses
	res.Suspended = a.suspended
	a.nextDay = day + 1
	e.accept(now)
	return res, nil
}

// Reinstate 解除暂停并清零连续不达标数，以其所在日为新的首个结算日；
// 非暂停时报状态不符。
func (e *Engine) Reinstate(now int64, mm, sym string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	a, err := e.account(mm, sym)
	if err != nil {
		return err
	}
	if !a.suspended {
		return fmt.Errorf("reinstate %s/%s: %w", mm, sym, ErrState)
	}
	a.suspended = false
	a.misses = 0
	a.nextDay = now / obligation.DayLen
	e.accept(now)
	return nil
}
