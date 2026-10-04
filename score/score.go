// Package score 是报价义务考核引擎：登记做市关系、接收报价事件、
// 维护单调时钟与豁免窗口、按日结算并执行连续不达标暂停。
// 所有操作持同一把互斥锁，并发调用等价于某个串行顺序。
package score

import (
	"errors"
	"sync"

	"ontology/obligation"
	"ontology/quote"
)

// 各类拒绝原因，可用 errors.Is 区分。
var (
	ErrParam         = errors.New("score: invalid parameter")
	ErrClock         = errors.New("score: clock moved backward")
	ErrNotRegistered = errors.New("score: market maker not registered")
	ErrSuspended     = errors.New("score: market maker suspended")
	ErrNotClosed     = errors.New("score: trading day not closed")
	ErrState         = errors.New("score: state mismatch")
	ErrConflict      = errors.New("score: exemption window conflict")
)

// Side 表示报价的一侧，取自 quote 包。
type Side = quote.Side

const (
	Buy  = quote.Buy
	Sell = quote.Sell
)

const (
	maxNow    int64 = 1_000_000_000_000
	maxQty    int64 = 1_000_000_000
	secPerDay       = obligation.SecPerDay
)

// Result 是一次 Settle 的结算结果。
type Result struct {
	Day       int64 // 结算日号
	D         int64 // 应报秒数（时段长度减去豁免交集）
	A         int64 // 合格秒数
	Skipped   bool  // D==0，跳过考核
	Passed    bool  // A*100 >= R*D（取等达标）
	Consec    int   // 结算后的连续不达标数
	Suspended bool  // 本次结算后是否处于暂停
}

type key struct {
	mm  string
	sym string
}

type mmState struct {
	q         quote.State
	tr        *obligation.Tracker
	suspended bool
	consec    int
	nextDay   int64 // 下一个应结算的日号
}

// Engine 是考核引擎，零值不可用，须用 New 创建。
type Engine struct {
	mu     sync.Mutex
	open   int64
	close  int64
	qmin   int64
	spread int64
	grace  int64
	req    int64
	k      int64

	started bool
	maxNow  int64

	syms map[string]*obligation.Windows
	mms  map[key]*mmState

	quotes   int // 已接受的 Quote 次数（对照用）
	replayed int // Settle 访问的报价事件记录数，恒为 0
}

// New 创建引擎。每日交易时段为日内秒 [open, close)。
func New(open, close, qmin, spread, grace, req, k int64) (*Engine, error) {
	if open < 0 || open >= close || close > secPerDay ||
		qmin < 1 || qmin > maxQty ||
		spread < 1 || spread > 10000 ||
		grace < 0 || grace > 3600 ||
		req < 1 || req > 100 ||
		k < 1 || k > 100 {
		return nil, ErrParam
	}
	return &Engine{
		open:   open,
		close:  close,
		qmin:   qmin,
		spread: spread,
		grace:  grace,
		req:    req,
		k:      k,
		syms:   map[string]*obligation.Windows{},
		mms:    map[key]*mmState{},
	}, nil
}

// checkClock 校验 now 的参数合法性与单调性（调用方须已持锁）。
func (e *Engine) checkClock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrParam
	}
	if e.started && now < e.maxNow {
		return ErrClock
	}
	return nil
}

// accept 在接受一个操作后推进时钟。
func (e *Engine) accept(now int64) {
	e.maxNow = now
	e.started = true
}

func (e *Engine) symWindows(sym string) *obligation.Windows {
	ws, ok := e.syms[sym]
	if !ok {
		ws = &obligation.Windows{}
		e.syms[sym] = ws
	}
	return ws
}

// Register 登记做市关系；重复登记报状态不符。
func (e *Engine) Register(now int64, mm, sym string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	k := key{mm: mm, sym: sym}
	if _, ok := e.mms[k]; ok {
		return ErrState
	}
	day := obligation.Day(now)
	e.mms[k] = &mmState{
		tr:      obligation.NewTracker(e.open, e.close, e.grace, day, now),
		nextDay: day,
	}
	e.accept(now)
	return nil
}

