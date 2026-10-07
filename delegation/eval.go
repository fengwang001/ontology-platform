package delegation

import "time"

// evaluator 在一次判定内对委托图（DAG）做记忆化求值。
// 通过按受托方索引的邻接表，只访问目标主体上游可达的委托记录，
// 遍历量与系统中累计委托总数无关。
type evaluator struct {
	svc     *Service
	cutoff  uint64    // 状态截止序号（含）
	now     time.Time // 用于有效期判定的“当前时刻”
	visited int       // 已遍历的委托记录数（可观测开销）

	delegatableMemo map[string]PermissionSet
	effectiveMemo   map[string]PermissionSet
}

func newEvaluator(svc *Service, cutoff uint64, now time.Time) *evaluator {
	return &evaluator{
		svc:             svc,
		cutoff:          cutoff,
		now:             now,
		delegatableMemo: make(map[string]PermissionSet),
		effectiveMemo:   make(map[string]PermissionSet),
	}
}

// active 判断委托记录在截止序号与当前时刻下是否处于生效状态：
// 已声明、未撤销、且处于有效期 [validFrom, validTo) 内。
func (e *evaluator) active(d *delegationRecord) bool {
	if d.declaredSeq > e.cutoff {
		return false
	}
	if d.revokedSeq != 0 && d.revokedSeq <= e.cutoff {
		return false
	}
	return !e.now.Before(d.validFrom) && e.now.Before(d.validTo)
}

// directAt 返回主体在截止序号下持有的直接权限。
func (e *evaluator) directAt(subject string) PermissionSet {
	out := PermissionSet{}
	for perm, intervals := range e.svc.direct[subject] {
		for _, iv := range intervals {
			if iv.fromSeq <= e.cutoff && (iv.toSeq == 0 || e.cutoff < iv.toSeq) {
				out[perm] = struct{}{}
				break
			}
		}
	}
	return out
}

// delegatable 返回主体可再委托的权限：直接权限，以及来自
// “生效、允许再委托、且声明子集仍被委托方可委托权限完整支持”的委托的子集。
// 支持条件是“全有或全无”：子集不被完整支持时该委托整体不贡献任何权限。
func (e *evaluator) delegatable(subject string) PermissionSet {
	if m, ok := e.delegatableMemo[subject]; ok {
		return m
	}
	out := e.directAt(subject)
	for _, d := range e.svc.incoming[subject] {
		e.visited++
		if !e.active(d) || !d.allowRedelegate {
			continue
		}
		if d.subset.SubsetOf(e.delegatable(d.delegator)) {
			out.Union(d.subset)
		}
	}
	e.delegatableMemo[subject] = out
	return out
}

// effective 返回主体实际拥有的权限：直接权限，以及来自
// “生效且声明子集仍被委托方可委托权限完整支持”的委托的子集。
func (e *evaluator) effective(subject string) PermissionSet {
	if m, ok := e.effectiveMemo[subject]; ok {
		return m
	}
	out := e.directAt(subject)
	for _, d := range e.svc.incoming[subject] {
		e.visited++
		if !e.active(d) {
			continue
		}
		if d.subset.SubsetOf(e.delegatable(d.delegator)) {
			out.Union(d.subset)
		}
	}
	e.effectiveMemo[subject] = out
	return out
}

// witness 为“subject 拥有权限 p”提取一条支持性委托链：
// 返回自 subject 向上游的委托 ID 序列；p 为直接权限时返回空链。
// 调用前提是 p ∈ effective(subject)。visited 集合防御意外成环。
func (e *evaluator) witness(subject string, p Permission, seen map[string]bool) []uint64 {
	if e.directAt(subject).Contains(p) {
		return nil
	}
	if seen[subject] {
		return nil
	}
	seen[subject] = true
	for _, d := range e.svc.incoming[subject] {
		if !e.active(d) || !d.subset.Contains(p) {
			continue
		}
		if !d.subset.SubsetOf(e.delegatable(d.delegator)) {
			continue
		}
		// p ∈ delegatable(delegator) ⊆ effective(delegator)，递归必可找到链。
		return append([]uint64{d.id}, e.witness(d.delegator, p, seen)...)
	}
	return nil
}
