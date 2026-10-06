package health

import (
	"sort"
	"sync"
)

// naiveCode 朴素模型里的目录节点：显式 parent + accident。
type naiveCode struct {
	parent   string
	accident bool
	exists   bool
}

type naivePolicy struct {
	regAt    int64
	start    int64
	end      int64
	wait     int64
	amount   int64
	declared []string
	renewal  bool
	base     int64
}

type naivePerson struct {
	policies []naivePolicy
	archive  map[string]bool
}

// NaiveEngine 是按同一规则“独立重写”的朴素参考模型：
// 目录、保单、档案、判定全部在这里另写一份，刻意不使用
// catalog/policy/archive 中的任何实现，只共享公开 DTO 与错误常量。
type NaiveEngine struct {
	mu      sync.Mutex
	codes   map[string]naiveCode
	persons map[string]*naivePerson
	claims  map[string]bool
}

// NewNaiveEngine 创建朴素模型。
func NewNaiveEngine() *NaiveEngine {
	return &NaiveEngine{
		codes:   map[string]naiveCode{},
		persons: map[string]*naivePerson{},
		claims:  map[string]bool{},
	}
}

func (n *NaiveEngine) AddCode(in CodeInput) error {
	if in.Code == "" {
		return ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.codes[in.Code].exists {
		return ErrCodeDuplicate
	}
	if in.Parent != "" && !n.codes[in.Parent].exists {
		return ErrParentNotFound
	}
	n.codes[in.Code] = naiveCode{parent: in.Parent, accident: in.Accident, exists: true}
	return nil
}

func (n *NaiveEngine) ChangeParent(code, newParent string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	node := n.codes[code]
	if !node.exists {
		return ErrCodeNotFound
	}
	if newParent != "" && !n.codes[newParent].exists {
		return ErrParentNotFound
	}
	cur := newParent
	for cur != "" {
		if cur == code {
			return ErrInvalidArgument
		}
		cur = n.codes[cur].parent
	}
	node.parent = newParent
	n.codes[code] = node
	return nil
}

func (n *NaiveEngine) RegisterPolicy(in PolicyInput) error {
	if in.Person == "" || in.Start < 0 || in.End <= in.Start ||
		in.RegisteredAt < 0 || in.Amount <= 0 || in.WaitDays < 0 {
		return ErrInvalidArgument
	}
	declared := append([]string(nil), in.Declared...)
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, code := range declared {
		if !n.codes[code].exists {
			return ErrCodeNotFound
		}
	}
	p := n.persons[in.Person]
	var pols []naivePolicy
	if p != nil {
		pols = p.policies
	}
	for _, q := range pols {
		if in.Start < q.end && q.start < in.End {
			return ErrIntervalOverlap
		}
	}
	np := naivePolicy{
		regAt: in.RegisteredAt, start: in.Start, end: in.End,
		wait: in.WaitDays, amount: in.Amount, declared: declared,
	}
	var prev *naivePolicy
	if p != nil {
		for i := range p.policies {
			q := &p.policies[i]
			if in.Start == q.end && in.RegisteredAt <= q.end {
				prev = q
				break
			}
		}
	}
	if prev != nil {
		np.renewal = true
		np.base = prev.amount
		if in.Amount <= prev.amount || in.WaitDays == 0 {
			np.wait = 0
		}
	}
	if p == nil {
		p = &naivePerson{archive: map[string]bool{}}
		n.persons[in.Person] = p
	}
	p.policies = append(p.policies, np)
	for _, code := range declared {
		p.archive[code] = true
	}
	return nil
}

func (n *NaiveEngine) SubmitClaim(in ClaimInput) (*ClaimResult, error) {
	if in.ID == "" || in.Person == "" || len(in.Diagnoses) == 0 {
		return nil, ErrInvalidArgument
	}
	for _, d := range in.Diagnoses {
		if d.Code == "" || d.Fee <= 0 {
			return nil, ErrInvalidArgument
		}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.persons[in.Person]
	if p == nil || len(p.policies) == 0 {
		return nil, ErrInsuredNotFound
	}
	for _, d := range in.Diagnoses {
		if !n.codes[d.Code].exists {
			return nil, ErrCodeNotFound
		}
	}
	if n.claims[in.ID] {
		return nil, ErrClaimExists
	}
	var cover *naivePolicy
	for i := len(p.policies) - 1; i >= 0; i-- {
		q := &p.policies[i]
		if in.Day >= q.start && in.Day < q.end {
			cover = q
			break
		}
	}
	if cover == nil {
		return nil, ErrUninsuredDate
	}

	res := &ClaimResult{Verdicts: make([]DiagnosisVerdict, len(in.Diagnoses))}
	var paid int64
	for i, d := range in.Diagnoses {
		v := DiagnosisVerdict{Code: d.Code, Fee: d.Fee}
		acc := false
		for c := d.Code; c != ""; c = n.codes[c].parent {
			if n.codes[c].accident {
				acc = true
				break
			}
		}
		excl := false
		for c := d.Code; ; {
			if p.archive[c] {
				excl = true
				break
			}
			par := n.codes[c].parent
			if par == "" {
				break
			}
			c = par
		}
		waiting := cover.wait > 0 && in.Day <= cover.start+cover.wait-1
		raisedWaiting := cover.renewal && cover.amount > cover.base && waiting
		switch {
		case excl:
			v.Reason = ReasonExcluded
		case !acc && waiting:
			if raisedWaiting {
				v.Reason = ReasonPaid
				if paid < cover.base {
					room := cover.base - paid
					add := d.Fee
					if add > room {
						add = room
					}
					paid += add
				}
			} else {
				v.Reason = ReasonWaiting
				p.archive[d.Code] = true
			}
		default:
			v.Reason = ReasonPaid
			limit := cover.amount
			if raisedWaiting && !acc {
				limit = cover.base
			}
			if paid < limit {
				room := limit - paid
				add := d.Fee
				if add > room {
					add = room
				}
				paid += add
			}
		}
		res.Verdicts[i] = v
	}
	res.Payout = paid
	n.claims[in.ID] = true
	return res, nil
}

func (n *NaiveEngine) ArchiveSnapshot(person string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	p := n.persons[person]
	out := []string{}
	if p != nil {
		for code := range p.archive {
			out = append(out, code)
		}
		sort.Strings(out)
	}
	return out
}
