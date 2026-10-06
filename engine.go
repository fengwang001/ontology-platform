package ontology

import "sync"

// Diagnosis 是理赔中的单个诊断。
type Diagnosis struct {
	Code   string
	Charge int // 费用（正整数分）
}

// 诊断级判定依据。
const (
	VerdictPaid      = "赔付"
	VerdictExcluded  = "既往症除外"
	VerdictInWaiting = "等待期内"
)

// DiagnosisResult 是单个诊断的判定结果。
type DiagnosisResult struct {
	Code    string
	Charge  int
	Verdict string // 赔付 / 既往症除外 / 等待期内
	Paid    int    // 实际计入赔付（封顶前，赔付项为费用，否则 0）
	Reason  string // 可读判定依据
}

// ClaimResult 是一笔理赔的判定结果。
type ClaimResult struct {
	ClaimID   string
	Insured   string
	OccurDay  int
	Paid      int // 合计赔付（封顶后）
	Diagnoses []DiagnosisResult
}

// RegisterPolicyInput 是保单登记入参。
type RegisterPolicyInput struct {
	Insured    string
	RegisterAt int
	Effective  int
	Expiry     int
	WaitDays   int
	Amount     int
	Disclosure []string // 投保告知疾病编码集合
}

// Engine 是健康险等待期与既往症除外判定引擎。
// 单一互斥锁串行化所有变更入口；纯判定同样在锁内完成，
// 从而并发调用结果等价于某个串行顺序，重放结果确定。
type Engine struct {
	mu       sync.Mutex
	cat      *catalog
	policies *policyStore
	archives map[string]*archive
	claims   map[string]struct{}
}

// NewEngine 创建空引擎。
func NewEngine() *Engine {
	return &Engine{
		cat:      newCatalog(),
		policies: newPolicyStore(),
		archives: make(map[string]*archive),
		claims:   make(map[string]struct{}),
	}
}

// AddCode 维护疾病编码目录。
func (e *Engine) AddCode(code, parent string, accidental bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cat.addCode(code, parent, accidental)
}

// RegisterPolicy 登记保单。
func (e *Engine) RegisterPolicy(in RegisterPolicyInput) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 参数非法优先：被保人非空、生效日非负、到期日严格大于生效日、
	// 登记日非负、等待天数非负、保额为正。
	if in.Insured == "" || in.Effective < 0 || in.Expiry <= in.Effective ||
		in.RegisterAt < 0 || in.WaitDays < 0 || in.Amount <= 0 {
		return ErrInvalidParam
	}
	// 告知编码不在目录 -> 编码不存在（次序先于区间重叠）。
	for _, code := range in.Disclosure {
		if !e.cat.exists(code) {
			return ErrCodeNotFound
		}
	}
	// 区间重叠 -> 区间重叠。
	if e.policies.overlaps(in.Insured, in.Effective, in.Expiry) {
		return ErrOverlap
	}

	arch, existed := e.archives[in.Insured]
	if !existed {
		arch = newArchive()
		e.archives[in.Insured] = arch
	}

	p := &Policy{
		Insured:    in.Insured,
		RegisterAt: in.RegisterAt,
		Effective:  in.Effective,
		Expiry:     in.Expiry,
		WaitDays:   in.WaitDays,
		Amount:     in.Amount,
		BaseAmount: in.Amount,
	}
	renewed, prev := e.policies.add(p)

	// 全部校验通过后才落盘，保证被拒绝操作不改变任何状态。
	if renewed {
		p.Renewed = true
		if in.Amount > prev.Amount {
			// 续保提高保额：原保额以内不受等待期约束，高出部分按新等待期
			// 重新等待。等待期内非意外诊断仍赔，但以原保额为上限，不入档。
			p.BaseAmount = prev.Amount
			p.PartialWaiting = in.WaitDays > 0
		} else {
			// 保额未提高：整单不受等待期约束。
			p.BaseAmount = prev.Amount
			p.WaitDays = 0
		}
	}

	// 告知编码全部记入既往症档案（新投保与连续续保均延续同一档案）。
	for _, code := range in.Disclosure {
		arch.add(code)
	}
	return nil
}

// SubmitClaim 提交理赔并逐诊断判定。
func (e *Engine) SubmitClaim(claimID, insured string, occurDay int, diags []Diagnosis) (*ClaimResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 拒绝次序：参数非法 > 被保人不存在 > 编码不存在 > 理赔已存在 > 出险日未承保。
	// 先做不改变任何状态的校验，全部通过后才判定并记档案。
	if claimID == "" || insured == "" || occurDay < 0 || len(diags) == 0 {
		return nil, ErrInvalidParam
	}
	for _, d := range diags {
		if d.Charge <= 0 {
			return nil, ErrInvalidParam
		}
	}
	if !e.policies.hasPolicy(insured) {
		return nil, ErrInsuredNotFound
	}
	for _, d := range diags {
		if !e.cat.exists(d.Code) {
			return nil, ErrCodeNotFound
		}
	}
	if _, ok := e.claims[claimID]; ok {
		return nil, ErrClaimExists
	}
	pol := e.policies.policyAt(insured, occurDay)
	if pol == nil {
		return nil, ErrNotInsured
	}
	arch := e.archives[insured]

	res := &ClaimResult{ClaimID: claimID, Insured: insured, OccurDay: occurDay}
	waiting := pol.inWaiting(occurDay)
	var nonAccidentTotal, accidentTotal int

	for _, d := range diags {
		dr := DiagnosisResult{Code: d.Code, Charge: d.Charge}
		accidental := e.cat.isAccidental(d.Code)
		switch {
		case arch.excluded(d.Code, e.cat):
			// 既往症除外优先于等待期与意外豁免。
			dr.Verdict = VerdictExcluded
			dr.Reason = "诊断编码自身或祖先在既往症档案中，层级除外"
		case waiting && !pol.PartialWaiting && !accidental:
			// 新投保等待期内出险且非意外：不赔并记入档案。
			dr.Verdict = VerdictInWaiting
			dr.Reason = "出险日处于保单等待期内，费用不赔并将编码记入既往症档案"
			arch.add(d.Code)
		default:
			dr.Verdict = VerdictPaid
			dr.Paid = d.Charge
			if waiting && accidental {
				dr.Reason = "意外类编码不受等待期约束，按费用赔付"
				accidentTotal += d.Charge
			} else if waiting && pol.PartialWaiting {
				dr.Reason = "续保提高保额的等待期内：原保额以内照赔，合计以原保额封顶"
				nonAccidentTotal += d.Charge
			} else {
				dr.Reason = "诊断既未被除外也不在等待期，按费用赔付"
				nonAccidentTotal += d.Charge
			}
		}
		res.Diagnoses = append(res.Diagnoses, dr)
	}

	// 保额封顶：续保提高保额的等待期内，非意外部分以原保额为上限；
	// 意外部分不受等待期约束，可使用保额余量与高出部分额度。
	cap := pol.Amount
	paidNonAccident := nonAccidentTotal
	if waiting && pol.PartialWaiting {
		if paidNonAccident > pol.BaseAmount {
			paidNonAccident = pol.BaseAmount
		}
	}
	paid := paidNonAccident + accidentTotal
	if paid > cap {
		paid = cap
	}
	res.Paid = paid

	e.claims[claimID] = struct{}{}
	return res, nil
}

// ArchiveSnapshot 返回某被保人当前既往症档案的有序副本（测试/日志用）。
func (e *Engine) ArchiveSnapshot(insured string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if arch, ok := e.archives[insured]; ok {
		return arch.snapshot()
	}
	return nil
}
