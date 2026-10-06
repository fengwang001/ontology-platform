package ontology

// naive 是按题面规则独立写成的朴素对照模型：
//   - 既往症判定不沿祖先链查表，而是对档案中每个编码展开整棵子树，
//     诊断落在任一已展开集合内即除外（O(档案数×子树大小)）。
//   - 意外判定同样用“意外根子树集合”，而非祖先链。
//   - 保单线性扫描、规则逐分支直接书写，刻意不与实现共享任何辅助函数。
// 它只用于测试，不进入交付实现。

type naiveCode struct {
	parent     string
	accidental bool
	hasParent  bool
}

type naivePolicy struct {
	registerAt, effective, expiry, waitDays, amount, base int
	partial                                               bool
}

type naive struct {
	codes    map[string]*naiveCode
	children map[string][]string
	policies map[string][]*naivePolicy
	archive  map[string]map[string]struct{}
	claims   map[string]struct{}
}

func newNaive() *naive {
	return &naive{
		codes:    map[string]*naiveCode{},
		children: map[string][]string{},
		policies: map[string][]*naivePolicy{},
		archive:  map[string]map[string]struct{}{},
		claims:   map[string]struct{}{},
	}
}

// subtree 返回 root 及其全部下级（朴素 DFS 展开）。
func (m *naive) subtree(root string) map[string]struct{} {
	out := map[string]struct{}{}
	stack := []string{root}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := out[c]; seen {
			continue
		}
		out[c] = struct{}{}
		stack = append(stack, m.children[c]...)
	}
	return out
}

func (m *naive) excluded(u, code string) bool {
	arc := m.archive[u]
	for a := range arc {
		set := m.subtree(a)
		if _, ok := set[code]; ok {
			return true
		}
	}
	return false
}

func (m *naive) accidental(code string) bool {
	for c, n := range m.codes {
		if !n.accidental {
			continue
		}
		if _, ok := m.subtree(c)[code]; ok {
			return true
		}
	}
	return false
}

func (m *naive) addCode(code, parent string, accidental bool) error {
	if code == "" {
		return ErrInvalidParam
	}
	if _, ok := m.codes[code]; ok {
		return ErrCodeDuplicate
	}
	if parent != "" {
		if _, ok := m.codes[parent]; !ok {
			return ErrParentNotFound
		}
	}
	m.codes[code] = &naiveCode{parent: parent, accidental: accidental, hasParent: parent != ""}
	if parent != "" {
		m.children[parent] = append(m.children[parent], code)
	}
	return nil
}

func (m *naive) register(in RegisterPolicyInput) error {
	if in.Insured == "" || in.Effective < 0 || in.Expiry <= in.Effective ||
		in.RegisterAt < 0 || in.WaitDays < 0 || in.Amount <= 0 {
		return ErrInvalidParam
	}
	for _, c := range in.Disclosure {
		if _, ok := m.codes[c]; !ok {
			return ErrCodeNotFound
		}
	}
	for _, p := range m.policies[in.Insured] {
		if in.Effective < p.expiry && p.effective < in.Expiry {
			return ErrOverlap
		}
	}

	np := &naivePolicy{
		registerAt: in.RegisterAt, effective: in.Effective, expiry: in.Expiry,
		waitDays: in.WaitDays, amount: in.Amount, base: in.Amount,
	}
	chain := m.policies[in.Insured]
	if n := len(chain); n > 0 {
		prev := chain[n-1]
		if in.RegisterAt <= prev.expiry && in.Effective == prev.expiry {
			np.base = prev.amount
			if in.Amount > prev.amount {
				np.partial = in.WaitDays > 0
			} else {
				np.waitDays = 0
			}
		}
	}
	if _, ok := m.archive[in.Insured]; !ok {
		m.archive[in.Insured] = map[string]struct{}{}
	}
	for _, c := range in.Disclosure {
		m.archive[in.Insured][c] = struct{}{}
	}
	m.policies[in.Insured] = append(chain, np)
	return nil
}

type naiveDiag struct {
	code    string
	verdict string
}

type naiveResult struct {
	paid  int
	diags []naiveDiag
}

func (m *naive) claim(id, u string, day int, ds []Diagnosis) (*naiveResult, error) {
	if id == "" || u == "" || day < 0 || len(ds) == 0 {
		return nil, ErrInvalidParam
	}
	for _, d := range ds {
		if d.Charge <= 0 {
			return nil, ErrInvalidParam
		}
	}
	if _, ok := m.policies[u]; !ok {
		return nil, ErrInsuredNotFound
	}
	for _, d := range ds {
		if _, ok := m.codes[d.Code]; !ok {
			return nil, ErrCodeNotFound
		}
	}
	if _, ok := m.claims[id]; ok {
		return nil, ErrClaimExists
	}
	var pol *naivePolicy
	for _, p := range m.policies[u] {
		if day >= p.effective && day < p.expiry {
			pol = p
			break
		}
	}
	if pol == nil {
		return nil, ErrNotInsured
	}

	waiting := pol.waitDays > 0 && day <= pol.effective+pol.waitDays-1
	res := &naiveResult{}
	var nonAcc, acc int
	for _, d := range ds {
		switch {
		case m.excluded(u, d.Code):
			res.diags = append(res.diags, naiveDiag{d.Code, VerdictExcluded})
		case waiting && !pol.partial && !m.accidental(d.Code):
			m.archive[u][d.Code] = struct{}{}
			res.diags = append(res.diags, naiveDiag{d.Code, VerdictInWaiting})
		default:
			res.diags = append(res.diags, naiveDiag{d.Code, VerdictPaid})
			if m.accidental(d.Code) {
				acc += d.Charge
			} else {
				nonAcc += d.Charge
			}
		}
	}
	if waiting && pol.partial && nonAcc > pol.base {
		nonAcc = pol.base
	}
	paid := nonAcc + acc
	if paid > pol.amount {
		paid = pol.amount
	}
	res.paid = paid
	m.claims[id] = struct{}{}
	return res, nil
}
