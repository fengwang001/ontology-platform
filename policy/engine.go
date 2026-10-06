package policy

import "sync"

// Engine 是保单账与批改引擎，所有入口可并发调用。
// 串行化粒度：单一互斥锁把每个入口整体线性化，结果等价于某个串行顺序；
// 被拒绝操作在任何状态变更前返回，故不留痕。
type Engine struct {
	mu       sync.Mutex
	now      int64
	seq      int64
	policies map[string]*policy
}

// New 创建空引擎，初始时刻为 0。
func New() *Engine {
	return &Engine{policies: map[string]*policy{}}
}

// Now 返回当前时刻（整数天）。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

// PolicyView 是保单关键账目的只读快照。
type PolicyView struct {
	ID            string
	SumAssured    int64
	AnnualPremium int64
	PayPeriods    int64
	PaidTotal     int64
	Loan          int64
	Terminated    bool
	EffectiveDay  int64
	Now           int64
	Beneficiary   string
}

// Register 登记保单。
func (e *Engine) Register(id string, cfg Config) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" || !cfg.valid() {
		return ErrInvalid
	}
	if _, ok := e.policies[id]; ok {
		return ErrInvalid
	}
	p := &policy{
		id:                  id,
		cfg:                 cloneCfg(cfg),
		sumAssured:          cfg.BaseSumAssured,
		annualPremium:       cfg.AnnualPremium,
		payPeriods:          cfg.PayPeriods,
		paidYears:           map[int64]struct{}{},
		endByID:             map[string]*endorsement{},
		pending:             map[string]*endorsement{},
		heap:                newEndHeap(),
		lastEffectiveEndDay: -1,
	}
	e.policies[id] = p
	return nil
}

// AdvanceTime 推进时钟并让到达生效日的预约批改按次序依次生效。
// 待补缴批改停留在待补缴，不阻塞其后已可生效的其他批改。
// 返回本次推进中新到达处理点的批改（含已生效与转为待补缴者）的有序结果。
func (e *Engine) AdvanceTime(toDay int64) ([]EndInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if toDay < e.now {
		return nil, ErrClockBack
	}
	if toDay == e.now {
		return nil, nil
	}
	e.now = toDay
	var result []EndInfo
	for _, p := range e.policies {
		result = append(result, e.processDue(p)...)
	}
	return result, nil
}

// GetPolicy 返回保单关键账目的只读快照。
func (e *Engine) GetPolicy(id string) (PolicyView, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "" {
		return PolicyView{}, ErrInvalid
	}
	p, ok := e.policies[id]
	if !ok {
		return PolicyView{}, ErrPolicyMissing
	}
	return PolicyView{
		ID:            p.id,
		SumAssured:    p.sumAssured,
		AnnualPremium: p.annualPremium,
		PayPeriods:    p.payPeriods,
		PaidTotal:     p.paidTotal,
		Loan:          p.loan,
		Terminated:    p.terminated,
		EffectiveDay:  p.cfg.EffectiveDay,
		Now:           e.now,
		Beneficiary:   p.beneficiary,
	}, nil
}

// GetEndorsement 返回批改的只读视图。
func (e *Engine) GetEndorsement(policyID, endID string) (EndInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || endID == "" {
		return EndInfo{}, ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return EndInfo{}, ErrPolicyMissing
	}
	en, ok := p.endByID[endID]
	if !ok {
		return EndInfo{}, ErrEndMissing
	}
	return en.info(), nil
}

// CashValue 计算任意计算日的现金价值（不修改状态）。
// 开销 O(1)：只读取累计实缴保费 paidTotal 与比例表，不遍历任何历史。
func (e *Engine) CashValue(policyID string, day int64, loan int64) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || day < 0 || loan < 0 {
		return 0, ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return 0, ErrPolicyMissing
	}
	return p.cashValue(day, loan), nil
}

// SetLoan 由外部设置未偿借款本息（整数分，不计息）。
func (e *Engine) SetLoan(policyID string, loan int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if policyID == "" || loan < 0 {
		return ErrInvalid
	}
	p, ok := e.policies[policyID]
	if !ok {
		return ErrPolicyMissing
	}
	if p.terminated {
		return ErrTerminated
	}
	p.loan = loan
	return nil
}

func (c Config) valid() bool {
	if c.EffectiveDay < 0 || c.AnnualPremium <= 0 || c.BaseSumAssured <= 0 ||
		c.MinSumAssured < 0 || c.CoolingDays < 0 || c.PolicyFee < 0 || c.PayPeriods <= 0 {
		return false
	}
	if c.MinSumAssured > c.BaseSumAssured {
		return false
	}
	for _, r := range c.RatioTable {
		if r < 0 {
			return false
		}
	}
	return true
}

func cloneCfg(c Config) Config {
	out := c
	if c.RatioTable != nil {
		out.RatioTable = append([]int64(nil), c.RatioTable...)
	}
	return out
}

// cashValue = floor(累计实缴保费 * 当年比例 / 100) - 借款，下限为零。
func (p *policy) cashValue(day, loan int64) int64 {
	year := policyYear(day, p.cfg.EffectiveDay)
	ratio := ratioOf(p.cfg.RatioTable, year)
	gross := p.paidTotal * ratio / 100
	v := gross - loan
	if v < 0 {
		return 0
	}
	return v
}

// inCooling 判断 day 是否处于犹豫期（含生效日，末日当天仍在期内）。
func (p *policy) inCooling(day int64) bool {
	return day-p.cfg.EffectiveDay < p.cfg.CoolingDays
}

// processDue 处理保单堆中所有生效日不晚于当前时刻的批改，按次序依次生效；
// 缺补缴的停留待补缴且不影响后续批改处理。保单内取下一条只看堆顶 O(log n)。
func (e *Engine) processDue(p *policy) []EndInfo {
	var result []EndInfo
	for {
		top := p.heap.peek()
		if top == nil || top.effectiveDay > e.now {
			return result
		}
		en := p.heap.pop()
		if en.status != StatusScheduled {
			continue
		}
		e.applyEnd(p, en)
		result = append(result, en.info())
	}
}
