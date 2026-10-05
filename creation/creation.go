// Package creation 实现 ETF 实物申购赎回处理器：申购、赎回与份额、
// 成分券、现金过户，以及当日额度、份额锁定与日终退补。
package creation

import (
	"errors"
	"fmt"
	"math/big"
	"sync"

	"ontology/basket"
	"ontology/subst"
)

// 错误哨兵，均可用 errors.Is 判定。
var (
	ErrClock     = errors.New("creation: clock rollback")
	ErrNotExist  = errors.New("creation: not exist")
	ErrQuota     = errors.New("creation: daily quota exceeded")
	ErrComponent = errors.New("creation: component insufficient")
	ErrRatio     = errors.New("creation: substitution ratio exceeded")
	ErrFund      = errors.New("creation: cash insufficient")
	ErrShares    = errors.New("creation: shares insufficient")
	ErrInventory = errors.New("creation: fund inventory insufficient")
)

// SymbolError 在成分券不足 / 库存不足时携带触发项（字节序最小的一项）。
type SymbolError struct {
	Kind error
	Sym  string
}

func (e *SymbolError) Error() string { return fmt.Sprintf("%v: %s", e.Kind, e.Sym) }
func (e *SymbolError) Unwrap() error { return e.Kind }

const (
	maxNow   = 1_000_000_000_000
	maxAmt   = 1_000_000_000_000
	maxPrice = 1_000_000
	maxUnits = 1_000_000
)

type account struct {
	cash   int64
	holds  map[string]int64
	shares int64
	locked int64 // 当日申购所得份额，当日不可赎回
	debt   int64 // 日终补收不足的欠款
}

// gap 记录一笔申购中某个 A 类缺口，供日终退补。
type gap struct {
	sym string
	d   int64
	pre int64
}

type createRec struct {
	acct string
	gaps []gap // 按 sym 字节序
}

// Processor 为申赎处理器，所有方法可并发调用，等价于某个串行顺序。
type Processor struct {
	mu       sync.Mutex
	b        *basket.Basket
	maxNow   int64
	hasNow   bool
	prices   map[string]int64
	accts    map[string]*account
	fundInv  map[string]int64
	fundCash int64 // 基金现金，可以为负
	ids      map[string]struct{}
	dayUnits int64
	pending  []createRec // 当日被接受的申购，按接受次序
	touched  int         // 最近一次 Create 触碰的账户持券记录数
}

// New 校验清单并构造处理器。
func New(items []basket.Item, e, rmax, q int64) (*Processor, error) {
	b, err := basket.New(items, e, rmax, q)
	if err != nil {
		return nil, err
	}
	return &Processor{
		b:       b,
		prices:  make(map[string]int64),
		accts:   make(map[string]*account),
		fundInv: make(map[string]int64),
		ids:     make(map[string]struct{}),
	}, nil
}

// checkNow 校验 now 的范围与时钟单调性。
func (p *Processor) checkNow(now int64) error {
	if now < 0 || now > maxNow {
		return fmt.Errorf("%w: now %d", basket.ErrParam, now)
	}
	if p.hasNow && now < p.maxNow {
		return fmt.Errorf("%w: now %d < %d", ErrClock, now, p.maxNow)
	}
	return nil
}

func (p *Processor) advance(now int64) {
	p.maxNow = now
	p.hasNow = true
}

