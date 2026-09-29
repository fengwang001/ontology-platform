package ontology

import "fmt"

// Rename 将对象类型 oldName 原子重命名为 newName。
//
// 流程：前置校验 -> 在快照副本上重写全图引用 -> 校验候选状态 ->
// 一次性整体提交 -> 提交后不变量复检（失败即整体回滚）。
// 任意一步失败都不会改变注册表当前状态，不会产生半改状态。
func (r *Registry) Rename(oldName, newName string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	logger := r.logger.With("op", "rename", "old", oldName, "new", newName)
	logger.Info("开始重命名")

	// 1. 前置校验：旧名必须已注册，新名必须可用。
	if _, ok := r.types[oldName]; !ok {
		if _, inv := r.invalidated[oldName]; inv {
			logger.Warn("拒绝重命名", "code", CodeOldNameInvalidated, "依据", "旧名已因之前的重命名失效")
			return newError(CodeOldNameInvalidated, oldName, "旧名已因之前的重命名失效，不可再次重命名")
		}
		logger.Warn("拒绝重命名", "code", CodeTypeNotFound, "依据", "旧名对应的类型未注册")
		return newError(CodeTypeNotFound, oldName, "待重命名的类型未注册")
	}
	if oldName == newName {
		logger.Warn("拒绝重命名", "code", CodeNameConflict, "依据", "新名与旧名相同")
		return newError(CodeNameConflict, newName, "新名与旧名相同")
	}
	if _, ok := r.types[newName]; ok {
		logger.Warn("拒绝重命名", "code", CodeNameConflict, "依据", "新名已被其它类型占用")
		return newError(CodeNameConflict, newName, "新名已被其它类型占用")
	}
	if _, inv := r.invalidated[newName]; inv {
		logger.Warn("拒绝重命名", "code", CodeOldNameInvalidated, "依据", "新名是已失效的历史旧名")
		return newError(CodeOldNameInvalidated, newName, "新名是已失效的历史旧名，不可复用")
	}

	// 2. 在快照副本上应用重命名并重写全图引用，原状态不被触碰。
	prevTypes := r.types
	next := r.snapshot()
	renamed := next[oldName]
	delete(next, oldName)
	renamed.Name = newName
	next[newName] = renamed

	refs := referencesLocked(next, oldName)
	for _, ref := range refs {
		logger.Info("更新引用", "holder", ref.HolderType, "kind", ref.Kind,
			"location", ref.Location, "from", oldName, "to", newName)
	}
	rewriteReferences(next, oldName, newName)

	// 测试钩子：模拟重命名中断造成的状态损坏，验证校验与回滚路径。
	if r.testCorruptCandidate != nil {
		r.testCorruptCandidate(next)
	}

	// 3. 校验候选状态：无残留旧名引用、旧名不可解析、无悬空引用。
	if residual := referencesLocked(next, oldName); len(residual) > 0 {
		logger.Warn("拒绝重命名", "code", CodeResidualReference, "依据", residualReason(residual))
		return newError(CodeResidualReference, oldName, residualReason(residual))
	}
	if _, ok := next[oldName]; ok {
		logger.Warn("拒绝重命名", "code", CodeOldNameResolvable, "依据", "候选状态中旧名仍可解析到原类型")
		return newError(CodeOldNameResolvable, oldName, "候选状态中旧名仍可解析到原类型")
	}
	if dangling := danglingReferences(next); len(dangling) > 0 {
		logger.Warn("拒绝重命名", "code", CodeDanglingReference, "依据", danglingReason(dangling))
		return newError(CodeDanglingReference, newName, danglingReason(dangling))
	}

	// 4. 一次性整体提交：单个 map 赋值，读者要么看到旧快照，要么看到新快照。
	r.types = next
	r.invalidated[oldName] = struct{}{}

	// 测试钩子：模拟提交后才发现的中断/损坏。
	if r.testCorruptCommitted != nil {
		r.testCorruptCommitted(r.types)
	}

	// 5. 提交后不变量复检：任何破坏都触发整体回滚。
	if verr := r.verifyInvariantsLocked(oldName); verr != nil {
		r.types = prevTypes
		delete(r.invalidated, oldName)
		logger.Error("重命名中断，已整体回滚", "code", CodeRenameAborted, "依据", verr.Reason)
		return newError(CodeRenameAborted, oldName, "重命名中断："+verr.Reason+"，已整体回滚")
	}

	logger.Info("重命名完成", "type", newName, "更新引用数", len(refs))
	return nil
}

