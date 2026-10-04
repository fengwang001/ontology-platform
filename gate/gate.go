// Package gate 是涨跌停与波动性中断的成交价格闸门门面。
package gate

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/band"
	"ontology/phase"
)

var (
	ErrParam    = errors.New("参数非法")
	ErrClock    = errors.New("时钟回退")
	ErrSymbol   = errors.New("标的不存在或重复")
	ErrPhase    = errors.New("阶段不符")
	ErrHalted   = errors.New("已中断")
	ErrLimit    = errors.New("涨跌停")
	ErrOverBand = errors.New("超带")
	ErrStatic   = errors.New("静态超带")
	ErrDynamic  = errors.New("动态超带")
)

// Reason 是中断原因。
type Reason string

const (
	ReasonStatic  Reason = "static"
	ReasonDynamic Reason = "dynamic"
)

// HaltResult 是触发中断或延长时的返回。
type HaltResult struct {
	Halted bool
	Reason Reason
	HE     int64
}

// TradeResult 是 Trade 的结果。
type TradeResult struct {
	Filled bool
	Halt   *HaltResult
}

// ResumeResult 是 Resume 的结果。
type ResumeResult struct {
	Extended bool
	Filled   bool
	HE       int64
}

// Gate 是闸门。
type Gate struct {
	mu     sync.Mutex
	maxNow int64
	syms   map[string]*symState
}

type symState struct {
	sym  *band.Symbol
	book *band.Book
	ph   *phase.Machine
}

// New 建立空闸门。
func New() *Gate { return &Gate{syms: map[string]*symState{}} }

func inRange(v, lo, hi int64) bool { return lo <= v && v <= hi }

func staticErr() error  { return fmt.Errorf("%w（%w）：静态参考价", ErrOverBand, ErrStatic) }
func dynamicErr() error { return fmt.Errorf("%w（%w）：动态参考价", ErrOverBand, ErrDynamic) }

// rdPeek 不改变队列地求 now 时刻动态参考价；老化记录恒为队首连续前缀。
func rdPeek(b *band.Book, now, w int64) int64 {
	cutoff := now - w
	off := sort.Search(b.Len(), func(k int) bool {
		return b.At(k).Time > cutoff
	})
	if off == 0 {
		return b.LastRd()
	}
	return b.At(off - 1).Price
}

