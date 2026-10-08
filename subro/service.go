package subro

import (
	"fmt"
	"sync"

	"ontology/subro/alloc"
	"ontology/subro/clock"
	"ontology/subro/internal/clog"
	"ontology/subro/ledger"
)

// Recovery 一笔已登记的回收（仅作审计流水，结清不遍历它）。
type Recovery struct {
	Gross int64
	Fee   int64
	Net   int64
	Now   int64
}

// caseState 单案件的聚合状态。应得只由这些聚合量决定，
// 因此每次结清是 O(1)，与历史回收笔数无关。
type caseState struct {
	totalLoss int64
	paid      int64 // 保险人已赔付额；未获赔额 = totalLoss - paid
	deadline  int64
	ratioBP   int64
	waived    bool

	grossTotal int64
	feeTotal   int64 // 费用一经扣除不退还，只进不出
	netTotal   int64 // 净回收总额 = Σ(毛额-费用)

	recoveries clog.Log[Recovery] // 仅审计流水，结清不遍历
	ledger     ledger.Ledger
}

func (c *caseState) allocation() alloc.Allocation {
	return alloc.Compute(c.netTotal, alloc.Cap(c.totalLoss, c.ratioBP),
		c.totalLoss-c.paid, c.paid, c.waived)
}

// OpResult 一次被接受操作的结果：结清后的应得、已发放与本次调整记录。
type OpResult struct {
	Entitlements alloc.Entitlements
	Disbursed    alloc.Entitlements
	Adjustments  []ledger.Adjustment
}

// Snapshot 案件在某一时刻的完整可观测状态。
type Snapshot struct {
	CaseID        string
	TotalLoss     int64
	InsurerPaid   int64
	Uncompensated int64
	Deadline      int64
	RatioBP       int64
	Waived        bool
	GrossTotal    int64
	FeeTotal      int64
	NetTotal      int64
	Cap           int64
	Distributable int64
	Entitlements  alloc.Entitlements
	Disbursed     alloc.Entitlements
	Adjustments   []ledger.Adjustment
	RecoveryCount int
}

// Service 代位追偿分配服务。所有方法可并发调用，
// 内部以单互斥串行化，结果等价于按接受顺序的串行执行。
type Service struct {
	mu    sync.Mutex
	cases map[string]*caseState
	clock clock.Clock
	seq   int64
	ops   clog.Log[Op] // 已接受操作日志，按应用顺序
}

// NewService 创建空服务。
func NewService() *Service {
	return &Service{cases: make(map[string]*caseState)}
}

// Apply 校验并应用一个操作。被拒绝的操作不改变任何状态、
// 已发放记录与时钟。错误按优先级只报第一个：
// 参数非法 > 时钟回退 > 案件不存在 > 已过时效 > 超出总损失额 > 已放弃。
func (s *Service) Apply(op Op) (OpResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := validateParams(op); err != nil {
		return OpResult{}, err
	}
	if op.Kind == OpRegisterCase {
		if _, dup := s.cases[op.Case.CaseID]; dup {
			return OpResult{}, fmt.Errorf("案件编号 %q 已存在: %w", op.Case.CaseID, ErrInvalidParam)
		}
	}
	if !s.clock.Acceptable(op.Now) {
		last, _ := s.clock.Last()
		return OpResult{}, fmt.Errorf("now=%d 小于上一次被接受操作的 now=%d: %w", op.Now, last, ErrClockRollback)
	}

	var st *caseState
	if op.Kind != OpRegisterCase {
		var ok bool
		if st, ok = s.cases[op.CaseID]; !ok {
			return OpResult{}, fmt.Errorf("案件 %q: %w", op.CaseID, ErrCaseNotFound)
		}
	}

	var err error
	switch op.Kind {
	case OpRegisterCase:
		st = s.register(op.Case)
	case OpRecover:
		err = st.recover(op)
	case OpAdjustRatio:
		st.ratioBP = op.RatioBP
	case OpSupplement:
		err = st.supplement(op)
	case OpWaive:
		err = st.waive(op)
	}
	if err != nil {
		return OpResult{}, err
	}

	// 每次被接受的操作之后立即结清：应得与已发放的最小差。
	a := st.allocation()
	adj := st.ledger.Settle(a.Entitlements, op.Now, op.Kind.String(), s.nextSeq)

	s.clock.Advance(op.Now)
	s.ops.Append(op)
	return OpResult{
		Entitlements: a.Entitlements,
		Disbursed:    st.ledger.Disbursed(),
		Adjustments:  adj,
	}, nil
}

func (s *Service) nextSeq() int64 {
	s.seq++
	return s.seq
}

