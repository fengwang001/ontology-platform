package health

import "sync"

type personState struct {
	policies []*policy
	archive  *archive
}

// inRaisedWaiting 判断续保提额后的“增量等待期”：连续续保、保额确实提高、
// 出险日落在新等待期内。此时原保额以内仍赔付（仅封顶），
// 诊断不记档案，也不是普通意义上的等待期不赔。
func (p *policy) inRaisedWaiting(day int64) bool {
	return p.renewal && p.in.Amount > p.baseAmount && p.inWaiting(day)
}

// Engine 健康险等待期与既往症除外判定引擎。
// 一把互斥锁串行化全部变更：结果等价于某个串行顺序，
// 同一操作序列重放得到完全相同的判定与档案；目录内部另有自己的锁。
type Engine struct {
	mu      sync.Mutex
	cat     *Catalog
	persons map[string]*personState
	claims  map[string]struct{}
}

// NewEngine 创建使用空目录的引擎。
func NewEngine() *Engine {
	return &Engine{cat: NewCatalog(), persons: map[string]*personState{}, claims: map[string]struct{}{}}
}

// Catalog 暴露目录供检查使用。
func (e *Engine) Catalog() *Catalog { return e.cat }

// AddCode 登记疾病编码。
func (e *Engine) AddCode(in CodeInput) error { return e.cat.AddCode(in) }

// ChangeParent 变更编码上级。
func (e *Engine) ChangeParent(code, newParent string) error {
	return e.cat.ChangeParent(code, newParent)
}

// RegisterPolicy 登记保单。先做全部校验再落状态，被拒绝不改变任何状态。
// 拒绝次序：参数非法 > 编码不存在 > 区间重叠。
func (e *Engine) RegisterPolicy(in PolicyInput) error {
	if in.Person == "" || in.Start < 0 || in.End <= in.Start ||
		in.RegisteredAt < 0 || in.Amount <= 0 || in.WaitDays < 0 {
		return ErrInvalidArgument
	}
	declared := append([]string(nil), in.Declared...)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cat.mu.RLock()
	for _, code := range declared {
		if !e.cat.has(code) {
			e.cat.mu.RUnlock()
			return ErrCodeNotFound
		}
	}
	e.cat.mu.RUnlock()

	st := e.persons[in.Person]
	var pols []*policy
	if st != nil {
		pols = st.policies
	}
	candidate := &policy{in: in}
	for _, old := range pols {
		if candidate.overlaps(old) {
			return ErrIntervalOverlap
		}
	}

	// 续保链：找到覆盖末端恰为新生效日、且登记不晚于该末端的旧保单。
	// 无重叠前提下这样的旧保单至多一张；链条中断后再恢复相接也仍是续保
	// （规则只要求“原保单到期日之前（含）登记、生效日恰等于到期日”）。
	var prev *policy
	for _, old := range pols {
		if renewablePrev(old, in.Start, in.RegisteredAt) {
			prev = old
			break
		}
	}
	if prev != nil {
		candidate.renewal = true
		candidate.baseAmount = prev.in.Amount
		if in.Amount <= prev.in.Amount || in.WaitDays == 0 {
			candidate.in.WaitDays = 0 // 未提高保额：整单不受等待期约束
		}
	}

	if st == nil {
		st = &personState{archive: newArchive()}
		e.persons[in.Person] = st
	}
	st.policies = append(st.policies, candidate)
	for _, code := range declared {
		st.archive.add(code) // 告知编码全部记入既往症档案
	}
	return nil
}

// SubmitClaim 受理一笔理赔。先校验、后生效，被拒绝不写档案。
// 拒绝次序：参数非法 > 被保人不存在 > 编码不存在 > 理赔已存在 > 出险日未承保。
func (e *Engine) SubmitClaim(in ClaimInput) (*ClaimResult, error) {
	if in.ID == "" || in.Person == "" || len(in.Diagnoses) == 0 {
		return nil, ErrInvalidArgument
	}
	for _, d := range in.Diagnoses {
		if d.Code == "" || d.Fee <= 0 {
			return nil, ErrInvalidArgument
		}
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.persons[in.Person]
	if st == nil || len(st.policies) == 0 {
		return nil, ErrInsuredNotFound
	}
	e.cat.mu.RLock()
	for _, d := range in.Diagnoses {
		if !e.cat.has(d.Code) {
			e.cat.mu.RUnlock()
			return nil, ErrCodeNotFound
		}
	}
	e.cat.mu.RUnlock()

	if _, ok := e.claims[in.ID]; ok {
		return nil, ErrClaimExists
	}
	var pol *policy
	for _, p := range st.policies {
		if p.covers(in.Day) {
			pol = p
			break
		}
	}
	if pol == nil {
		return nil, ErrUninsuredDate
	}

	// 全部校验通过，逐诊断判定并让等待期诊断即时入档案，
	// 使同一笔理赔内后续诊断也能看到本笔新增的除外（串行重放确定）。
	res := &ClaimResult{Verdicts: make([]DiagnosisVerdict, len(in.Diagnoses))}
	var totalPaid int64
	for i, d := range in.Diagnoses {
		v := DiagnosisVerdict{Code: d.Code, Fee: d.Fee}
		e.cat.mu.RLock()
		isAccident := e.cat.accident(d.Code)
		isExcluded, _ := st.archive.excluded(d.Code, e.cat)
		e.cat.mu.RUnlock()
		switch {
		case isExcluded:
			v.Reason = ReasonExcluded // 既往症除外优先于等待期
		case !isAccident && pol.inWaiting(in.Day):
			if pol.inRaisedWaiting(in.Day) {
				// 连续续保提额：原保额以内仍赔付，高出部分在新等待期内不赔。
				v.Reason = ReasonPaid
				if totalPaid < pol.baseAmount {
					room := pol.baseAmount - totalPaid
					pay := d.Fee
					if pay > room {
						pay = room
					}
					totalPaid += pay
				}
			} else {
				v.Reason = ReasonWaiting // 普通等待期：不赔并记入档案
				st.archive.add(d.Code)
			}
		default:
			v.Reason = ReasonPaid
			capAmount := pol.capFor(in.Day, isAccident)
			room := capAmount - totalPaid
			if room <= 0 {
				break
			}
			pay := d.Fee
			if pay > room {
				pay = room
			}
			totalPaid += pay
		}
		res.Verdicts[i] = v
	}
	res.Payout = totalPaid
	e.claims[in.ID] = struct{}{}
	return res, nil
}

// ArchiveSnapshot 返回某被保人档案编码的排序快照。
func (e *Engine) ArchiveSnapshot(person string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := e.persons[person]
	if st == nil {
		return []string{}
	}
	return st.archive.snapshot()
}
