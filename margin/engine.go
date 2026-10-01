// Package margin 实现逐仓合约的保证金与强制平仓引擎。
package margin

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// Direction 为开仓方向。
type Direction int

const (
	// Long 多头，持仓量与成本累加正值。
	Long Direction = 1
	// Short 空头，持仓量与成本累加负值。
	Short Direction = -1
)

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	ErrInvalidArgument   = errors.New("margin: invalid argument")
	ErrAccountNotFound   = errors.New("margin: account not found")
	ErrPositionConflict  = errors.New("margin: position conflict")
	ErrNoPosition        = errors.New("margin: no position")
	ErrInsufficientFunds = errors.New("margin: insufficient funds")
)

// Account 是某一时刻对外可见的账户快照。
type Account struct {
	M int64
	Q int64
	C int64
}

// ADLItem 是一次强平中对单个对手方账户的减仓明细。
type ADLItem struct {
	Account string
	Take    int64
}

// LiquidationItem 是 Mark 返回清单中的一项。
type LiquidationItem struct {
	Account  string
	E        int64
	Fine     int64
	Absorbed int64
	ADL      []ADLItem
	BadDebt  int64
}

// Engine 是并发安全的逐仓保证金与强平引擎。
type Engine struct {
	mu       sync.Mutex
	iRate    int // 初始保证金率（万分比）
	mRate    int // 维持保证金率（万分比）
	fRate    int // 强平费率（万分比）
	zFund    int64
	bDebt    int64
	mark     int64
	accounts map[string]*account
}

type account struct {
	m int64
	q int64
	c int64
}

const (
	basis      = 10000
	maxRate    = 10000
	maxPrice   = int64(1_000_000)
	maxQty     = int64(1_000_000)
	maxDeposit = int64(1_000_000_000_000)
	maxMargin  = int64(1_000_000_000_000_000)
)

// New 创建引擎；参数不合法返回 ErrInvalidArgument。
func New(initial, maintenance, fee int) (*Engine, error) {
	if maintenance < 1 || initial <= maintenance || initial > maxRate || fee < 0 || fee > maxRate {
		return nil, ErrInvalidArgument
	}
	return &Engine{
		iRate:    initial,
		mRate:    maintenance,
		fRate:    fee,
		accounts: make(map[string]*account),
	}, nil
}

// Deposit 存入保证金（不存在则先创建账户）。
func (e *Engine) Deposit(a string, x int64) error {
	if a == "" || x < 1 || x > maxDeposit {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		if x > maxMargin {
			return ErrInvalidArgument
		}
		e.accounts[a] = &account{m: x}
		return nil
	}
	if acc.m > maxMargin-x {
		return ErrInvalidArgument
	}
	acc.m += x
	return nil
}

// Open 开仓或同方向加仓。
func (e *Engine) Open(a string, dir Direction, n, p int64) error {
	if a == "" || (dir != Long && dir != Short) || n < 1 || n > maxQty || p < 1 || p > maxPrice {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return ErrAccountNotFound
	}
	signedN := int64(dir) * n
	if acc.q != 0 && sign(acc.q) != sign(signedN) {
		return ErrPositionConflict
	}
	newQ := acc.q + signedN
	newC := acc.c + signedN*p
	if abs(newQ) > maxQty {
		return ErrInvalidArgument
	}
	required := ceilDiv(abs(newC)*int64(e.iRate), basis)
	if acc.m < required {
		return ErrInsufficientFunds
	}
	acc.q = newQ
	acc.c = newC
	return nil
}

// Close 以价格 p 全部平仓，不触发自动减仓。
func (e *Engine) Close(a string, p int64) error {
	if a == "" || p < 1 || p > maxPrice {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return ErrAccountNotFound
	}
	if acc.q == 0 {
		return ErrNoPosition
	}
	eq := acc.m + acc.q*p - acc.c
	if eq >= 0 {
		acc.m = eq
	} else {
		deficit := -eq
		acc.m = 0
		absorbed := minInt64(e.zFund, deficit)
		e.zFund -= absorbed
		e.bDebt += deficit - absorbed
	}
	acc.q = 0
	acc.c = 0
	return nil
}

// Withdraw 在无仓位时提取保证金。
func (e *Engine) Withdraw(a string, x int64) error {
	if a == "" || x < 1 || x > maxDeposit {
		return ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return ErrAccountNotFound
	}
	if acc.q != 0 {
		return ErrPositionConflict
	}
	if x > acc.m {
		return ErrInsufficientFunds
	}
	acc.m -= x
	return nil
}

