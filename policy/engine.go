package policy

import "sync"

// Engine 保单引擎：登记保单并对外提供全部入口。
//
// 所有入口由一把互斥锁串行化，并发调用等价于某个串行顺序；同一操作序列
// 重放得到完全相同的状态轨迹。每个入口按固定次序拒绝：
// 参数非法 > 保单不存在 > 时钟回退 > 状态不允许 > 补缴不足 > 超额还款。
// 可能被拒的操作先在保单副本上推进结算，拒绝即丢弃副本，不留下任何痕迹。
type Engine struct {
	mu       sync.Mutex
	policies map[uint64]*Policy
	nextID   uint64
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[uint64]*Policy)}
}

// Register 登记保单：首期保费视为已缴，当前时刻为生效日。返回保单号。
func (e *Engine) Register(cfg Config) (uint64, error) {
	if err := cfg.validate(); err != nil {
		return 0, err
	}
	cfg.CashValues = append([]int64(nil), cfg.CashValues...)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextID++
	e.policies[e.nextID] = newPolicy(cfg)
	return e.nextID, nil
}

// Advance 将保单时刻推进到 day，依次结算其间全部应缴、宽限期满、
// 垫交、中止与终止事件。
func (e *Engine) Advance(id uint64, day int64) error {
	if day < 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return err
	}
	if day < p.now {
		return ErrClockBackward
	}
	p.settle(day)
	return nil
}

// PayPremium 主动缴费：金额须恰好为整数期保费，多缴期数顺延应缴日。
func (e *Engine) PayPremium(id uint64, day, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return err
	}
	if amount%p.cfg.PremiumCents != 0 {
		return ErrInvalidParam
	}
	if day < p.now {
		return ErrClockBackward
	}
	return e.transact(p, day, func(cp *Policy) error { return cp.payPremium(amount) })
}

// RepayLoan 主动还款：仅有效状态可还；先还利息后还本金；
// 还款额超过本息合计报「超额还款」。
func (e *Engine) RepayLoan(id uint64, day, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return err
	}
	if day < p.now {
		return ErrClockBackward
	}
	return e.transact(p, day, func(cp *Policy) error { return cp.repayLoan(amount) })
}

// Reinstate 申请复效：仅中止状态可申请；须一次性补缴中止期间全部到期
// 保费与已有借款本息，不足报「补缴不足」且不留任何部分款项。
func (e *Engine) Reinstate(id uint64, day, amount int64) error {
	if day < 0 || amount < 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return err
	}
	if day < p.now {
		return ErrClockBackward
	}
	return e.transact(p, day, func(cp *Policy) error { return cp.reinstate(amount) })
}

// Claim 出险判定：以出险日结算后所处状态为准。
func (e *Engine) Claim(id uint64, day int64) (ClaimResult, error) {
	if day < 0 {
		return ClaimResult{}, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return ClaimResult{}, err
	}
	if day < p.now {
		return ClaimResult{}, ErrClockBackward
	}
	p.settle(day)
	return p.adjudicate(), nil
}

// Query 查询推进到 day 后的保单快照（状态、欠费金额、借款账等）。
func (e *Engine) Query(id uint64, day int64) (Snapshot, error) {
	if day < 0 {
		return Snapshot{}, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.lookup(id)
	if err != nil {
		return Snapshot{}, err
	}
	if day < p.now {
		return Snapshot{}, ErrClockBackward
	}
	p.settle(day)
	return p.snapshot(), nil
}

func (e *Engine) lookup(id uint64) (*Policy, error) {
	p, ok := e.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	return p, nil
}

// transact 在保单副本上推进并执行操作；操作被拒绝时丢弃副本，
// 保单状态、借款账、应缴日与当前时刻均不变。
func (e *Engine) transact(p *Policy, day int64, op func(*Policy) error) error {
	cp := *p
	cp.settle(day)
	if err := op(&cp); err != nil {
		return err
	}
	*p = cp
	return nil
}
