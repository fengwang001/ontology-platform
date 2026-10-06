package ontology

// Policy 是一张已登记的保单。
type Policy struct {
	Insured    string
	RegisterAt int // 登记日（用于判断续保登记是否在到期日之前，含到期日）
	Effective  int // 生效日（覆盖区间左端，含）
	Expiry     int // 到期日（覆盖区间右端，不含）
	WaitDays   int // 等待天数
	Amount     int // 保额（分）
	BaseAmount int // 续保衔接的原保额；新投保时等于 Amount
	Renewed    bool
	// PartialWaiting 为 true 表示当前等待期仅约束续保提高的“高出部分”：
	// 等待期内非意外诊断仍赔付，只是合计以 BaseAmount 为上限，且不入档案。
	PartialWaiting bool
}

// covers 判定 day 是否落在覆盖区间 [Effective, Expiry)。
func (p *Policy) covers(day int) bool {
	return day >= p.Effective && day < p.Expiry
}

// waitingEnd 返回等待期最后一天（生效日当天算第 1 天）；
// WaitDays<=0 表示无等待期，返回 Effective-1。
func (p *Policy) waitingEnd() int {
	return p.Effective + p.WaitDays - 1
}

// inWaiting 判定出险日是否处于等待期内（含等待期最后一天）。
func (p *Policy) inWaiting(day int) bool {
	return p.WaitDays > 0 && day <= p.waitingEnd()
}

// policyStore 保存各被保人的保单链。
type policyStore struct {
	chains map[string][]*Policy
}

func newPolicyStore() *policyStore {
	return &policyStore{chains: make(map[string][]*Policy)}
}

// add 登记保单并返回其与档案是否为连续续保。
func (s *policyStore) add(p *Policy) (renewed bool, prev *Policy) {
	chain := s.chains[p.Insured]
	// 连续续保：原保单到期日之前（含当天）登记，且新生效日恰等于原到期日。
	// 同一被保人区间不重叠已在登记时保证，因此取链尾保单即可唯一判定。
	if n := len(chain); n > 0 {
		last := chain[n-1]
		if p.RegisterAt <= last.Expiry && p.Effective == last.Expiry {
			s.chains[p.Insured] = append(chain, p)
			return true, last
		}
	}
	s.chains[p.Insured] = append(chain, p)
	return false, nil
}

// overlaps 判定新区间 [eff, exp) 是否与该被保人既有任一保单重叠。
func (s *policyStore) overlaps(insured string, eff, exp int) bool {
	for _, p := range s.chains[insured] {
		if eff < p.Expiry && p.Effective < exp {
			return true
		}
	}
	return false
}

// policyAt 返回覆盖 day 的保单；登记已拒绝重叠，故至多一张。
func (s *policyStore) policyAt(insured string, day int) *Policy {
	for _, p := range s.chains[insured] {
		if p.covers(day) {
			return p
		}
	}
	return nil
}

func (s *policyStore) hasPolicy(insured string) bool {
	return len(s.chains[insured]) > 0
}