// Mark 原子地推进标记价格并执行强平。
func (e *Engine) Mark(P int64) ([]LiquidationItem, error) {
	if P < 1 || P > maxPrice {
		return nil, ErrInvalidArgument
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	// 第一步：按 Mark 开始时的状态一次性确定 Λ。
	type target struct {
		id string
		eq int64
	}
	var lambda []target
	inLambda := make(map[string]struct{})
	for id, acc := range e.accounts {
		if acc.q == 0 {
			continue
		}
		eq := acc.m + acc.q*P - acc.c
		maint := ceilDiv(abs(acc.q)*P*int64(e.mRate), basis)
		if eq < maint {
			lambda = append(lambda, target{id: id, eq: eq})
			inLambda[id] = struct{}{}
		}
	}
	sort.Slice(lambda, func(i, j int) bool { return lambda[i].id < lambda[j].id })

	// 第二步：按账户编号字节序逐个执行，Z 与对手方状态逐步演化。
	items := make([]LiquidationItem, 0, len(lambda))
	for _, t := range lambda {
		acc := e.accounts[t.id]
		item := LiquidationItem{Account: t.id, E: t.eq, ADL: []ADLItem{}}
		if t.eq >= 0 {
			fine := minInt64(t.eq, floorDiv(abs(acc.q)*P*int64(e.fRate), basis))
			acc.m = t.eq - fine
			e.zFund += fine
			item.Fine = fine
		} else {
			acc.m = 0
			deficit := -t.eq
			absorbed := minInt64(e.zFund, deficit)
			e.zFund -= absorbed
			item.Absorbed = absorbed
			remaining := deficit - absorbed
			if remaining > 0 {
				item.ADL, remaining = e.runADL(acc.q, P, remaining, inLambda)
				if remaining > 0 {
					e.bDebt += remaining
					item.BadDebt = remaining
				}
			}
		}
		acc.q = 0
		acc.c = 0
		items = append(items, item)
	}

	// 第三步：记录标记价格并返回执行清单。
	e.mark = P
	return items, nil
}

// Balance 查询账户保证金。
func (e *Engine) Balance(a string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return 0, ErrAccountNotFound
	}
	return acc.m, nil
}

// Position 查询账户带符号持仓量与成本。
func (e *Engine) Position(a string) (q, c int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return 0, 0, ErrAccountNotFound
	}
	return acc.q, acc.c, nil
}

// AccountSnapshot 查询账户完整快照。
func (e *Engine) AccountSnapshot(a string) (Account, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	acc := e.accounts[a]
	if acc == nil {
		return Account{}, ErrAccountNotFound
	}
	return Account{M: acc.m, Q: acc.q, C: acc.c}, nil
}

// InsuranceFund 返回保险基金余额。
func (e *Engine) InsuranceFund() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.zFund
}

// BadDebt 返回坏账累计。
func (e *Engine) BadDebt() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bDebt
}

// MarkPrice 返回最近一次成功 Mark 的标记价格（无则为 0）。
func (e *Engine) MarkPrice() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mark
}

// runADL 对单个亏空账户执行自动减仓，返回减仓明细与未覆盖余额。
// 排序依据的 pnl 取调用当时（可能已被前一亏空账户改写过）的 C。
func (e *Engine) runADL(liquidatedQ, P, remaining int64, inLambda map[string]struct{}) ([]ADLItem, int64) {
	type cand struct {
		id   string
		acc  *account
		pnl  int64
		absC int64
	}
	var cands []cand
	for id, acc := range e.accounts {
		if acc.q == 0 || sign(acc.q) == sign(liquidatedQ) {
			continue
		}
		if _, ok := inLambda[id]; ok {
			continue
		}
		pnl := acc.q*P - acc.c
		if pnl <= 0 {
			continue
		}
		cands = append(cands, cand{id: id, acc: acc, pnl: pnl, absC: abs(acc.c)})
	}
	sort.Slice(cands, func(i, j int) bool {
		// pnl_i/|C_i| 与 pnl_j/|C_j| 交叉相乘比较，乘积可达 10^24，用大整数。
		lhs := new(big.Int).Mul(big.NewInt(cands[i].pnl), big.NewInt(cands[j].absC))
		rhs := new(big.Int).Mul(big.NewInt(cands[j].pnl), big.NewInt(cands[i].absC))
		if cmp := lhs.Cmp(rhs); cmp != 0 {
			return cmp > 0
		}
		return cands[i].id < cands[j].id
	})
	items := make([]ADLItem, 0)
	for _, cd := range cands {
		if remaining <= 0 {
			break
		}
		take := minInt64(remaining, cd.pnl)
		if take <= 0 {
			continue
		}
		cd.acc.c += take // 权益恰好减少 take，M 与 q 不变；take ≤ pnl 保证 C 不翻号
		remaining -= take
		items = append(items, ADLItem{Account: cd.id, Take: take})
	}
	return items, remaining
}

func sign(v int64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	default:
		return 0
	}
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

func ceilDiv(a, b int64) int64  { return (a + b - 1) / b }
func floorDiv(a, b int64) int64 { return a / b }
func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
