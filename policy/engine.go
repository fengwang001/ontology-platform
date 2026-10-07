package policy

import "sync"

// Engine 保单引擎。所有入口共用一把互斥锁串行化，
// 并发调用结果等价于某个串行顺序；相同操作序列重放得到相同轨迹。
//
// 每个操作的统一流程（拒绝次序即下列次序，任一拒绝都不留痕）：
//  1. 参数非法：与保单无关的参数校验（day<0、金额<=0 等）；
//  2. 保单不存在；
//  3. 参数非法：依赖保单参数的校验（缴费非整数期等）；
//  4. 时钟回退：day 小于保单当前时刻；
//  5. 在保单副本上把事件结算到 day，再做状态类校验（状态不允许）；
//  6. 金额类校验（补缴不足、超额还款）；
//  7. 全部通过才提交副本，否则原保单（含当前时刻）完全不变。
type Engine struct {
	mu       sync.Mutex
	policies map[string]*Policy
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[string]*Policy)}
}

// Register 登记保单，首期保费视为已缴，当前时刻为生效日。
func (e *Engine) Register(id string, cfg Config) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.policies[id]; ok {
		return ErrInvalidParam
	}
	e.policies[id] = newPolicy(cfg)
	return nil
}

// get 存在性 + 时钟校验，调用方须持锁。
func (e *Engine) get(id string, day int) (*Policy, error) {
	p, ok := e.policies[id]
	if !ok {
		return nil, ErrPolicyNotFound
	}
	if day < p.day {
		return nil, ErrClockRollback
	}
	return p, nil
}

// Advance 只推进时刻并结算其间全部事件。
func (e *Engine) Advance(id string, day int) error {
	if day < 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.get(id, day)
	if err != nil {
		return err
	}
	p.settle(day)
	return nil
}

// Query 查询 day 时刻的保单快照（先结算到 day）。
func (e *Engine) Query(id string, day int) (Snapshot, error) {
	if day < 0 {
		return Snapshot{}, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.get(id, day)
	if err != nil {
		return Snapshot{}, err
	}
	p.settle(day)
	return p.snapshot(), nil
}

// PayPremium 主动缴费：金额必须恰好为整数期保费；有效或宽限中可缴。
func (e *Engine) PayPremium(id string, day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.policies[id]
	if !ok {
		return ErrPolicyNotFound
	}
	if amount%p.cfg.Premium != 0 {
		return ErrInvalidParam
	}
	if day < p.day {
		return ErrClockRollback
	}
	cp := *p
	cp.settle(day)
	if cp.state != StateActive && cp.state != StateGrace {
		return ErrStateNotAllowed
	}
	cp.paidCount += int(amount / cp.cfg.Premium)
	cp.state = StateActive // 缴清当期欠费，多缴期数顺延应缴日
	cp.settle(day)         // 应缴日仍可能已过去（宽限天数大于周期时）
	*p = cp
	return nil
}

// RepayLoan 主动还款：仅有效状态可用；先还利息后还本金；
// 超过本息合计报 ErrOverpayment。
func (e *Engine) RepayLoan(id string, day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.get(id, day)
	if err != nil {
		return err
	}
	cp := *p
	cp.settle(day)
	if cp.state != StateActive {
		return ErrStateNotAllowed
	}
	if err := cp.ledger.repay(amount, day); err != nil {
		return err
	}
	*p = cp
	return nil
}

// Reinstate 复效：仅中止状态且在复效期内可申请；须一次性补缴
// 中止期间全部到期保费与已有借款本息，不足报 ErrInsufficientPayment
// 且不得记入任何部分款项；超出部分不予入账。
func (e *Engine) Reinstate(id string, day int, amount int64) error {
	if day < 0 || amount <= 0 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.get(id, day)
	if err != nil {
		return err
	}
	cp := *p
	cp.settle(day)
	if cp.state != StateLapsed {
		return ErrStateNotAllowed
	}
	required := cp.cfg.Premium + cp.ledger.balanceAt(day)
	if amount < required {
		return ErrInsufficientPayment
	}
	cp.ledger.clear(day)
	cp.paidCount++ // 补缴中止期间到期的全部保费（恰为一期）
	cp.state = StateActive
	cp.waitingEnd = day + cp.cfg.WaitingDays - 1 // 复效生效日起重新适用等待天数
	cp.settle(day)
	*p = cp
	return nil
}

// Claim 出险判定：以出险日所处状态为准；等待期内出险不赔付且
// 与中止不赔付可区分。
func (e *Engine) Claim(id string, day int) (ClaimResult, error) {
	if day < 0 {
		return ClaimResult{}, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.get(id, day)
	if err != nil {
		return ClaimResult{}, err
	}
	p.settle(day)
	res := ClaimResult{}
	switch {
	case p.inWaiting() && (p.state == StateActive || p.state == StateGrace):
		res.Verdict = DenyWaiting
	case p.state == StateActive:
		res.Verdict = PayFull
	case p.state == StateGrace:
		res.Verdict = PayReduced
		res.Deduction = p.cfg.Premium
	case p.state == StateLapsed:
		res.Verdict = DenyLapsed
	default:
		res.Verdict = DenyTerminated
	}
	return res, nil
}
