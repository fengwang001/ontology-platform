package delegation

import (
	"sync"
	"time"
)

// NaiveService 是独立维护的朴素参照实现，用于与 Service 做差分对照。
//
// 它与 Service 不共享任何求值代码：不建索引、不做记忆化，每次查询都
// 从全部历史事件出发做全量扫描，并用不动点迭代计算可委托权限闭包。
// 它的正确性来自算法上的显而易见，而非性能。
type NaiveService struct {
	mu     sync.Mutex
	clock  Clock
	seq    uint64
	lastT  time.Time
	events []naiveEvent
	nextID uint64
}

// naiveEvent 是一次变更调用的完整记录；查询不重写历史。
type naiveEvent struct {
	seq  uint64
	time time.Time
	kind OpKind // 仅 GrantDirect / RevokeDirect / Declare / Revoke 会落历史

	subject string
	perm    Permission

	// Declare 的完整声明内容
	decl DelegationInput
	id   uint64
}

// NewNaiveService 创建朴素参照实现。
func NewNaiveService(clock Clock) *NaiveService {
	if clock == nil {
		clock = RealClock{}
	}
	return &NaiveService{clock: clock, nextID: 1}
}

func (n *NaiveService) next() (uint64, time.Time) {
	n.seq++
	t := n.clock.Now()
	if t.Before(n.lastT) {
		t = n.lastT
	}
	n.lastT = t
	return n.seq, t
}

// GrantDirect 记录一次直接权限授予。
func (n *NaiveService) GrantDirect(subject string, perms ...Permission) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range perms {
		seq, t := n.next()
		n.events = append(n.events, naiveEvent{seq: seq, time: t, kind: OpGrantDirect, subject: subject, perm: p})
	}
}

// RevokeDirect 记录一次直接权限收缩。
func (n *NaiveService) RevokeDirect(subject string, perms ...Permission) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, p := range perms {
		seq, t := n.next()
		n.events = append(n.events, naiveEvent{seq: seq, time: t, kind: OpRevokeDirect, subject: subject, perm: p})
	}
}

// Declare 按与 Service 相同的固定优先级校验并（可能）记录一条委托。
func (n *NaiveService) Declare(in DelegationInput) (uint64, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	seq, t := n.next()
	subset := NewPermissionSet(in.Subset...)
	if in.Delegator == "" || in.Delegatee == "" || len(subset) == 0 {
		return 0, ErrInvalidArgument
	}
	if !in.ValidFrom.Before(in.ValidTo) {
		return 0, ErrInvalidArgument
	}
	delegatable, effective := n.compute(seq, t)
	// 1. 超范围
	if !subset.SubsetOf(effective[in.Delegator]) {
		return 0, ErrScopeExceeded
	}
	// 2. 禁止再委托
	if !subset.SubsetOf(delegatable[in.Delegator]) {
		return 0, ErrRedelegationDenied
	}
	// 3. 成环（对全部未撤销委托保守判定）
	if n.createsCycle(seq, in.Delegator, in.Delegatee) {
		return 0, ErrCycle
	}
	// 4. 已过期
	if !t.Before(in.ValidTo) {
		return 0, ErrExpired
	}
	id := n.nextID
	n.nextID++
	n.events = append(n.events, naiveEvent{seq: seq, time: t, kind: OpDeclare, decl: in, id: id})
	return id, nil
}

// createsCycle 在未撤销委托图上判断新增边是否成环（全量 DFS）。
func (n *NaiveService) createsCycle(cutoff uint64, delegator, delegatee string) bool {
	if delegator == delegatee {
		return true
	}
	revoked := n.revokedSet(cutoff)
	adj := map[string][]string{}
	for _, ev := range n.events {
		if ev.kind != OpDeclare || ev.seq > cutoff || revoked[ev.id] {
			continue
		}
		adj[ev.decl.Delegator] = append(adj[ev.decl.Delegator], ev.decl.Delegatee)
	}
	stack := []string{delegatee}
	seen := map[string]bool{delegatee: true}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nxt := range adj[cur] {
			if nxt == delegator {
				return true
			}
			if !seen[nxt] {
				seen[nxt] = true
				stack = append(stack, nxt)
			}
		}
	}
	return false
}

