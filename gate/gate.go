// Package gate 是委托校验、拟成交闸门与中断恢复的并发安全门面。
package gate

import (
	"errors"
	"sync"

	"ontology/band"
	"ontology/phase"
)

// 拒绝原因哨兵错误，严格按拒绝次序排列；errors.Is 可区分。
var (
	// ErrInvalidParam：参数非法（取值越界或约束不满足）。
	ErrInvalidParam = errors.New("invalid parameter")
	// ErrClockBack：now 小于已接受的最大时钟。
	ErrClockBack = errors.New("clock moved backwards")
	// ErrUnknownSymbol：标的不存在。
	ErrUnknownSymbol = errors.New("unknown symbol")
	// ErrDuplicateSymbol：AddSymbol 标的重复。
	ErrDuplicateSymbol = errors.New("duplicate symbol")
	// ErrOutOfSession：Trade/Resume 的 now 不在交易时段 [open,close) 内。
	ErrOutOfSession = errors.New("out of trading session")
	// ErrNotHalted：Resume 时并未处于中断。
	ErrNotHalted = errors.New("not in volatility halt")
	// ErrHaltNotEnded：Resume 时 now < he。
	ErrHaltNotEnded = errors.New("halt not ended")
	// ErrHalted：中断期间发起 Trade（即使 now 已到 he，须先 Resume）。
	ErrHalted = errors.New("in volatility halt")
	// ErrLimit：价格超出涨跌停 [dn,up]。
	ErrLimit = errors.New("price outside daily limit")
	// ErrStaticBand：相对静态参考价超带（静态优先）。
	ErrStaticBand = errors.New("price outside static band")
	// ErrDynamicBand：相对动态参考价超带。
	ErrDynamicBand = errors.New("price outside dynamic band")
)

// HaltReason 区分中断由哪条带触发。
type HaltReason int

const (
	ReasonStatic HaltReason = iota
	ReasonDynamic
)

// HaltResult 是 Trade 触发中断时的结果。
type HaltResult struct {
	End    int64
	Reason HaltReason
}

// Gate 持有全部标的、统一时钟与锁。
type Gate struct {
	mu      sync.Mutex
	clock   int64
	clockOK bool
	symbols map[string]*symbol
}

// symbol 聚合单只标的的参数、带宽、历史与阶段机。
type symbol struct {
	band *band.Band
	hist *band.History
	ph   *phase.Phase

	window   int64
	duration int64
	open     int64
	close    int64
	lastCall int64
	haltMax  int
}

// New 创建空闸门。
func New() *Gate {
	return &Gate{symbols: make(map[string]*symbol)}
}

// AddSymbol 登记标的；末位参数 protection 为尾盘保护时长 C（lastCall=close-C）。
// 重复报 ErrDuplicateSymbol。
func (g *Gate) AddSymbol(sym string, prev, limit, ds, dd, de, window, duration int64, maxExtend, haltLimit int, open, close, protection int64) error {
	lastCall := close - protection
	if sym == "" ||
		prev < 1 || prev > 1_000_000_000 ||
		limit < 1 || limit > 10_000 ||
		ds < 1 || ds > 10_000 ||
		dd < 1 || dd > 10_000 ||
		de < 1 || de > 10_000 ||
		window < 1 || window > 1_000_000 ||
		duration < 1 || duration > 1_000_000 ||
		maxExtend < 0 || maxExtend > 10 ||
		haltLimit < 0 || haltLimit > 100 ||
		open < 0 || close < 0 || open >= close ||
		protection < 0 || protection > close-open {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.symbols[sym]; ok {
		return ErrDuplicateSymbol
	}
	b := band.NewBand(prev, limit, ds, dd, de)
	s := &symbol{
		band:     b,
		hist:     band.NewHistory(prev),
		ph:       phase.New(duration, maxExtend, haltLimit, lastCall),
		window:   window,
		duration: duration,
		open:     open,
		close:    close,
		lastCall: lastCall,
		haltMax:  haltLimit,
	}
	g.symbols[sym] = s
	return nil
}

// CheckOrder 只校验涨跌停，中断期间与时段外照常接受。
func (g *Gate) CheckOrder(now int64, sym string, price int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clockOK && now < g.clock {
		return ErrClockBack
	}
	s, ok := g.symbols[sym]
	if !ok {
		return ErrUnknownSymbol
	}
	if !s.band.LimitOK(price) {
		return ErrLimit
	}
	g.clock, g.clockOK = now, true
	return nil
}

// Trade 处理一笔拟成交：成交返回 nil，超带进入中断返回 *HaltResult，其余为错误。
func (g *Gate) Trade(now int64, sym string, price int64) (*HaltResult, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clockOK && now < g.clock {
		return nil, ErrClockBack
	}
	s, ok := g.symbols[sym]
	if !ok {
		return nil, ErrUnknownSymbol
	}
	if now < s.open || now >= s.close {
		return nil, ErrOutOfSession
	}
	if s.ph.Halted() {
		return nil, ErrHalted
	}
	if !s.band.LimitOK(price) {
		return nil, ErrLimit
	}
	rs := s.hist.Rs()
	rd := s.hist.Peek(now, s.window)
	staticOver := band.Over(price, rs, s.band.Ds)
	dynamicOver := band.Over(price, rd, s.band.Dd)
	if staticOver || dynamicOver {
		if now >= s.lastCall || s.ph.HaltCount() >= s.haltMax {
			if staticOver {
				return nil, ErrStaticBand
			}
			return nil, ErrDynamicBand
		}
		// 接受该操作：推进时钟并触发中断（本笔不成交）。
		g.clock = now
		g.clockOK = true
		reason := ReasonDynamic
		if staticOver {
			reason = ReasonStatic
		}
		return &HaltResult{End: s.ph.Begin(now), Reason: reason}, nil
	}
	// 接受成交：推进时钟、提交裁剪并记录成交。
	g.clock, g.clockOK = now, true
	s.hist.AppendTrade(now, price)
	return nil, nil
}

// ExtendResult 是 Resume 延长中断时的结果。
type ExtendResult struct{ End int64 }

// Resume 处理中断恢复：成交恢复返回 nil，延长返回 *ExtendResult。
func (g *Gate) Resume(now int64, sym string, price int64) (*ExtendResult, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, ErrInvalidParam
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.clockOK && now < g.clock {
		return nil, ErrClockBack
	}
	s, ok := g.symbols[sym]
	if !ok {
		return nil, ErrUnknownSymbol
	}
	if now < s.open || now >= s.close {
		return nil, ErrOutOfSession
	}
	if !s.ph.Halted() {
		return nil, ErrNotHalted
	}
	if now < s.ph.End() {
		return nil, ErrHaltNotEnded
	}
	if !s.band.LimitOK(price) {
		return nil, ErrLimit
	}
	g.clock, g.clockOK = now, true
	if s.ph.CanExtend(s.hist.Rs(), price, s.band.De) {
		return &ExtendResult{End: s.ph.Extend()}, nil
	}
	// 恢复：恢复价成交，静态参考价更新，历史清空并以本笔重新开始。
	s.ph.Recover()
	s.hist.Reset(now, price)
	return nil, nil
}
