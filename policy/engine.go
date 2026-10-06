package policy

import (
	"container/heap"
	"sync"
)

// PolicyInput 保单登记参数，金额单位为分，时刻单位为天。
type PolicyInput struct {
	ID              string
	EffectiveDate   int64   // 生效日，非负
	AnnualPremium   int64   // 年缴保费，正整数分
	SumInsured      int64   // 基本保额，正整数分
	MinSumInsured   int64   // 最低保额，正整数分且不超过基本保额
	HesitationDays  int64   // 犹豫期天数，非负
	IssueFee        int64   // 工本费，非负整数分
	CashValueRatios []int64 // 现金价值比例表（百分比，非负，可超 100）
	PaymentTerm     int     // 缴费期（年），正整数
	Beneficiary     string  // 初始受益人
}

// EventKind 时刻推进产生的事件类别。
type EventKind int

const (
	EventEffective    EventKind = iota + 1 // 批改已生效
	EventPendingTopUp                      // 批改缺补缴，停留待补缴
)

// Event 一次批改生效尝试的结果。
type Event struct {
	EndorsementID string
	Kind          EventKind
	Refund        int64 // 保额减少退还的保费（分）
	TopUp         int64 // 保额增加需补缴的保费（分）
}

// EndorsementSnapshot 批改状态快照。
type EndorsementSnapshot struct {
	State EndorsementState
	TopUp int64
}

// Snapshot 保单账快照，用于重放对比与“被拒操作不留痕”验证。
type Snapshot struct {
	Now          int64
	SumInsured   int64
	Premium      int64
	TotalPaid    int64
	PaymentTerm  int
	Beneficiary  string
	Terminated   bool
	Endorsements map[string]EndorsementSnapshot
}

// Engine 保单引擎。所有入口共用一把互斥锁，并发调用等价于某串行顺序。
type Engine struct {
	mu       sync.Mutex
	policies map[string]*Policy
}

func NewEngine() *Engine {
	return &Engine{policies: make(map[string]*Policy)}
}

// RegisterPolicy 登记保单，保单时钟自生效日起走。
func (e *Engine) RegisterPolicy(in PolicyInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if in.ID == "" || in.EffectiveDate < 0 || in.AnnualPremium <= 0 ||
		in.SumInsured <= 0 || in.MinSumInsured <= 0 || in.MinSumInsured > in.SumInsured ||
		in.HesitationDays < 0 || in.IssueFee < 0 || len(in.CashValueRatios) == 0 ||
		in.PaymentTerm <= 0 {
		return newErr(ErrInvalidParam, "参数非法")
	}
	for _, r := range in.CashValueRatios {
		if r < 0 {
			return newErr(ErrInvalidParam, "参数非法")
		}
	}
	if _, dup := e.policies[in.ID]; dup {
		return newErr(ErrInvalidParam, "参数非法: 保单号重复")
	}
	ratios := make([]int64, len(in.CashValueRatios))
	copy(ratios, in.CashValueRatios)
	e.policies[in.ID] = &Policy{
		id:            in.ID,
		effDate:       in.EffectiveDate,
		premium:       in.AnnualPremium,
		sumInsured:    in.SumInsured,
		minSumInsured: in.MinSumInsured,
		hesitation:    in.HesitationDays,
		issueFee:      in.IssueFee,
		ratios:        ratios,
		paymentTerm:   in.PaymentTerm,
		beneficiary:   in.Beneficiary,
		now:           in.EffectiveDate,
		paidYears:     make(map[int64]bool),
		endorsements:  make(map[string]*endorsement),
		maxEffDay:     -1,
	}
	return nil
}

// Advance 把保单时刻推进到 day，区间内到达生效日的预约批改按
// （生效日， 申请次序）依次生效；缺补缴的停留待补缴，不阻塞其后批改。
func (e *Engine) Advance(id string, day int64) ([]Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if day < 0 {
		return nil, newErr(ErrInvalidParam, "参数非法")
	}
	p, ok := e.policies[id]
	if !ok {
		return nil, newErr(ErrPolicyNotFound, "保单不存在")
	}
	if day < p.now {
		return nil, newErr(ErrClockRegression, "时钟回退")
	}
	if p.terminated {
		return nil, newErr(ErrTerminated, "已终态")
	}
	var events []Event
	for len(p.queue) > 0 && p.queue[0].effDay <= day {
		en := heap.Pop(&p.queue).(*endorsement)
		events = append(events, p.effectuate(en))
	}
	p.now = day
	return events, nil
}

// Snapshot 返回保单账当前快照。
func (e *Engine) Snapshot(id string) (Snapshot, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.policies[id]
	if !ok {
		return Snapshot{}, newErr(ErrPolicyNotFound, "保单不存在")
	}
	snap := Snapshot{
		Now:          p.now,
		SumInsured:   p.sumInsured,
		Premium:      p.premium,
		TotalPaid:    p.totalPaid,
		PaymentTerm:  p.paymentTerm,
		Beneficiary:  p.beneficiary,
		Terminated:   p.terminated,
		Endorsements: make(map[string]EndorsementSnapshot, len(p.endorsements)),
	}
	for eid, en := range p.endorsements {
		snap.Endorsements[eid] = EndorsementSnapshot{State: en.state, TopUp: en.topUp}
	}
	return snap, nil
}
