package delegation

import "time"

// evalContext 承载一次求值过程中的记忆化结果、递归保护
// 与被检查委托记录的计数（用于遍历开销的可观测证明）。
type evalContext struct {
	t          time.Time
	memo       map[string]PermissionSet
	redelegMem map[string]PermissionSet
	inStack    map[string]bool
	visited    int
}

func newEvalContext(t time.Time) *evalContext {
	return &evalContext{
		t:          t,
		memo:       make(map[string]PermissionSet),
		redelegMem: make(map[string]PermissionSet),
		inStack:    make(map[string]bool),
	}
}

// effective 计算主体在 ctx.t 时刻的有效权限：
// 原始权限 与 所有生效且被上游完整支撑的委托子集 的并集。
//
// 级联收缩语义由此体现：若委托方在 ctx.t 的有效权限已不再包含
// 某条下游委托声明的全部内容，该委托整体不贡献任何权限
// （取整体失效而非保留交集）；若委托方另有独立来源仍能覆盖
// 该委托声明的全部内容，则该委托依然生效。
//
// 遍历只沿着"受托方 -> 委托记录 -> 委托方"的反向边进行，
// 检查的记录数由 ctx.visited 计数，与系统中记录总数无关。
// 调用方必须持有锁。
func (s *Service) effective(principal string, ctx *evalContext) PermissionSet {
	if m, ok := ctx.memo[principal]; ok {
		return m
	}
	if ctx.inStack[principal] {
		// 新增委托时已做成环检测，正常不会到达此处；
		// 防御性返回空集避免病态数据导致无限递归。
		return PermissionSet{}
	}
	ctx.inStack[principal] = true
	defer delete(ctx.inStack, principal)

	out := s.baseAt(principal, ctx.t)
	for _, d := range s.byDelegatee[principal] {
		ctx.visited++
		if !activeAt(d, ctx.t) {
			continue
		}
		if d.Subset.IsSubsetOf(s.effective(d.Delegator, ctx)) {
			out = out.Union(d.Subset)
		}
	}
	ctx.memo[principal] = out
	return out
}

// redelegatable 计算主体在 ctx.t 时刻可再委托的权限：
// 原始权限 与 所有生效、被上游完整支撑且标记允许再委托的委托子集 的并集。
// 调用方必须持有锁。
func (s *Service) redelegatable(principal string, ctx *evalContext) PermissionSet {
	if m, ok := ctx.redelegMem[principal]; ok {
		return m
	}
	if ctx.inStack[principal] {
		return PermissionSet{}
	}
	ctx.inStack[principal] = true
	defer delete(ctx.inStack, principal)

	out := s.baseAt(principal, ctx.t)
	for _, d := range s.byDelegatee[principal] {
		ctx.visited++
		if !activeAt(d, ctx.t) || !d.AllowRedelegate {
			continue
		}
		if d.Subset.IsSubsetOf(s.effective(d.Delegator, ctx)) {
			out = out.Union(d.Subset)
		}
	}
	ctx.redelegMem[principal] = out
	return out
}