// rewriteReferences 将图中所有对 oldName 的引用改写为 newName。
func rewriteReferences(types map[string]*ObjectType, oldName, newName string) {
	for _, t := range types {
		for i := range t.Properties {
			if t.Properties[i].Type == PrimitiveObjectRef && t.Properties[i].RefType == oldName {
				t.Properties[i].RefType = newName
			}
		}
		for i := range t.Links {
			if t.Links[i].SourceType == oldName {
				t.Links[i].SourceType = newName
			}
			if t.Links[i].TargetType == oldName {
				t.Links[i].TargetType = newName
			}
		}
		for ai := range t.Actions {
			a := &t.Actions[ai]
			for i := range a.Params {
				if a.Params[i].TypeRef == oldName {
					a.Params[i].TypeRef = newName
				}
			}
			for i := range a.ReturnRefs {
				if a.ReturnRefs[i] == oldName {
					a.ReturnRefs[i] = newName
				}
			}
			for i := range a.PropertyRef {
				if a.PropertyRef[i].TypeName == oldName {
					a.PropertyRef[i].TypeName = newName
				}
			}
		}
	}
}

// danglingReferences 返回图中所有指向不存在类型的引用。
func danglingReferences(types map[string]*ObjectType) []Reference {
	var out []Reference
	for _, t := range types {
		check := func(kind, location, refName string) {
			if refName == "" {
				return
			}
			if _, ok := types[refName]; !ok {
				out = append(out, Reference{HolderType: t.Name, Kind: kind, Location: location, RefName: refName})
			}
		}
		for _, p := range t.Properties {
			if p.Type == PrimitiveObjectRef {
				check("property_ref", "property:"+p.Name, p.RefType)
			}
		}
		for _, l := range t.Links {
			check("link_source", "link:"+l.Name, l.SourceType)
			check("link_target", "link:"+l.Name, l.TargetType)
		}
		for _, a := range t.Actions {
			for _, param := range a.Params {
				check("action_param", "action:"+a.Name+"/param:"+param.Name, param.TypeRef)
			}
			for _, ret := range a.ReturnRefs {
				check("action_return", "action:"+a.Name, ret)
			}
			for _, pr := range a.PropertyRef {
				check("action_property_ref", "action:"+a.Name+"/prop:"+pr.PropertyName, pr.TypeName)
			}
		}
	}
	return out
}

// verifyInvariantsLocked 校验已提交状态的不变量：
// 旧名不可解析、无残留旧名引用、无悬空引用。
func (r *Registry) verifyInvariantsLocked(oldName string) *Error {
	if _, ok := r.types[oldName]; ok {
		return newError(CodeOldNameResolvable, oldName, "提交后旧名仍可解析到原类型")
	}
	if residual := referencesLocked(r.types, oldName); len(residual) > 0 {
		return newError(CodeResidualReference, oldName, residualReason(residual))
	}
	if dangling := danglingReferences(r.types); len(dangling) > 0 {
		return newError(CodeDanglingReference, oldName, danglingReason(dangling))
	}
	return nil
}

func residualReason(refs []Reference) string {
	return fmt.Sprintf("仍残留 %d 处旧名引用：%s", len(refs), describeRef(refs[0]))
}

func danglingReason(refs []Reference) string {
	return fmt.Sprintf("存在 %d 处悬空引用：%s", len(refs), describeRef(refs[0]))
}

func describeRef(ref Reference) string {
	return fmt.Sprintf("%s/%s/%s -> %s", ref.HolderType, ref.Kind, ref.Location, ref.RefName)
}
