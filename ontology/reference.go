package ontology

// reference.go 是独立维护的朴素参照实现。
// 它刻意使用与 Engine 不同的算法风格（递归、反复松弛求最浅深度、
// 全量复制状态），不共享任何内部实现，用于在随机生成的关系图与
// 动作序列上与 Engine 逐项对照。

// RefResult 是参照实现一次执行的完整可观察结果。
type RefResult struct {
	Allowed bool
	ErrKind ErrorKind // 0 表示无错误
	Skipped []InstanceID
	State   State // 执行后的预期状态（拒绝时等于执行前）
}

// ReferenceExecute 以朴素方式重放一次动作调用，不修改传入的 Store。
func ReferenceExecute(s *Store, decls map[ActionTypeID]ActionDecl, auth Authorizer, subject SubjectID, inv Invocation) RefResult {
	before := s.Snapshot()
	reject := func(kind ErrorKind) RefResult {
		return RefResult{Allowed: false, ErrKind: kind, State: before}
	}

	decl, ok := decls[inv.Action]
	if !ok {
		return reject(ErrInvalidParams)
	}
	if refValidate(s, decl, inv) {
		return reject(ErrInvalidParams)
	}

	// 递归收集可达实例（去重），再用反复松弛求每个实例的最浅深度。
	excluded := map[LinkTypeID]bool{}
	for _, lt := range decl.Cascade.ExcludeLinkTypes {
		excluded[lt] = true
	}
	reachable := map[InstanceID]bool{}
	var walk func(id InstanceID)
	walk = func(id InstanceID) {
		if reachable[id] {
			return
		}
		reachable[id] = true
		for _, l := range s.Links {
			if l.From == id && !excluded[l.Type] {
				walk(l.To)
			}
		}
	}
	for _, op := range inv.Ops {
		walk(op.Target)
	}
	depth := map[InstanceID]int{}
	for _, op := range inv.Ops {
		depth[op.Target] = 0
	}
	for changed := true; changed; {
		changed = false
		for _, l := range s.Links {
			if excluded[l.Type] {
				continue
			}
			d, ok := depth[l.From]
			if !ok {
				continue
			}
			if cur, ok := depth[l.To]; !ok || d+1 < cur {
				depth[l.To] = d + 1
				changed = true
			}
		}
	}
	for id := range reachable {
		if depth[id] > decl.Cascade.MaxDepth {
			return reject(ErrDepthExceeded)
		}
	}

	// 直接目标可见性。
	isTarget := map[InstanceID]bool{}
	for _, op := range inv.Ops {
		isTarget[op.Target] = true
		if op.Kind == OpModify && !auth.Visible(subject, op.Target) {
			return reject(ErrTargetInvisible)
		}
	}

	// 级联可见性。
	var effect, skipped []InstanceID
	for id := range reachable {
		if isTarget[id] {
			continue
		}
		if auth.Visible(subject, id) {
			effect = append(effect, id)
		} else if decl.Invisible == InvisibleDeny {
			return reject(ErrCascadeInvisible)
		} else {
			skipped = append(skipped, id)
		}
	}

	// 合并规则：朴素地计算全部结论再折叠（AND/OR 与顺序无关）。
	var grants []bool
	for _, op := range inv.Ops {
		grants = append(grants, auth.Authorize(subject, op.Target, depth[op.Target]))
	}
	for _, id := range effect {
		grants = append(grants, auth.Authorize(subject, id, depth[id]))
	}
	allowed := decl.Merge != MergeAny
	for _, g := range grants {
		if decl.Merge == MergeAll {
			allowed = allowed && g
		} else {
			allowed = allowed || g
		}
	}
	if !allowed {
		return RefResult{Allowed: false, Skipped: skipped, State: before}
	}

	// 应用：全量复制后逐个改写，最后统一推进版本、标记与时钟。
	after := s.Snapshot()
	after.Clock++
	after.AuditLen++
	for _, op := range inv.Ops {
		switch op.Kind {
		case OpCreate:
			props := map[string]string{}
			for k, v := range op.Props {
				props[k] = v
			}
			after.Instances[op.Target] = Instance{ID: op.Target, Type: op.Type, Props: props, Version: 1, CascadeMark: after.Clock}
		case OpModify:
			in := after.Instances[op.Target]
			for k, v := range op.Props {
				in.Props[k] = v
			}
			in.CascadeMark = after.Clock
			after.Instances[op.Target] = in
		}
	}
	for _, id := range effect {
		if _, ok := after.Instances[id]; !ok {
			continue
		}
		in := after.Instances[id]
		in.CascadeMark = after.Clock
		after.Instances[id] = in
	}
	for id, in := range after.Instances {
		if in.CascadeMark == after.Clock && !isCreateOp(inv, id) {
			in.Version++
			after.Instances[id] = in
		}
	}
	return RefResult{Allowed: true, Skipped: skipped, State: after}
}

// refValidate 返回 true 表示参数不合法。
func refValidate(s *Store, decl ActionDecl, inv Invocation) bool {
	if len(inv.Ops) == 0 || decl.Cascade.MaxDepth < 0 {
		return true
	}
	for _, lt := range decl.Cascade.ExcludeLinkTypes {
		if _, ok := s.LinkTypes[lt]; !ok {
			return true
		}
	}
	created := map[InstanceID]bool{}
	for _, op := range inv.Ops {
		ot, ok := s.Types[op.Type]
		if !ok || !decl.allows(op.Type, op.Kind) || op.Target == "" {
			return true
		}
		if op.Kind != OpCreate && op.Kind != OpModify {
			return true
		}
		for p := range op.Props {
			if !ot.HasProperty(p) {
				return true
			}
		}
		switch op.Kind {
		case OpCreate:
			if _, exists := s.Instances[op.Target]; exists || created[op.Target] {
				return true
			}
			created[op.Target] = true
		case OpModify:
			in, exists := s.Instances[op.Target]
			if !exists || in.Type != op.Type {
				return true
			}
		}
	}
	return false
}