// AddSymbol 添加标的。
func (g *Gate) AddSymbol(sym string, prev, l, ds, dd, de, w, t, x, hmax, open, close, c int64) error {
	if sym == "" ||
		!inRange(prev, 1, 1_000_000_000) ||
		!inRange(l, 1, 10000) || !inRange(ds, 1, 10000) ||
		!inRange(dd, 1, 10000) || !inRange(de, 1, 10000) ||
		!inRange(w, 1, 1_000_000) || !inRange(t, 1, 1_000_000) ||
		!inRange(x, 0, 10) || !inRange(hmax, 0, 100) ||
		!(0 <= open && open < close && close <= 1_000_000_000_000) ||
		!(0 <= c && c <= close-open) {
		return fmt.Errorf("%w：AddSymbol 参数越界", ErrParam)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.syms[sym]; ok {
		return fmt.Errorf("%w：标的 %q 重复", ErrSymbol, sym)
	}
	p := band.Params{Prev: prev, L: l, Ds: ds, Dd: dd, De: de, W: w, T: t,
		X: x, Hmax: hmax, Open: open, Close: close, C: c}
	g.syms[sym] = &symState{sym: band.New(p), book: band.NewBook(prev), ph: phase.New()}
	return nil
}

// begin 校验带时钟操作的公共前缀：参数、时钟、标的。
func (g *Gate) begin(now int64, sym string) (*symState, error) {
	if !inRange(now, 0, 1_000_000_000_000) || sym == "" {
		return nil, fmt.Errorf("%w：now 越界或标的为空", ErrParam)
	}
	if now < g.maxNow {
		return nil, fmt.Errorf("%w：now=%d 小于已接受最大时刻 %d", ErrClock, now, g.maxNow)
	}
	st, ok := g.syms[sym]
	if !ok {
		return nil, fmt.Errorf("%w：标的 %q 不存在", ErrSymbol, sym)
	}
	return st, nil
}

// CheckOrder 只校验涨跌停（中断期间与时段外照常接受）。
func (g *Gate) CheckOrder(now int64, sym string, price int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, err := g.begin(now, sym)
	if err != nil {
		return err
	}
	g.maxNow = now
	if !st.sym.InLimit(price) {
		return fmt.Errorf("%w：价格 %d 不在 [%d,%d]", ErrLimit, price, st.sym.Dn(), st.sym.Up())
	}
	return nil
}

// overOrHalt 处理超带：尾盘或 Hmax 用尽则拒绝，否则进入中断。
func (st *symState) overOrHalt(now int64, reason Reason, cause error) (*TradeResult, error) {
	p := st.sym.P()
	tailStart := p.Close - p.C
	if now >= tailStart || st.ph.HaltCount() >= p.Hmax {
		return nil, cause
	}
	he := now + p.T
	if he > tailStart {
		he = tailStart
	}
	st.ph.Enter(he)
	return &TradeResult{Halt: &HaltResult{Halted: true, Reason: reason, HE: he}}, nil
}

// Trade 提交一笔拟成交。
func (g *Gate) Trade(now int64, sym string, price int64) (*TradeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, err := g.begin(now, sym)
	if err != nil {
		return nil, err
	}
	p := st.sym.P()
	if now < p.Open || now >= p.Close {
		return nil, fmt.Errorf("%w：now=%d 不在交易时段 [%d,%d)", ErrPhase, now, p.Open, p.Close)
	}
	if st.ph.State() == phase.Halt {
		return nil, fmt.Errorf("%w：中断至 %d", ErrHalted, st.ph.HE())
	}
	if !st.sym.InLimit(price) {
		return nil, fmt.Errorf("%w：价格 %d 不在 [%d,%d]", ErrLimit, price, st.sym.Dn(), st.sym.Up())
	}
	if band.Over(price, st.book.Rs(), p.Ds) {
		res, err := st.overOrHalt(now, ReasonStatic, staticErr())
		if err != nil {
			return nil, err
		}
		g.maxNow = now
		return res, nil
	}
	rd := rdPeek(st.book, now, p.W)
	if band.Over(price, rd, p.Dd) {
		res, err := st.overOrHalt(now, ReasonDynamic, dynamicErr())
		if err != nil {
			return nil, err
		}
		g.maxNow = now
		return res, nil
	}
	st.book.Drain(now, p.W)
	st.book.Append(band.Tick{Time: now, Price: price})
	g.maxNow = now
	return &TradeResult{Filled: true}, nil
}

// Resume 在中断中提交恢复价。
func (g *Gate) Resume(now int64, sym string, price int64) (*ResumeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st, err := g.begin(now, sym)
	if err != nil {
		return nil, err
	}
	p := st.sym.P()
	if now < p.Open || now >= p.Close {
		return nil, fmt.Errorf("%w：now=%d 不在交易时段 [%d,%d)", ErrPhase, now, p.Open, p.Close)
	}
	if st.ph.State() != phase.Halt {
		return nil, fmt.Errorf("%w：当前未处于中断", ErrPhase)
	}
	if now < st.ph.HE() {
		return nil, fmt.Errorf("%w：now=%d 早于中断结束时刻 %d", ErrPhase, now, st.ph.HE())
	}
	if !st.sym.InLimit(price) {
		return nil, fmt.Errorf("%w：价格 %d 不在 [%d,%d]", ErrLimit, price, st.sym.Dn(), st.sym.Up())
	}
	tailStart := p.Close - p.C
	if band.Over(price, st.book.Rs(), p.De) && st.ph.ExtCount() < p.X && st.ph.HE() < tailStart {
		he := st.ph.HE() + p.T
		if he > tailStart {
			he = tailStart
		}
		st.ph.Extend(he)
		g.maxNow = now
		return &ResumeResult{Extended: true, HE: he}, nil
	}
	st.ph.Resume()
	st.book.Reset(price, band.Tick{Time: now, Price: price})
	g.maxNow = now
	return &ResumeResult{Filled: true}, nil
}
