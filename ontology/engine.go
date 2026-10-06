package ontology

import "sync"

// validSpec 校验保单登记参数；非法一律返回 ErrInvalid。
func validSpec(s PolicySpec) bool {
	if s.InceptDay < 0 || s.YearLen <= 0 {
		return false
	}
	if s.PerClaimDeductible < 0 || s.AnnualDeductCap < 0 || s.OOPCap < 0 {
		return false
	}
	if s.InpatientRate < 0 || s.InpatientRate > 100 ||
		s.OutpatientRate < 0 || s.OutpatientRate > 100 {
		return false
	}
	return true
}

func validClaim(c Claim) bool {
	if c.ID == "" || len(c.Lines) == 0 {
		return false
	}
	for _, l := range c.Lines {
		if l.Amount <= 0 {
			return false
		}
		if l.Category != CatInpatient && l.Category != CatOutpatient {
			return false
		}
	}
	return true
}

// claimRecord 保存已受理理赔，供撤销与同号冲突检测使用。
type claimRecord struct {
	claim   Claim
	settle  Settlement
	yearIdx int64
	// 撤销恢复点：结算前年度累计
	prevDeduct int64
	prevOOP    int64
	// 该年度受理次序中的前一笔，形成每年度链表以支持“仅末笔可撤销”
	prevInYear *claimRecord
}

type policyBook struct {
	spec  PolicySpec
	years map[int64]*yearState
	// 每个保单年度的受理理赔按顺序组织；last 指向当前末笔
	last map[int64]*claimRecord
	// 理赔号 -> 记录（同保单内唯一）
	claims map[string]*claimRecord
}

// Engine 是并发安全的理赔分摊结算引擎。
type Engine struct {
	mu       sync.Mutex
	policies map[string]*policyBook
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{policies: map[string]*policyBook{}}
}

func (e *Engine) RegisterPolicy(id string, spec PolicySpec) error {
	if id == "" || !validSpec(spec) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.policies[id]; ok {
		return ErrInvalid // 重复登记按参数非法处理
	}
	e.policies[id] = &policyBook{
		spec:   spec,
		years:  map[int64]*yearState{},
		last:   map[int64]*claimRecord{},
		claims: map[string]*claimRecord{},
	}
	return nil
}

func (e *Engine) Submit(policyID string, in Claim) (Settlement, error) {
	// 拒绝次序：参数非法 > 保单不存在 > 理赔已存在 > 事故日未承保。
	if !validClaim(in) {
		return Settlement{}, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	book, ok := e.policies[policyID]
	if !ok {
		return Settlement{}, ErrPolicyMissing
	}
	if _, dup := book.claims[in.ID]; dup {
		return Settlement{}, ErrClaimExists
	}
	spec := book.spec
	if in.AccDay < spec.InceptDay {
		return Settlement{}, ErrNotCovered
	}

	yIdx := yearIndex(spec, in.AccDay)
	ys := book.years[yIdx]
	if ys == nil {
		ys = newYearState()
		book.years[yIdx] = ys
	}
	prevDeduct, prevOOP := ys.deductUsed, ys.oopUsed

	res, newDeduct, newOOP := settle(in, spec, prevDeduct, prevOOP)
	res.YearIndex = yIdx

	rec := &claimRecord{
		claim:      in,
		settle:     res,
		yearIdx:    yIdx,
		prevDeduct: prevDeduct,
		prevOOP:    prevOOP,
		prevInYear: book.last[yIdx],
	}
	book.last[yIdx] = rec
	book.claims[in.ID] = rec
	ys.deductUsed = newDeduct
	ys.oopUsed = newOOP
	return res, nil
}

func (e *Engine) Cancel(policyID, claimID string) error {
	// 拒绝次序：参数非法(理赔号空) > 保单不存在 > 事故日... > 理赔不存在 > 非末笔。
	if claimID == "" {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	book, ok := e.policies[policyID]
	if !ok {
		return ErrPolicyMissing
	}
	rec, ok := book.claims[claimID]
	if !ok {
		return ErrClaimMissing
	}
	if book.last[rec.yearIdx] != rec {
		return ErrNotLast
	}

	// 恢复年度累计到该笔结算前，并把末笔指针回退、释放理赔号。
	ys := book.years[rec.yearIdx]
	ys.deductUsed = rec.prevDeduct
	ys.oopUsed = rec.prevOOP
	book.last[rec.yearIdx] = rec.prevInYear
	delete(book.claims, claimID)
	return nil
}

// YearSnapshot 返回指定保单指定事故日所属年度的当前累计（测试/核对用）。
func (e *Engine) YearSnapshot(policyID string, day int64) (yearIdx, deductUsed, oopUsed int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	book, ok := e.policies[policyID]
	if !ok {
		return 0, 0, 0, ErrPolicyMissing
	}
	spec := book.spec
	if day < spec.InceptDay {
		return 0, 0, 0, ErrNotCovered
	}
	yIdx := yearIndex(spec, day)
	if ys := book.years[yIdx]; ys != nil {
		return yIdx, ys.deductUsed, ys.oopUsed, nil
	}
	return yIdx, 0, 0, nil
}