func validateParams(op Op) error {
	invalid := func(format string, args ...any) error {
		return fmt.Errorf(format+": %w", append(args, ErrInvalidParam)...)
	}
	switch op.Kind {
	case OpRegisterCase:
		c := op.Case
		switch {
		case c.CaseID == "":
			return invalid("案件编号为空")
		case c.TotalLoss < 0:
			return invalid("总损失额 %d 为负", c.TotalLoss)
		case c.InsurerPaid < 0 || c.InsurerPaid > c.TotalLoss:
			return invalid("已赔付额 %d 不在 [0,%d] 内", c.InsurerPaid, c.TotalLoss)
		case c.RatioBP < 0 || c.RatioBP > 10000:
			return invalid("责任比例 %d 基点越界", c.RatioBP)
		}
	case OpRecover:
		switch {
		case op.CaseID == "":
			return invalid("案件编号为空")
		case op.Gross < 0:
			return invalid("回收毛额 %d 为负", op.Gross)
		case op.Fee < 0:
			return invalid("追偿费用 %d 为负", op.Fee)
		case op.Fee > op.Gross:
			return invalid("追偿费用 %d 大于毛额 %d", op.Fee, op.Gross)
		}
	case OpAdjustRatio:
		switch {
		case op.CaseID == "":
			return invalid("案件编号为空")
		case op.RatioBP < 0 || op.RatioBP > 10000:
			return invalid("责任比例 %d 基点越界", op.RatioBP)
		}
	case OpSupplement:
		switch {
		case op.CaseID == "":
			return invalid("案件编号为空")
		case op.Amount <= 0:
			return invalid("补充赔付额 %d 非正", op.Amount)
		}
	case OpWaive:
		if op.CaseID == "" {
			return invalid("案件编号为空")
		}
	default:
		return invalid("未知操作类型 %d", int(op.Kind))
	}
	return nil
}

func (s *Service) register(in CaseInput) *caseState {
	st := &caseState{
		totalLoss: in.TotalLoss,
		paid:      in.InsurerPaid,
		deadline:  in.Deadline,
		ratioBP:   in.RatioBP,
	}
	s.cases[in.CaseID] = st
	return st
}

func (c *caseState) recover(op Op) error {
	if op.Now > c.deadline {
		return fmt.Errorf("now=%d 晚于时效截止日 %d: %w", op.Now, c.deadline, ErrExpired)
	}
	net := op.Gross - op.Fee
	c.grossTotal += op.Gross
	c.feeTotal += op.Fee
	c.netTotal += net
	c.recoveries.Append(Recovery{Gross: op.Gross, Fee: op.Fee, Net: net, Now: op.Now})
	return nil
}

func (c *caseState) supplement(op Op) error {
	if c.paid+op.Amount > c.totalLoss {
		return fmt.Errorf("已赔付额 %d 加补充赔付 %d 超过总损失额 %d: %w",
			c.paid, op.Amount, c.totalLoss, ErrExceedsTotalLoss)
	}
	c.paid += op.Amount
	return nil
}

func (c *caseState) waive(op Op) error {
	if op.Now > c.deadline {
		return fmt.Errorf("now=%d 晚于时效截止日 %d: %w", op.Now, c.deadline, ErrExpired)
	}
	if c.waived {
		return fmt.Errorf("案件已声明放弃: %w", ErrAlreadyWaived)
	}
	c.waived = true
	return nil
}

// RegisterCase 登记案件。
func (s *Service) RegisterCase(now int64, in CaseInput) error {
	_, err := s.Apply(Op{Kind: OpRegisterCase, Now: now, CaseID: in.CaseID, Case: in})
	return err
}

// Recover 登记一笔回收并在接受后立即结清。
func (s *Service) Recover(now int64, caseID string, gross, fee int64) (OpResult, error) {
	return s.Apply(Op{Kind: OpRecover, Now: now, CaseID: caseID, Gross: gross, Fee: fee})
}

// AdjustRatio 调整第三方责任比例并在接受后立即结清。
func (s *Service) AdjustRatio(now int64, caseID string, ratioBP int64) (OpResult, error) {
	return s.Apply(Op{Kind: OpAdjustRatio, Now: now, CaseID: caseID, RatioBP: ratioBP})
}

// Supplement 保险人补充赔付并在接受后立即结清。
func (s *Service) Supplement(now int64, caseID string, amount int64) (OpResult, error) {
	return s.Apply(Op{Kind: OpSupplement, Now: now, CaseID: caseID, Amount: amount})
}

// Waive 被保险人声明放弃追偿权（一次性、不可撤销、须在时效内）。
func (s *Service) Waive(now int64, caseID string) (OpResult, error) {
	return s.Apply(Op{Kind: OpWaive, Now: now, CaseID: caseID})
}

// Snapshot 返回案件当前可观测状态；案件不存在时 ok=false。
func (s *Service) Snapshot(caseID string) (snap Snapshot, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.cases[caseID]
	if !ok {
		return Snapshot{}, false
	}
	a := st.allocation()
	return Snapshot{
		CaseID:        caseID,
		TotalLoss:     st.totalLoss,
		InsurerPaid:   st.paid,
		Uncompensated: st.totalLoss - st.paid,
		Deadline:      st.deadline,
		RatioBP:       st.ratioBP,
		Waived:        st.waived,
		GrossTotal:    st.grossTotal,
		FeeTotal:      st.feeTotal,
		NetTotal:      st.netTotal,
		Cap:           a.Cap,
		Distributable: a.Distributable,
		Entitlements:  a.Entitlements,
		Disbursed:     st.ledger.Disbursed(),
		Adjustments:   st.ledger.Adjustments(),
		RecoveryCount: st.recoveries.Len(),
	}, true
}

// CaseIDs 返回当前全部案件编号（顺序未定，供遍历核对）。
func (s *Service) CaseIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.cases))
	for id := range s.cases {
		ids = append(ids, id)
	}
	return ids
}

// LastNow 返回上一次被接受操作的 now；尚无被接受操作时 ok=false。
func (s *Service) LastNow() (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock.Last()
}

// AcceptedOps 返回已接受操作日志的副本，顺序即应用顺序；
// 将其重放到新服务可得到完全相同的状态与调整记录。
func (s *Service) AcceptedOps() []Op {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ops.Slice()
}