// Revoke 撤销一条委托（幂等）。
func (n *NaiveService) Revoke(id uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	seq, t := n.next()
	found := false
	for _, ev := range n.events {
		if ev.kind == OpDeclare && ev.id == id {
			found = true
		}
	}
	if !found {
		return ErrDelegationNotFound
	}
	n.events = append(n.events, naiveEvent{seq: seq, time: t, kind: OpRevoke, id: id})
	return nil
}

// Check 在当前时刻判定访问。
func (n *NaiveService) Check(subject string, p Permission) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	seq, t := n.next()
	_, effective := n.compute(seq, t)
	return effective[subject].Contains(p)
}

// CheckAt 以 asOf 时刻可见的状态与该时刻的有效期重新判定。
func (n *NaiveService) CheckAt(subject string, p Permission, asOf time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	cutoff := uint64(0)
	for _, ev := range n.events {
		if !ev.time.After(asOf) && ev.seq > cutoff {
			cutoff = ev.seq
		}
	}
	_, effective := n.compute(cutoff, asOf)
	return effective[subject].Contains(p)
}

// revokedSet 返回截止序号下已被撤销的委托 ID 集合。
func (n *NaiveService) revokedSet(cutoff uint64) map[uint64]bool {
	revoked := map[uint64]bool{}
	for _, ev := range n.events {
		if ev.kind == OpRevoke && ev.seq <= cutoff {
			revoked[ev.id] = true
		}
	}
	return revoked
}

// compute 从全部历史事件出发，计算截止序号 cutoff、判定时刻 now 下
// 每个主体的可委托权限与实际权限。算法：先重放直接权限事件得到当前
// 持有集合，再对“生效且允许再委托且子集被完整支持”的委托做不动点扩张。
func (n *NaiveService) compute(cutoff uint64, now time.Time) (delegatable, effective map[string]PermissionSet) {
	direct := map[string]PermissionSet{}
	held := map[string]PermissionSet{} // 重放直接权限的当前持有状态
	var declares []naiveEvent
	revoked := n.revokedSet(cutoff)
	for _, ev := range n.events {
		if ev.seq > cutoff {
			continue
		}
		switch ev.kind {
		case OpGrantDirect:
			if held[ev.subject] == nil {
				held[ev.subject] = PermissionSet{}
			}
			held[ev.subject][ev.perm] = struct{}{}
		case OpRevokeDirect:
			delete(held[ev.subject], ev.perm)
		case OpDeclare:
			if !revoked[ev.id] {
				declares = append(declares, ev)
			}
		}
	}
	for subj, perms := range held {
		direct[subj] = perms.Clone()
	}

	active := func(ev naiveEvent) bool {
		return !now.Before(ev.decl.ValidFrom) && now.Before(ev.decl.ValidTo)
	}

	// 可委托权限的不动点闭包。
	delegatable = map[string]PermissionSet{}
	for subj, perms := range direct {
		delegatable[subj] = perms.Clone()
	}
	for changed := true; changed; {
		changed = false
		for _, ev := range declares {
			d := ev.decl
			if !d.AllowRedelegate || !active(ev) {
				continue
			}
			subset := NewPermissionSet(d.Subset...)
			if !subset.SubsetOf(delegatable[d.Delegator]) {
				continue
			}
			if delegatable[d.Delegatee] == nil {
				delegatable[d.Delegatee] = PermissionSet{}
			}
			for p := range subset {
				if !delegatable[d.Delegatee].Contains(p) {
					delegatable[d.Delegatee][p] = struct{}{}
					changed = true
				}
			}
		}
	}

	// 实际权限：直接权限 + 所有被完整支持的生效委托（含不允许再委托的）。
	effective = map[string]PermissionSet{}
	for subj, perms := range direct {
		effective[subj] = perms.Clone()
	}
	for _, ev := range declares {
		d := ev.decl
		if !active(ev) {
			continue
		}
		subset := NewPermissionSet(d.Subset...)
		if !subset.SubsetOf(delegatable[d.Delegator]) {
			continue
		}
		if effective[d.Delegatee] == nil {
			effective[d.Delegatee] = PermissionSet{}
		}
		effective[d.Delegatee].Union(subset)
	}
	return delegatable, effective
}
