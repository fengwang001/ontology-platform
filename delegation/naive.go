package delegation

import "time"

// Naive 是独立维护的朴素参照实现：不建任何索引、不做任何记忆化，
// 每次调用都在线性扫描全部记录的基础上从头重算，语义与 Service
// 完全一致但实现路径不同，用于随机对照测试交叉验证 Service 的正确性。
// Naive 不是并发安全的，仅在单线程参照场景使用。
type Naive struct {
	clock       Clock
	baseEvents  []baseEvent
	delegations []*Delegation
	nextID      DelegationID
}

// NewNaive 创建以 clock 为时间来源的朴素参照实现。
func NewNaive(clock Clock) *Naive {
	return &Naive{clock: clock, nextID: 1}
}

// GrantBase 见 Service.GrantBase。
func (n *Naive) GrantBase(principal string, perms PermissionSet) {
	n.baseEvents = append(n.baseEvents, baseEvent{at: n.clock.Now(), principal: principal, grant: true, perms: perms.Clone()})
}

// ShrinkBase 见 Service.ShrinkBase。
func (n *Naive) ShrinkBase(principal string, perms PermissionSet) {
	n.baseEvents = append(n.baseEvents, baseEvent{at: n.clock.Now(), principal: principal, grant: false, perms: perms.Clone()})
}

// Delegate 见 Service.Delegate，校验优先级相同。
func (n *Naive) Delegate(req DelegationRequest) (DelegationID, error) {
	now := n.clock.Now()
	if !req.Subset.IsSubsetOf(n.effective(req.Delegator, now, nil)) {
		return 0, ErrSubsetExceeds
	}
	if !req.Subset.IsSubsetOf(n.redelegatable(req.Delegator, now, nil)) {
		return 0, ErrRedelegateNotAllowed
	}
	if n.reachable(req.Delegatee, req.Delegator, now) {
		return 0, ErrCycle
	}
	if !req.End.After(req.Start) || !req.End.After(now) {
		return 0, ErrExpired
	}
	d := &Delegation{
		ID:              n.nextID,
		Delegator:       req.Delegator,
		Delegatee:       req.Delegatee,
		Subset:          req.Subset.Clone(),
		Start:           req.Start,
		End:             req.End,
		AllowRedelegate: req.AllowRedelegate,
		CreatedAt:       now,
	}
	n.nextID++
	n.delegations = append(n.delegations, d)
	return d.ID, nil
}

// Revoke 见 Service.Revoke。
func (n *Naive) Revoke(id DelegationID) error {
	for _, d := range n.delegations {
		if d.ID == id {
			if d.RevokedAt != nil {
				return ErrDelegationRevokedDone
			}
			now := n.clock.Now()
			d.RevokedAt = &now
			return nil
		}
	}
	return ErrDelegationNotFound
}

// Decide 见 Service.Decide。
func (n *Naive) Decide(req AccessRequest) Decision {
	at := req.At
	if at.IsZero() {
		at = n.clock.Now()
	}
	eff := n.effective(req.Subject, at, nil)
	allowed := PermissionSet{req.ObjectType: req.Require}.IsSubsetOf(eff)
	return Decision{Allowed: allowed}
}

// baseAt 线性扫描全部事件重放原始权限。
func (n *Naive) baseAt(principal string, t time.Time) PermissionSet {
	out := PermissionSet{}
	for _, ev := range n.baseEvents {
		if ev.principal != principal || ev.at.After(t) {
			continue
		}
		if ev.grant {
			out = out.Union(ev.perms)
		} else {
			out = out.Subtract(ev.perms)
		}
	}
	return out
}

// effective 每次调用都重新扫描全部委托记录，不做记忆化；
// stack 仅用于病态数据的递归保护。
func (n *Naive) effective(p string, t time.Time, stack map[string]bool) PermissionSet {
	if stack[p] {
		return PermissionSet{}
	}
	if stack == nil {
		stack = map[string]bool{}
	}
	stack[p] = true
	defer delete(stack, p)

	out := n.baseAt(p, t)
	for _, d := range n.delegations {
		if d.Delegatee != p || !activeAt(d, t) {
			continue
		}
		if d.Subset.IsSubsetOf(n.effective(d.Delegator, t, stack)) {
			out = out.Union(d.Subset)
		}
	}
	return out
}

// redelegatable 与 effective 类似，但只计入允许再委托的上游委托。
func (n *Naive) redelegatable(p string, t time.Time, stack map[string]bool) PermissionSet {
	if stack[p] {
		return PermissionSet{}
	}
	if stack == nil {
		stack = map[string]bool{}
	}
	stack[p] = true
	defer delete(stack, p)

	out := n.baseAt(p, t)
	for _, d := range n.delegations {
		if d.Delegatee != p || !d.AllowRedelegate || !activeAt(d, t) {
			continue
		}
		if d.Subset.IsSubsetOf(n.effective(d.Delegator, t, stack)) {
			out = out.Union(d.Subset)
		}
	}
	return out
}

// reachable 在全部记录上做深度优先搜索。
func (n *Naive) reachable(from, to string, now time.Time) bool {
	if from == to {
		return true
	}
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, d := range n.delegations {
			if d.Delegator != cur || d.RevokedAt != nil || !d.End.After(now) {
				continue
			}
			if d.Delegatee == to {
				return true
			}
			if !seen[d.Delegatee] {
				seen[d.Delegatee] = true
				stack = append(stack, d.Delegatee)
			}
		}
	}
	return false
}