// Credit 给账户增加持券并（在首次时）建立账户。
func (p *Processor) Credit(now int64, acct, sym string, n int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if acct == "" || sym == "" || n < 1 || n > maxAmt {
		return fmt.Errorf("%w: credit %q %q %d", basket.ErrParam, acct, sym, n)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	a := p.accts[acct]
	if a == nil {
		a = &account{holds: make(map[string]int64)}
		p.accts[acct] = a
	}
	a.holds[sym] += n
	p.advance(now)
	return nil
}

// CreditCash 给账户增加现金并（在首次时）建立账户。
func (p *Processor) CreditCash(now int64, acct string, amt int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if acct == "" || amt < 1 || amt > maxAmt {
		return fmt.Errorf("%w: credit cash %q %d", basket.ErrParam, acct, amt)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	a := p.accts[acct]
	if a == nil {
		a = &account{holds: make(map[string]int64)}
		p.accts[acct] = a
	}
	a.cash += amt
	p.advance(now)
	return nil
}

// SetPrice 设置标的现价。
func (p *Processor) SetPrice(now int64, sym string, price int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sym == "" || price < 1 || price > maxPrice {
		return fmt.Errorf("%w: set price %q %d", basket.ErrParam, sym, price)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	p.prices[sym] = price
	p.advance(now)
	return nil
}

// Create 申购 n 个单位。校验次序：参数 > 时钟 > 不存在/id 重复 >
// 当日额度 > 成分券不足 > 替代比例超限 > 资金不足。全有或全无。
func (p *Processor) Create(now int64, id, acct string, n int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" || acct == "" || n < 1 || n > maxUnits {
		return fmt.Errorf("%w: create %q %q %d", basket.ErrParam, id, acct, n)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	a := p.accts[acct]
	if a == nil {
		return fmt.Errorf("%w: account %q", ErrNotExist, acct)
	}
	for _, it := range p.b.Items {
		if it.Flag == basket.FlagM {
			continue
		}
		if _, ok := p.prices[it.Sym]; !ok {
			return fmt.Errorf("%w: no price for %q", ErrNotExist, it.Sym)
		}
	}
	if _, dup := p.ids[id]; dup {
		return fmt.Errorf("%w: duplicate id %q", ErrNotExist, id)
	}
	if p.dayUnits+n > p.b.Q {
		return fmt.Errorf("%w: %d+%d > %d", ErrQuota, p.dayUnits, n, p.b.Q)
	}
	for _, it := range p.b.Items {
		if it.Flag == basket.FlagN && a.holds[it.Sym] < it.Qty*n {
			return &SymbolError{Kind: ErrComponent, Sym: it.Sym}
		}
	}
	// 替代比例与应付净额（中间量用 big.Int）。
	x := new(big.Int) // 替代额
	y := new(big.Int) // 篮子市值
	net := new(big.Int)
	rec := createRec{acct: acct}
	for _, it := range p.b.Items {
		switch it.Flag {
		case basket.FlagN:
			y.Add(y, new(big.Int).Mul(big.NewInt(it.Qty*n), big.NewInt(p.prices[it.Sym])))
		case basket.FlagA:
			need := it.Qty * n
			give := a.holds[it.Sym]
			if give > need {
				give = need
			}
			d := need - give
			pr := p.prices[it.Sym]
			y.Add(y, new(big.Int).Mul(big.NewInt(need), big.NewInt(pr)))
			if d > 0 {
				x.Add(x, new(big.Int).Mul(big.NewInt(d), big.NewInt(pr)))
				pre := subst.Precollect(d, pr, it.Prem)
				net.Add(net, big.NewInt(pre))
				rec.gaps = append(rec.gaps, gap{sym: it.Sym, d: d, pre: pre})
			}
		case basket.FlagM:
			fv := new(big.Int).Mul(big.NewInt(it.Fixed), big.NewInt(n))
			x.Add(x, fv)
			y.Add(y, fv)
			net.Add(net, fv)
		}
	}
	if !subst.RatioOK(x, y, p.b.Rmax) {
		return fmt.Errorf("%w: X=%s Y=%s Rmax=%d", ErrRatio, x, y, p.b.Rmax)
	}
	net.Add(net, new(big.Int).Mul(big.NewInt(p.b.E), big.NewInt(n)))
	if net.Sign() > 0 && net.Cmp(big.NewInt(a.cash)) > 0 {
		return fmt.Errorf("%w: need %s have %d", ErrFund, net, a.cash)
	}
	// 落账：券入基金、现金入基金、账户得份额。
	p.touched = 0
	for _, it := range p.b.Items {
		switch it.Flag {
		case basket.FlagN:
			a.holds[it.Sym] -= it.Qty * n
			p.fundInv[it.Sym] += it.Qty * n
			p.touched++
		case basket.FlagA:
			need := it.Qty * n
			give := a.holds[it.Sym]
			if give > need {
				give = need
			}
			a.holds[it.Sym] -= give
			p.fundInv[it.Sym] += give
			p.touched++
		}
	}
	net64 := net.Int64() // 净额>0 时净额<=cash，净额<=0 时下界为 E*n，均不溢出
	a.cash -= net64
	p.fundCash += net64
	a.shares += n
	a.locked += n
	p.dayUnits += n
	p.ids[id] = struct{}{}
	p.pending = append(p.pending, rec)
	p.advance(now)
	return nil
}

// Redeem 赎回 n 个单位。校验次序：参数 > 时钟 > 不存在 >
// 份额不足 > 库存不足 > 资金不足。全有或全无。
func (p *Processor) Redeem(now int64, acct string, n int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if acct == "" || n < 1 || n > maxUnits {
		return fmt.Errorf("%w: redeem %q %d", basket.ErrParam, acct, n)
	}
	if err := p.checkNow(now); err != nil {
		return err
	}
	a := p.accts[acct]
	if a == nil {
		return fmt.Errorf("%w: account %q", ErrNotExist, acct)
	}
	for _, it := range p.b.Items {
		if it.Flag == basket.FlagA {
			if _, ok := p.prices[it.Sym]; !ok {
				return fmt.Errorf("%w: no price for %q", ErrNotExist, it.Sym)
			}
		}
	}
	if a.shares-a.locked < n {
		return fmt.Errorf("%w: redeemable %d < %d", ErrShares, a.shares-a.locked, n)
	}
	for _, it := range p.b.Items {
		if it.Flag == basket.FlagN && p.fundInv[it.Sym] < it.Qty*n {
			return &SymbolError{Kind: ErrInventory, Sym: it.Sym}
		}
	}
	net := new(big.Int) // 账户净收入
	for _, it := range p.b.Items {
		switch it.Flag {
		case basket.FlagA:
			need := it.Qty * n
			give := p.fundInv[it.Sym]
			if give > need {
				give = need
			}
			if d := need - give; d > 0 {
				net.Add(net, big.NewInt(subst.RedeemPay(d, p.prices[it.Sym], it.Prem)))
			}
		case basket.FlagM:
			net.Add(net, new(big.Int).Mul(big.NewInt(it.Fixed), big.NewInt(n)))
		}
	}
	net.Add(net, new(big.Int).Mul(big.NewInt(p.b.E), big.NewInt(n)))
	if net.Sign() < 0 && new(big.Int).Neg(net).Cmp(big.NewInt(a.cash)) > 0 {
		return fmt.Errorf("%w: need %s have %d", ErrFund, net, a.cash)
	}
	for _, it := range p.b.Items {
		switch it.Flag {
		case basket.FlagN:
			p.fundInv[it.Sym] -= it.Qty * n
			a.holds[it.Sym] += it.Qty * n
		case basket.FlagA:
			need := it.Qty * n
			give := p.fundInv[it.Sym]
			if give > need {
				give = need
			}
			p.fundInv[it.Sym] -= give
			a.holds[it.Sym] += give
		}
	}
	net64 := net.Int64()
	a.cash += net64
	p.fundCash -= net64
	a.shares -= n
	p.advance(now)
	return nil
}

// EndOfDay 按当日申购接受次序、每笔内按 sym 字节序退补，
// 随后清零当日申购单位数与当日申购份额锁定。
func (p *Processor) EndOfDay(now int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.checkNow(now); err != nil {
		return err
	}
	for _, rec := range p.pending {
		a := p.accts[rec.acct]
		for _, g := range rec.gaps {
			cost := g.d * p.prices[g.sym]
			diff := subst.Settle(g.pre, cost)
			switch {
			case diff > 0: // 退给账户
				a.cash += diff
				p.fundCash -= diff
			case diff < 0: // 向账户补收，不足扣到 0 并记欠款
				take := a.cash
				if take > -diff {
					take = -diff
				}
				a.cash -= take
				p.fundCash += take
				a.debt += -diff - take
			}
		}
		a.locked = 0
	}
	p.pending = nil
	p.dayUnits = 0
	p.advance(now)
	return nil
}

// 以下为只读查询，供对账与测试使用。

// Cash 返回账户现金（账户不存在时为 0）。
func (p *Processor) Cash(acct string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a := p.accts[acct]; a != nil {
		return a.cash
	}
	return 0
}

// Holding 返回账户持券数量。
func (p *Processor) Holding(acct, sym string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a := p.accts[acct]; a != nil {
		return a.holds[sym]
	}
	return 0
}

// Shares 返回账户持有份额。
func (p *Processor) Shares(acct string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a := p.accts[acct]; a != nil {
		return a.shares
	}
	return 0
}

// Debt 返回账户欠款。
func (p *Processor) Debt(acct string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	if a := p.accts[acct]; a != nil {
		return a.debt
	}
	return 0
}

// FundCash 返回基金现金（可以为负）。
func (p *Processor) FundCash() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fundCash
}

// FundInv 返回基金某成分券库存。
func (p *Processor) FundInv(sym string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fundInv[sym]
}
