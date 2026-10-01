// Package fee implements a tiered progressive fee calculator over
// monthly cumulative trade amounts.
package fee

import (
	"errors"
	"sync"
)

const (
	maxTradeAmt = int64(1_000_000_000)
	maxCumAmt   = int64(100_000_000_000_000)
	rateBase    = int64(10_000)
)

var (
	// ErrInvalidParam 构造参数、账户名、成交编号或成交金额非法。
	ErrInvalidParam = errors.New("fee: invalid parameter")
	// ErrDuplicateTID 成交编号已存在（含已撤销的编号）。
	ErrDuplicateTID = errors.New("fee: duplicate trade id")
	// ErrCumulativeLimit 登记后账户当前账期累计额（含 C0）超过 10^14。
	ErrCumulativeLimit = errors.New("fee: cumulative amount exceeds limit")
	// ErrTradeNotFound 成交编号不存在。
	ErrTradeNotFound = errors.New("fee: trade not found")
	// ErrAlreadyCancelled 成交已被撤销。
	ErrAlreadyCancelled = errors.New("fee: trade already cancelled")
	// ErrPeriodClosed 成交不属于当前账期，账期已结不可撤销。
	ErrPeriodClosed = errors.New("fee: trade belongs to a closed period")
)

// FeeChange 描述撤销成交后某笔后续成交的费用变化。
type FeeChange struct {
	TID    string
	OldFee int64
	NewFee int64
}

type trade struct {
	tid       string
	amt       int64
	fee       int64
	cancelled bool
}

type tidRecord struct {
	acct      string
	period    int
	cancelled bool
}

type account struct {
	c0     int64
	sum    int64
	trades []*trade
}

// Calculator 是并发安全的阶梯费率计费器。
type Calculator struct {
	mu         sync.Mutex
	thresholds []int64
	rates      []int64
	cap        int64
	rho        int64
	period     int
	accounts   map[string]*account
	tids       map[string]*tidRecord
}

// NewCalculator 构造计费器；参数非法时整体拒绝并返回 ErrInvalidParam。
func NewCalculator(thresholds []int64, rates []int64, cap int64, rho int64) (*Calculator, error) {
	if len(thresholds) < 1 || len(rates) != len(thresholds)+1 {
		return nil, ErrInvalidParam
	}
	prev := int64(0)
	for _, t := range thresholds {
		if t <= prev || t > maxCumAmt {
			return nil, ErrInvalidParam
		}
		prev = t
	}
	for _, r := range rates {
		if r < 0 || r > rateBase {
			return nil, ErrInvalidParam
		}
	}
	if cap < 0 || cap > maxCumAmt || rho < 0 || rho > rateBase {
		return nil, ErrInvalidParam
	}
	th := make([]int64, len(thresholds))
	copy(th, thresholds)
	ra := make([]int64, len(rates))
	copy(ra, rates)
	return &Calculator{
		thresholds: th,
		rates:      ra,
		cap:        cap,
		rho:        rho,
		accounts:   make(map[string]*account),
		tids:       make(map[string]*tidRecord),
	}, nil
}

// feeOf 返回未封顶累计费用 F(c)：各段 floor(seg_k(c)*r_k/10000) 之和。
func (c *Calculator) feeOf(cum int64) int64 {
	var total int64
	for k, r := range c.rates {
		lo := int64(0)
		if k > 0 {
			lo = c.thresholds[k-1]
		}
		if cum <= lo {
			break
		}
		seg := cum - lo
		if k < len(c.thresholds) {
			if width := c.thresholds[k] - lo; seg > width {
				seg = width
			}
		}
		total += seg * r / rateBase
	}
	return total
}

// phi 返回封顶后的累计费用 Φ(c) = min(F(c), CAP)。
func (c *Calculator) phi(cum int64) int64 {
	if f := c.feeOf(cum); f < c.cap {
		return f
	}
	return c.cap
}

// Trade 登记一笔成交并返回其费用。
func (c *Calculator) Trade(acct, tid string, amt int64) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if acct == "" || tid == "" || amt < 1 || amt > maxTradeAmt {
		return 0, ErrInvalidParam
	}
	if _, ok := c.tids[tid]; ok {
		return 0, ErrDuplicateTID
	}
	a := c.accounts[acct]
	if a == nil {
		a = &account{}
		c.accounts[acct] = a
	}
	cum := a.c0 + a.sum
	if cum+amt > maxCumAmt {
		return 0, ErrCumulativeLimit
	}
	fee := c.phi(cum+amt) - c.phi(cum)
	a.trades = append(a.trades, &trade{tid: tid, amt: amt, fee: fee})
	a.sum += amt
	c.tids[tid] = &tidRecord{acct: acct, period: c.period}
	return fee, nil
}

// Cancel 撤销当前账期内仍有效的成交，返回被撤销成交的旧费用与费用变更列表。
func (c *Calculator) Cancel(tid string) (int64, []FeeChange, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.tids[tid]
	if !ok {
		return 0, nil, ErrTradeNotFound
	}
	if rec.cancelled {
		return 0, nil, ErrAlreadyCancelled
	}
	if rec.period != c.period {
		return 0, nil, ErrPeriodClosed
	}
	rec.cancelled = true
	a := c.accounts[rec.acct]
	var oldFee int64
	cancelledIdx := -1
	for i, tr := range a.trades {
		if tr.tid == tid {
			tr.cancelled = true
			oldFee = tr.fee
			cancelledIdx = i
			a.sum -= tr.amt
			break
		}
	}
	// 从起点 C0 按原次序重算被撤销成交之后的有效成交费用。
	cum := a.c0
	for _, tr := range a.trades[:cancelledIdx+1] {
		if !tr.cancelled {
			cum += tr.amt
		}
	}
	var changes []FeeChange
	for _, tr := range a.trades[cancelledIdx+1:] {
		if tr.cancelled {
			continue
		}
		newFee := c.phi(cum+tr.amt) - c.phi(cum)
		cum += tr.amt
		if newFee != tr.fee {
			changes = append(changes, FeeChange{TID: tr.tid, OldFee: tr.fee, NewFee: newFee})
			tr.fee = newFee
		}
	}
	return oldFee, changes, nil
}

// NextPeriod 账期加 1，对所有账户按结转比例原子结转起点累计额。
func (c *Calculator) NextPeriod() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.accounts {
		a.c0 = (a.c0 + a.sum) * c.rho / rateBase
		a.sum = 0
		a.trades = nil
	}
	c.period++
}

// Period 返回当前账期编号（从 0 开始）。
func (c *Calculator) Period() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.period
}

// C0 返回账户当前账期的起点累计额；账户未出现过时 ok 为 false。
func (c *Calculator) C0(acct string) (c0 int64, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, ok := c.accounts[acct]
	if !ok {
		return 0, false
	}
	return a.c0, true
}

// TradeRecord 是账户当前账期内一笔成交的快照。
type TradeRecord struct {
	TID       string
	Amt       int64
	Fee       int64
	Cancelled bool
}

// Trades 返回账户当前账期内全部成交（含已撤销），按登记次序排列。
func (c *Calculator) Trades(acct string) []TradeRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accounts[acct]
	if a == nil {
		return nil
	}
	out := make([]TradeRecord, len(a.trades))
	for i, tr := range a.trades {
		out[i] = TradeRecord{TID: tr.tid, Amt: tr.amt, Fee: tr.fee, Cancelled: tr.cancelled}
	}
	return out
}
