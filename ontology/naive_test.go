package ontology

import (
	"fmt"
)

// 本文件是独立实现的朴素逐条处理参考模型，仅用于测试对拍。
// 与优化实现刻意采用不同的机制：
//   - 不做拓扑排序，而是反复扫描条目直到没有可处理者（O(n^2) 轮次）；
//   - 批次内基数判定与所有先声明条目两两比较；
//   - 联合基数校验通过 ListLinks 遍历源实例的全部已有链接（O(已有链接数)）。
// 两者的判定规则（五类判定、优先级、回滚语义）必须完全一致。

// naiveDeps 收集条目引用的、确实存在于本批次的条目 ID。
func naiveDeps(e *Entry, byID map[string]*Entry) []string {
	var out []string
	add := func(ref Ref) {
		if ref.EntryID != "" {
			if _, ok := byID[ref.EntryID]; ok {
				out = append(out, ref.EntryID)
			}
		}
	}
	if e.Kind == EntryObject {
		for _, v := range e.Properties {
			if ref, ok := v.(Ref); ok {
				add(ref)
			}
		}
	} else {
		add(e.Source)
		add(e.Target)
	}
	return out
}

// naiveCanonicalSource 与优化实现使用同一规范化规则（共享平凡助手）。
func naiveCanonicalSource(ref Ref, byID map[string]*Entry) string {
	if ref.EntryID != "" {
		if dep, ok := byID[ref.EntryID]; ok {
			return "rid:" + assignedObjectRID(dep)
		}
		return "entry:" + ref.EntryID
	}
	return "rid:" + ref.ObjectRID
}