// Quote 整体替换报价。
func (e *Engine) Quote(now int64, mm, sym string, bid, bidQty, ask, askQty int64) error {
	if bid <= 0 || bid >= ask || ask > maxQty ||
		bidQty < 0 || bidQty > maxQty || askQty < 0 || askQty > maxQty {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	m, ok := e.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	m.q.Set(bid, bidQty, ask, askQty)
	m.tr.OnQuote(now, m.nextDay, m.q.Qualified(e.qmin, e.spread), e.symWindows(sym))
	e.quotes++
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
	m, ok := e.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	if !m.q.Clear() {
		return ErrState
	}
	m.tr.OnWithdraw(now, m.nextDay, e.symWindows(sym))
	e.accept(now)
	return nil
}

// Fill 从指定侧数量中扣减；无报价或数量超过该侧时报状态不符。
func (e *Engine) Fill(now int64, mm, sym string, side Side, qty int64) error {
	if (side != Buy && side != Sell) || qty < 0 || qty > maxQty {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	m, ok := e.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if m.suspended {
		return ErrSuspended
	}
	qualBefore := m.q.Qualified(e.qmin, e.spread)
	if !m.q.Fill(side, qty) {
		return ErrState
	}
	m.tr.OnFill(now, m.nextDay, qualBefore, m.q.Qualified(e.qmin, e.spread), e.symWindows(sym))
	e.accept(now)
	return nil
}

// Exempt 为标的登记豁免窗口 [from, to)，要求 now<=from<to，
// 且与该标的已有窗口不重叠（首尾相接不算重叠）。不要求已登记。
func (e *Engine) Exempt(now int64, sym string, from, to int64) error {
	if from < now || from >= to || to > maxNow {
		return ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !e.symWindows(sym).Add(from, to) {
		return ErrConflict
	}
	e.accept(now)
	return nil
}

// Settle 结算指定日。要求 now>=day*86400+close，且 day 为应结算日。
func (e *Engine) Settle(now int64, mm, sym string, day int64) (Result, error) {
	var res Result
	if day < 0 || day > maxNow/secPerDay {
		return res, ErrParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return res, err
	}
	m, ok := e.mms[key{mm: mm, sym: sym}]
	if !ok {
		return res, ErrNotRegistered
	}
	if m.suspended {
		return res, ErrSuspended
	}
	if now < day*secPerDay+e.close {
		return res, ErrNotClosed
	}
	if day != m.nextDay {
		return res, ErrState
	}
	ws := e.symWindows(sym)
	a := m.tr.SettleDay(day, m.nextDay, ws)
	d := (e.close - e.open) - ws.Overlap(day*secPerDay+e.open, day*secPerDay+e.close)
	res = Result{Day: day, D: d, A: a}
	m.nextDay = day + 1
	switch {
	case d == 0:
		res.Skipped = true
	case a*100 >= e.req*d:
		res.Passed = true
		m.consec = 0
	default:
		m.consec++
		if int64(m.consec) >= e.k {
			m.suspended = true
			m.q.Clear()
			m.tr.OnWithdraw(now, m.nextDay, ws)
		}
	}
	res.Consec = m.consec
	res.Suspended = m.suspended
	e.accept(now)
	return res, nil
}

// Reinstate 解除暂停并清零连续不达标数；非暂停时报状态不符。
// 恢复后首个应结算日为恢复所在日。
func (e *Engine) Reinstate(now int64, mm, sym string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	m, ok := e.mms[key{mm: mm, sym: sym}]
	if !ok {
		return ErrNotRegistered
	}
	if !m.suspended {
		return ErrState
	}
	m.suspended = false
	m.consec = 0
	m.nextDay = obligation.Day(now)
	m.tr.DiscardBefore(m.nextDay)
	e.accept(now)
	return nil
}
