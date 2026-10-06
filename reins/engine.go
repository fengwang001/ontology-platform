package reins

import "sync"

// Stats 暴露可验证的内部行为计数。
type Stats struct {
	RecomputedEvents int64 // 历史累计被重算超赔承担的事故数
}

// Engine 是再保险分出引擎，所有入口可并发调用。
type Engine struct {
	mu        sync.Mutex
	treaty    Treaty
	policies  map[string]*policy
	claims    map[string]*claim
	eventByID map[string]*eventAgg
	events    []*eventAgg // 按 (事故时刻, 事故编号) 升序
	stats     Stats
}

func NewEngine(t Treaty) (*Engine, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}
	return &Engine{
		treaty:    t,
		policies:  make(map[string]*policy),
		claims:    make(map[string]*claim),
		eventByID: make(map[string]*eventAgg),
	}, nil
}

// RegisterPolicy 登记原保单并确定其分出结构。
// 拒绝次序：参数非法 > 保单重复 > 超出承保能力。
func (e *Engine) RegisterPolicy(no string, sumInsured int64, startDay, endDay int) (Cession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if sumInsured <= 0 || startDay < 0 || endDay <= startDay {
		return Cession{}, &Error{CodeInvalidParam, "参数非法"}
	}
	if _, ok := e.policies[no]; ok {
		return Cession{}, &Error{CodePolicyDuplicate, "保单重复"}
	}
	ces, err := computeCession(e.treaty, sumInsured)
	if err != nil {
		return Cession{}, err
	}
	e.policies[no] = &policy{
		no:         no,
		sumInsured: sumInsured,
		startSec:   int64(startDay) * secondsPerDay,
		endSec:     int64(endDay) * secondsPerDay,
		ces:        ces,
	}
	return ces, nil
}

// AddClaim 登记一笔赔款并返回当前归属。
// 拒绝次序：参数非法 > 保单不存在 > 赔款已存在 > 事故未承保。
func (e *Engine) AddClaim(claimNo, policyNo, eventID string, eventTime int64, amount int64) (Split, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if amount <= 0 || eventTime < 0 {
		return Split{}, &Error{CodeInvalidParam, "参数非法"}
	}
	p, ok := e.policies[policyNo]
	if !ok {
		return Split{}, &Error{CodePolicyNotFound, "保单不存在"}
	}
	if amount > p.sumInsured {
		return Split{}, &Error{CodeInvalidParam, "参数非法: 赔款金额超过保额"}
	}
	if _, ok := e.claims[claimNo]; ok {
		return Split{}, &Error{CodeClaimExists, "赔款已存在"}
	}
	if !p.covers(eventTime) {
		return Split{}, &Error{CodeAccidentNotCovered, "事故未承保"}
	}
	c := &claim{no: claimNo, policyNo: policyNo, eventID: eventID, time: eventTime, amount: amount}
	c.qs, c.surplus, c.net = splitClaim(p, amount)
	e.addToEvent(c)
	e.claims[claimNo] = c
	return c.split(), nil
}

// RemoveClaim 撤销赔款；撤销后结果与该赔款从未存在时一致。
func (e *Engine) RemoveClaim(claimNo string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.claims[claimNo]
	if !ok {
		return &Error{CodeClaimNotFound, "赔款不存在"}
	}
	delete(e.claims, claimNo)
	e.removeFromEvent(c)
	return nil
}

// ClaimResult 查询一笔赔款当前的各方归属。
func (e *Engine) ClaimResult(claimNo string) (Split, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	c, ok := e.claims[claimNo]
	if !ok {
		return Split{}, &Error{CodeClaimNotFound, "赔款不存在"}
	}
	return c.split(), nil
}

// Stats 返回内部行为计数，用于验证重算范围。
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stats
}