// naiveImport 朴素逐条处理模型。语义必须与 Importer.Import 完全一致。
func naiveImport(s *Store, req Request) Report {
	byID := make(map[string]*Entry, len(req.Entries))
	declIdx := make(map[string]int, len(req.Entries))
	for i := range req.Entries {
		e := &req.Entries[i]
		if e.ID == "" {
			return Report{Rejected: true, RejectReason: "条目 ID 为空"}
		}
		if _, dup := byID[e.ID]; dup {
			return Report{Rejected: true, RejectReason: "条目 ID 重复: " + e.ID}
		}
		byID[e.ID] = e
		declIdx[e.ID] = i
	}

	// 环检测：模拟可处理性不动点。若存在剩余条目，则引用成环，整体拒绝。
	// 朴素做法：每轮全量扫描所有条目，O(n^2)。
	simulated := map[string]bool{}
	for {
		progress := false
		for i := range req.Entries {
			id := req.Entries[i].ID
			if simulated[id] {
				continue
			}
			ready := true
			for _, dep := range naiveDeps(&req.Entries[i], byID) {
				if !simulated[dep] {
					ready = false
					break
				}
			}
			if ready {
				simulated[id] = true
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	if len(simulated) < len(req.Entries) {
		return Report{Rejected: true, RejectReason: "条目引用关系构成环，整体拒绝"}
	}

	// 逐条处理：同样以不动点方式保证被引用条目先处理。
	results := make(map[string]*EntryResult, len(req.Entries))
	var landed []landedOp
	for len(results) < len(req.Entries) {
		for i := range req.Entries {
			e := &req.Entries[i]
			if _, done := results[e.ID]; done {
				continue
			}
			ready := true
			for _, dep := range naiveDeps(e, byID) {
				if _, done := results[dep]; !done {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			s.mu.Lock()
			naiveProcessOne(s, req, byID, declIdx, results, &landed, e)
			s.mu.Unlock()
		}
	}

	// Atomic 模式：任一失败则按落地逆序回滚，逆操作失败继续并汇总。
	var rollbackFailures []RollbackFailure
	anyFailure := false
	for _, res := range results {
		if res.Verdict != VerdictSucceeded {
			anyFailure = true
			break
		}
	}
	if req.Mode == Atomic && anyFailure {
		s.mu.Lock()
		for i := len(landed) - 1; i >= 0; i-- {
			op := landed[i]
			var err error
			if op.kind == EntryLink {
				err = s.deleteLinkLocked(op.rid)
			} else {
				err = s.deleteObjectLocked(op.rid)
			}
			res := results[op.entryID]
			if err != nil {
				rollbackFailures = append(rollbackFailures, RollbackFailure{
					EntryID: op.entryID, RID: op.rid, Err: err.Error(),
				})
				continue
			}
			res.Verdict = VerdictRolledBack
			res.Reason = "曾成功落地，因请求内其它条目失败触发整体回滚而撤销"
		}
		s.mu.Unlock()
	}

	rep := Report{RolledBack: req.Mode == Atomic && anyFailure, RollbackFailures: rollbackFailures}
	for i := range req.Entries {
		rep.Results = append(rep.Results, *results[req.Entries[i].ID])
	}
	return rep
}

// naiveResolve 把引用解析为对象 RID。调用前需持锁。
func naiveResolve(s *Store, results map[string]*EntryResult, ref Ref) (string, error) {
	if ref.EntryID != "" {
		res, ok := results[ref.EntryID]
		if !ok || res.ObjectRID == "" || res.Verdict != VerdictSucceeded {
			return "", fmt.Errorf("条目引用 %s 无法解析", ref.EntryID)
		}
		return res.ObjectRID, nil
	}
	if ref.ObjectRID == "" {
		return "", fmt.Errorf("空引用")
	}
	if _, ok := s.objects[ref.ObjectRID]; !ok {
		return "", fmt.Errorf("对象 %s 不存在", ref.ObjectRID)
	}
	return ref.ObjectRID, nil
}

// naiveProcessOne 朴素处理单个条目，判定优先级与优化实现一致：
// 引用传播 > 批次内基数（两两比较）> 自身校验 > 联合基数（遍历已有链接）。
// 调用前需持锁。
func naiveProcessOne(s *Store, req Request, byID map[string]*Entry, declIdx map[string]int,
	results map[string]*EntryResult, landed *[]landedOp, e *Entry) {

	fail := func(v Verdict, reason string) {
		results[e.ID] = &EntryResult{EntryID: e.ID, Verdict: v, Reason: reason}
	}

	// 1. 引用传播
	for _, dep := range naiveDeps(e, byID) {
		if res := results[dep]; res != nil && res.Verdict != VerdictSucceeded {
			fail(VerdictPropagatedFailed, fmt.Sprintf("引用的同批次条目 %s 已失败", dep))
			return
		}
	}

	// 2. 批次内基数：与所有先声明条目两两比较（O(n^2)，刻意朴素）。
	if e.Kind == EntryLink {
		if lt, ok := s.linkTypes[e.LinkTypeRID]; ok && lt.MaxTargetsPerSource > 0 {
			myKey := naiveCanonicalSource(e.Source, byID)
			for j := range req.Entries {
				other := &req.Entries[j]
				if declIdx[other.ID] >= declIdx[e.ID] {
					break // 只比较声明顺序更早者（条目按声明顺序排列）
				}
				if other.Kind != EntryLink || other.LinkTypeRID != e.LinkTypeRID {
					continue
				}
				if naiveCanonicalSource(other.Source, byID) == myKey {
					fail(VerdictCardinalityConflict,
						"与声明顺序更早的条目 "+other.ID+" 基数冲突")
					return
				}
			}
		}
	}

	// 3. 自身校验 + 落地。
	if e.Kind == EntryObject {
		naiveProcessObject(s, results, landed, e, fail)
	} else {
		naiveProcessLink(s, results, landed, e, fail)
	}
}

func naiveProcessObject(s *Store, results map[string]*EntryResult, landed *[]landedOp,
	e *Entry, fail func(Verdict, string)) {
	ot, ok := s.objectTypes[e.ObjectTypeRID]
	if !ok {
		fail(VerdictValidationFailed, "未知对象类型 "+e.ObjectTypeRID)
		return
	}
	rid := assignedObjectRID(e)
	if _, exists := s.objects[rid]; exists {
		fail(VerdictValidationFailed, "对象 RID 已存在: "+rid)
		return
	}
	materialized := make(map[string]any, len(e.Properties))
	for name, v := range e.Properties {
		spec, known := ot.spec(name)
		if !known {
			fail(VerdictValidationFailed, "未声明属性 "+name)
			return
		}
		switch spec.Type {
		case PropString:
			if _, ok := v.(string); !ok {
				fail(VerdictValidationFailed, "属性 "+name+" 应为字符串")
				return
			}
			materialized[name] = v
		case PropInt:
			if _, ok := v.(int); !ok {
				fail(VerdictValidationFailed, "属性 "+name+" 应为整数")
				return
			}
			materialized[name] = v
		case PropObjectRef:
			ref, ok := v.(Ref)
			if !ok {
				fail(VerdictValidationFailed, "属性 "+name+" 应为对象引用")
				return
			}
			targetRID, err := naiveResolve(s, results, ref)
			if err != nil {
				fail(VerdictValidationFailed, "属性 "+name+" 引用解析失败: "+err.Error())
				return
			}
			if spec.RefObjectType != "" && s.objects[targetRID].TypeRID != spec.RefObjectType {
				fail(VerdictValidationFailed, "属性 "+name+" 引用对象类型不符")
				return
			}
			materialized[name] = targetRID
		}
		if spec.Validate != nil {
			if err := spec.Validate(v); err != nil {
				fail(VerdictValidationFailed, "属性 "+name+" 校验失败: "+err.Error())
				return
			}
		}
	}
	for _, spec := range ot.Properties {
		if spec.Required {
			if _, present := e.Properties[spec.Name]; !present {
				fail(VerdictValidationFailed, "缺少必填属性 "+spec.Name)
				return
			}
		}
	}
	s.objects[rid] = &ObjectInstance{RID: rid, TypeRID: ot.RID, Properties: materialized}
	results[e.ID] = &EntryResult{EntryID: e.ID, Verdict: VerdictSucceeded,
		Reason: "校验通过，对象已落地", ObjectRID: rid}
	*landed = append(*landed, landedOp{entryID: e.ID, rid: rid, kind: EntryObject})
}

func naiveProcessLink(s *Store, results map[string]*EntryResult, landed *[]landedOp,
	e *Entry, fail func(Verdict, string)) {
	lt, ok := s.linkTypes[e.LinkTypeRID]
	if !ok {
		fail(VerdictValidationFailed, "未知链接类型 "+e.LinkTypeRID)
		return
	}
	srcRID, err := naiveResolve(s, results, e.Source)
	if err != nil {
		fail(VerdictValidationFailed, "源端点解析失败: "+err.Error())
		return
	}
	tgtRID, err := naiveResolve(s, results, e.Target)
	if err != nil {
		fail(VerdictValidationFailed, "目标端点解析失败: "+err.Error())
		return
	}
	if lt.SourceObjectType != "" && s.objects[srcRID].TypeRID != lt.SourceObjectType {
		fail(VerdictValidationFailed, "源端点类型不符")
		return
	}
	if lt.TargetObjectType != "" && s.objects[tgtRID].TypeRID != lt.TargetObjectType {
		fail(VerdictValidationFailed, "目标端点类型不符")
		return
	}
	// 联合基数校验：朴素地遍历源实例的全部已有链接（O(已有链接数)）。
	if lt.MaxTargetsPerSource > 0 {
		existing := 0
		for _, l := range s.listLinksLocked(srcRID) {
			if l.TypeRID == lt.RID {
				existing++
			}
		}
		if existing+1 > lt.MaxTargetsPerSource {
			fail(VerdictCardinalityConflict, "源实例已有链接达到基数上限")
			return
		}
	}
	rid := "lnk-" + e.ID
	if _, exists := s.links[rid]; exists {
		fail(VerdictValidationFailed, "链接 RID 已存在: "+rid)
		return
	}
	s.links[rid] = &LinkInstance{RID: rid, TypeRID: lt.RID, SourceRID: srcRID, TargetRID: tgtRID}
	s.indexAddLocked(srcRID, lt.RID, tgtRID)
	results[e.ID] = &EntryResult{EntryID: e.ID, Verdict: VerdictSucceeded,
		Reason: "校验通过，链接已落地", LinkRID: rid}
	*landed = append(*landed, landedOp{entryID: e.ID, rid: rid, kind: EntryLink})
}
