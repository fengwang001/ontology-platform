package replay

import (
	"fmt"
	"sort"
)

// 本文件实现朴素参照模型：与 Verifier 遵循同一套判定规则与固定
// 优先级，但以最直白的方式实现——完整物化工作状态、全表扫描做
// 引用/约束检查、直接比较，不使用覆盖层与快照索引。
// 它用于随机对照测试，与主实现交叉验证判定结果。

// ReferenceVerify 是朴素参照实现，语义与 Verifier.Verify 完全一致。
// 它不修改 snap 与 log。
func ReferenceVerify(snap *Snapshot, log *DeltaLog) Result {
	if log == nil {
		return Result{Verdict: VerdictInconsistentDelta, ChangeIndex: -1,
			Reason: "差异记录为空"}
	}
	if snap == nil {
		return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: -1,
			Reason: "良好快照缺失（前提环境不存在）"}
	}

	// 阶段 1：差异记录自身自洽性校验（只用快照结构层）。
	if r := refCheckDelta(snap, log); r != nil {
		return *r
	}

	if !indexesComplete(snap.Indexes) {
		return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: -1,
			Reason: "良好快照缺少约束/引用完整性索引（前提环境损坏）"}
	}

	// 阶段 2：在完整物化的副本上逐条重放。
	st := stateFromSnapshot(snap)
	for i := range log.Changes {
		if reason := refApply(st, &log.Changes[i]); reason != "" {
			return Result{Verdict: VerdictPreconditionFailed, ChangeIndex: i, Reason: reason}
		}
	}

	// 阶段 3：与声明目标逐项核对。
	mismatches := refCompare(st, log)
	if len(mismatches) > 0 {
		return Result{Verdict: VerdictNotEquivalent, ChangeIndex: -1,
			Reason:     "重放结果与差异记录声明的目标状态不等价",
			Mismatches: mismatches,
		}
	}
	return Result{Verdict: VerdictEquivalent, ChangeIndex: -1,
		Reason: "差异记录自洽、前提环境满足、重放结果与声明目标等价"}
}

// --- 参照模型：阶段 1 ---

// refSim 是参照模型的自洽性模拟状态。
type refSim struct {
	schema           Schema
	objects          map[string]ObjectInstance
	removed          map[string]bool
	links            map[LinkKey]LinkInstance
	removedLinks     map[LinkKey]bool
	linkSourceCounts map[LinkSourceKey]int
}

func refCheckDelta(snap *Snapshot, log *DeltaLog) *Result {
	sim := &refSim{
		schema:           cloneSchema(snap.Schema),
		objects:          map[string]ObjectInstance{},
		removed:          map[string]bool{},
		links:            map[LinkKey]LinkInstance{},
		removedLinks:     map[LinkKey]bool{},
		linkSourceCounts: map[LinkSourceKey]int{},
	}
	for i := range log.Changes {
		if r := sim.step(i, &log.Changes[i]); r != nil {
			return r
		}
	}
	return nil
}

func (sim *refSim) objectShapeOK(i int, verb string, obj ObjectInstance) *Result {
	def, ok := sim.schema.ObjectTypes[obj.ObjectType]
	if !ok {
		return inconsistent(i, "%s: 对象类型 %q 未声明", verb, obj.ObjectType)
	}
	for p := range obj.Properties {
		if _, ok := def.Properties[p]; !ok {
			return inconsistent(i, "%s: 属性 %q 未在类型 %q 上声明", verb, p, obj.ObjectType)
		}
	}
	return nil
}

// step 校验并应用一条变化（参照模型，逐条独立判定）。
func (sim *refSim) step(i int, c *Change) *Result {
	switch c.Kind {
	case ChangeAddObjectType:
		if c.TypeID == "" {
			return inconsistent(i, "AddObjectType 缺少类型 ID")
		}
		if _, ok := sim.schema.ObjectTypes[c.TypeID]; ok {
			return inconsistent(i, "AddObjectType: 对象类型 %q 已存在", c.TypeID)
		}
		if c.ObjectTypeAfter.TypeID != c.TypeID {
			return inconsistent(i, "AddObjectType: 内嵌定义的类型 ID %q 与声明的 %q 不一致",
				c.ObjectTypeAfter.TypeID, c.TypeID)
		}
		sim.schema.ObjectTypes[c.TypeID] = cloneObjectTypeDef(c.ObjectTypeAfter)

	case ChangeRemoveObjectType:
		if c.TypeID == "" {
			return inconsistent(i, "RemoveObjectType 缺少类型 ID")
		}
		if _, ok := sim.schema.ObjectTypes[c.TypeID]; !ok {
			return inconsistent(i, "RemoveObjectType: 对象类型 %q 不存在", c.TypeID)
		}
		for _, lt := range sim.schema.LinkTypes {
			if lt.SourceType == c.TypeID || lt.TargetType == c.TypeID {
				return inconsistent(i, "RemoveObjectType: 链接类型 %q 仍引用对象类型 %q",
					lt.TypeID, c.TypeID)
			}
		}
		for _, obj := range sim.objects {
			if obj.ObjectType == c.TypeID {
				return inconsistent(i, "RemoveObjectType: 差异记录自身创建的对象 %q 仍为类型 %q",
					obj.ObjectID, c.TypeID)
			}
		}
		delete(sim.schema.ObjectTypes, c.TypeID)

	case ChangeAddProperty:
		if c.TypeID == "" || c.Property == "" {
			return inconsistent(i, "AddProperty 缺少类型 ID 或属性名")
		}
		def, ok := sim.schema.ObjectTypes[c.TypeID]
		if !ok {
			return inconsistent(i, "AddProperty: 对象类型 %q 不存在", c.TypeID)
		}
		if _, ok := def.Properties[c.Property]; ok {
			return inconsistent(i, "AddProperty: 类型 %q 已声明属性 %q", c.TypeID, c.Property)
		}
		def.Properties[c.Property] = struct{}{}
		sim.schema.ObjectTypes[c.TypeID] = def

	case ChangeRemoveProperty:
		if c.TypeID == "" || c.Property == "" {
			return inconsistent(i, "RemoveProperty 缺少类型 ID 或属性名")
		}
		def, ok := sim.schema.ObjectTypes[c.TypeID]
		if !ok {
			return inconsistent(i, "RemoveProperty: 对象类型 %q 不存在", c.TypeID)
		}
		if _, ok := def.Properties[c.Property]; !ok {
			return inconsistent(i, "RemoveProperty: 类型 %q 未声明属性 %q", c.TypeID, c.Property)
		}
		for _, obj := range sim.objects {
			if obj.ObjectType != c.TypeID {
				continue
			}
			if _, has := obj.Properties[c.Property]; has {
				return inconsistent(i, "RemoveProperty: 差异记录自身产生的对象 %q 仍持有属性 %q",
					obj.ObjectID, c.Property)
			}
		}
		delete(def.Properties, c.Property)
		sim.schema.ObjectTypes[c.TypeID] = def

	case ChangeAddLinkType:
		if c.LinkTypeID == "" {
			return inconsistent(i, "AddLinkType 缺少链接类型 ID")
		}
		if _, ok := sim.schema.LinkTypes[c.LinkTypeID]; ok {
			return inconsistent(i, "AddLinkType: 链接类型 %q 已存在", c.LinkTypeID)
		}
		if c.LinkTypeAfter.TypeID != c.LinkTypeID {
			return inconsistent(i, "AddLinkType: 内嵌定义的类型 ID %q 与声明的 %q 不一致",
				c.LinkTypeAfter.TypeID, c.LinkTypeID)
		}
		if _, ok := sim.schema.ObjectTypes[c.LinkTypeAfter.SourceType]; !ok {
			return inconsistent(i, "AddLinkType: 源端对象类型 %q 不存在", c.LinkTypeAfter.SourceType)
		}
		if _, ok := sim.schema.ObjectTypes[c.LinkTypeAfter.TargetType]; !ok {
			return inconsistent(i, "AddLinkType: 目标端对象类型 %q 不存在", c.LinkTypeAfter.TargetType)
		}
		sim.schema.LinkTypes[c.LinkTypeID] = cloneLinkTypeDef(c.LinkTypeAfter)

	case ChangeRemoveLinkType:
		if c.LinkTypeID == "" {
			return inconsistent(i, "RemoveLinkType 缺少链接类型 ID")
		}
		if _, ok := sim.schema.LinkTypes[c.LinkTypeID]; !ok {
			return inconsistent(i, "RemoveLinkType: 链接类型 %q 不存在", c.LinkTypeID)
		}
		for _, l := range sim.links {
			if l.LinkTypeID == c.LinkTypeID {
				return inconsistent(i, "RemoveLinkType: 差异记录自身创建的链接 %v 仍为类型 %q",
					l.Key(), c.LinkTypeID)
			}
		}
		delete(sim.schema.LinkTypes, c.LinkTypeID)

	case ChangeAddLinkTypeConstraint:
		if c.LinkTypeID == "" || c.Constraint == "" {
			return inconsistent(i, "AddLinkTypeConstraint 缺少链接类型 ID 或约束名")
		}
		def, ok := sim.schema.LinkTypes[c.LinkTypeID]
		if !ok {
			return inconsistent(i, "AddLinkTypeConstraint: 链接类型 %q 不存在", c.LinkTypeID)
		}
		if _, ok := def.Constraints[c.Constraint]; ok {
			return inconsistent(i, "AddLinkTypeConstraint: 链接类型 %q 已存在约束 %q",
				c.LinkTypeID, c.Constraint)
		}
		def.Constraints[c.Constraint] = c.ConstraintValue
		sim.schema.LinkTypes[c.LinkTypeID] = def

	case ChangeRemoveLinkTypeConstraint:
		if c.LinkTypeID == "" || c.Constraint == "" {
			return inconsistent(i, "RemoveLinkTypeConstraint 缺少链接类型 ID 或约束名")
		}
		def, ok := sim.schema.LinkTypes[c.LinkTypeID]
		if !ok {
			return inconsistent(i, "RemoveLinkTypeConstraint: 链接类型 %q 不存在", c.LinkTypeID)
		}
		if _, ok := def.Constraints[c.Constraint]; !ok {
			return inconsistent(i, "RemoveLinkTypeConstraint: 链接类型 %q 不存在约束 %q",
				c.LinkTypeID, c.Constraint)
		}
		delete(def.Constraints, c.Constraint)
		sim.schema.LinkTypes[c.LinkTypeID] = def

	default:
		return sim.stepInstance(i, c)
	}
	return nil
}

// stepInstance 处理实例层变化（参照模型）。
func (sim *refSim) stepInstance(i int, c *Change) *Result {
	switch c.Kind {
	case ChangeAddObject:
		obj := c.ObjectAfter
		if obj.ObjectID == "" {
			return inconsistent(i, "AddObject 缺少对象 ID")
		}
		if _, ok := sim.objects[obj.ObjectID]; ok {
			return inconsistent(i, "AddObject: 对象 %q 已被差异记录自身添加", obj.ObjectID)
		}
		if r := sim.objectShapeOK(i, "AddObject", obj); r != nil {
			return r
		}
		sim.objects[obj.ObjectID] = cloneObject(obj)
		delete(sim.removed, obj.ObjectID)

	case ChangeUpdateObject:
		before, after := c.ObjectBefore, c.ObjectAfter
		if before.ObjectID == "" {
			return inconsistent(i, "UpdateObject 缺少对象 ID")
		}
		if before.ObjectID != after.ObjectID {
			return inconsistent(i, "UpdateObject: 变化前对象 ID %q 与变化后 %q 不一致",
				before.ObjectID, after.ObjectID)
		}
		if before.ObjectType != after.ObjectType {
			return inconsistent(i, "UpdateObject: 不支持通过更新变更对象类型（%q -> %q），应先删后建",
				before.ObjectType, after.ObjectType)
		}
		if r := sim.objectShapeOK(i, "UpdateObject(before)", before); r != nil {
			return r
		}
		if r := sim.objectShapeOK(i, "UpdateObject(after)", after); r != nil {
			return r
		}
		if sim.removed[before.ObjectID] {
			return inconsistent(i, "UpdateObject: 对象 %q 已被差异记录自身删除", before.ObjectID)
		}
		if cur, ok := sim.objects[before.ObjectID]; ok && !objectEqual(cur, before) {
			return inconsistent(i, "UpdateObject: 对象 %q 的变化前状态与差异记录自身此前产生的状态不一致",
				before.ObjectID)
		}
		sim.objects[after.ObjectID] = cloneObject(after)

	case ChangeRemoveObject:
		before := c.ObjectBefore
		if before.ObjectID == "" {
			return inconsistent(i, "RemoveObject 缺少对象 ID")
		}
		if r := sim.objectShapeOK(i, "RemoveObject", before); r != nil {
			return r
		}
		if sim.removed[before.ObjectID] {
			return inconsistent(i, "RemoveObject: 对象 %q 已被差异记录自身删除", before.ObjectID)
		}
		if cur, ok := sim.objects[before.ObjectID]; ok && !objectEqual(cur, before) {
			return inconsistent(i, "RemoveObject: 对象 %q 的变化前状态与差异记录自身此前产生的状态不一致",
				before.ObjectID)
		}
		for _, l := range sim.links {
			if l.SourceID == before.ObjectID || l.TargetID == before.ObjectID {
				return inconsistent(i, "RemoveObject: 差异记录自身创建的链接 %v 仍引用对象 %q",
					l.Key(), before.ObjectID)
			}
		}
		delete(sim.objects, before.ObjectID)
		sim.removed[before.ObjectID] = true

	case ChangeAddLink:
		l := c.LinkAfter
		if l.LinkTypeID == "" || l.SourceID == "" || l.TargetID == "" {
			return inconsistent(i, "AddLink 缺少链接类型 ID 或端点对象 ID")
		}
		lt, ok := sim.schema.LinkTypes[l.LinkTypeID]
		if !ok {
			return inconsistent(i, "AddLink: 链接类型 %q 未声明", l.LinkTypeID)
		}
		key := l.Key()
		if _, ok := sim.links[key]; ok {
			return inconsistent(i, "AddLink: 链接 %v 已被差异记录自身添加", key)
		}
		for _, ep := range []struct {
			id         string
			expectType string
		}{{l.SourceID, lt.SourceType}, {l.TargetID, lt.TargetType}} {
			if sim.removed[ep.id] {
				return inconsistent(i, "AddLink: 端点对象 %q 已被差异记录自身删除", ep.id)
			}
			if obj, ok := sim.objects[ep.id]; ok && obj.ObjectType != ep.expectType {
				return inconsistent(i, "AddLink: 端点对象 %q 类型为 %q，与链接类型要求的 %q 不符",
					ep.id, obj.ObjectType, ep.expectType)
			}
		}
		if isUniqueSource(lt) {
			sk := LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}
			if sim.linkSourceCounts[sk] > 0 {
				return inconsistent(i, "AddLink: 链接 %v 违反链接类型 %q 的 uniqueSource 约束（与差异记录自身此前的链接冲突）",
					key, l.LinkTypeID)
			}
		}
		sim.links[key] = l
		delete(sim.removedLinks, key)
		sim.linkSourceCounts[LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}]++

	case ChangeRemoveLink:
		l := c.LinkBefore
		if l.LinkTypeID == "" || l.SourceID == "" || l.TargetID == "" {
			return inconsistent(i, "RemoveLink 缺少链接类型 ID 或端点对象 ID")
		}
		if _, ok := sim.schema.LinkTypes[l.LinkTypeID]; !ok {
			return inconsistent(i, "RemoveLink: 链接类型 %q 未声明", l.LinkTypeID)
		}
		key := l.Key()
		if sim.removedLinks[key] {
			return inconsistent(i, "RemoveLink: 链接 %v 已被差异记录自身删除", key)
		}
		if cur, ok := sim.links[key]; ok {
			if cur != l {
				return inconsistent(i, "RemoveLink: 链接 %v 的变化前状态与差异记录自身此前产生的不一致", key)
			}
			delete(sim.links, key)
			sim.linkSourceCounts[LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}]--
		}
		sim.removedLinks[key] = true

	default:
		return inconsistent(i, "未知的变化种类 %d", int(c.Kind))
	}
	return nil
}

// --- 参照模型：阶段 2（物化状态 + 全表扫描） ---

// refApply 在物化状态上应用一条变化；返回空串表示成功。
// 所有引用/约束检查都直接全表扫描，不使用任何索引。
func refApply(st *State, c *Change) string {
	switch c.Kind {
	case ChangeAddObjectType:
		st.Schema.ObjectTypes[c.TypeID] = cloneObjectTypeDef(c.ObjectTypeAfter)
		return ""
	case ChangeRemoveObjectType:
		n := 0
		for _, obj := range st.Objects {
			if obj.ObjectType == c.TypeID {
				n++
			}
		}
		if n > 0 {
			return fmt.Sprintf("RemoveObjectType: 快照中仍存在 %d 个类型 %q 的对象", n, c.TypeID)
		}
		delete(st.Schema.ObjectTypes, c.TypeID)
		return ""
	case ChangeAddProperty:
		def := st.Schema.ObjectTypes[c.TypeID]
		def.Properties[c.Property] = struct{}{}
		st.Schema.ObjectTypes[c.TypeID] = def
		return ""
	case ChangeRemoveProperty:
		n := 0
		for _, obj := range st.Objects {
			if obj.ObjectType != c.TypeID {
				continue
			}
			if _, has := obj.Properties[c.Property]; has {
				n++
			}
		}
		if n > 0 {
			return fmt.Sprintf("RemoveProperty: 快照中仍有 %d 个类型 %q 的对象持有属性 %q",
				n, c.TypeID, c.Property)
		}
		def := st.Schema.ObjectTypes[c.TypeID]
		delete(def.Properties, c.Property)
		st.Schema.ObjectTypes[c.TypeID] = def
		return ""
	case ChangeAddLinkType:
		st.Schema.LinkTypes[c.LinkTypeID] = cloneLinkTypeDef(c.LinkTypeAfter)
		return ""
	case ChangeRemoveLinkType:
		n := 0
		for _, l := range st.Links {
			if l.LinkTypeID == c.LinkTypeID {
				n++
			}
		}
		if n > 0 {
			return fmt.Sprintf("RemoveLinkType: 快照中仍存在 %d 条类型 %q 的链接", n, c.LinkTypeID)
		}
		delete(st.Schema.LinkTypes, c.LinkTypeID)
		return ""
	case ChangeAddLinkTypeConstraint:
		def := st.Schema.LinkTypes[c.LinkTypeID]
		def.Constraints[c.Constraint] = c.ConstraintValue
		st.Schema.LinkTypes[c.LinkTypeID] = def
		return ""
	case ChangeRemoveLinkTypeConstraint:
		def := st.Schema.LinkTypes[c.LinkTypeID]
		delete(def.Constraints, c.Constraint)
		st.Schema.LinkTypes[c.LinkTypeID] = def
		return ""
	case ChangeAddObject:
		obj := c.ObjectAfter
		if _, ok := st.Objects[obj.ObjectID]; ok {
			return fmt.Sprintf("AddObject: 对象 %q 在快照中已存在", obj.ObjectID)
		}
		st.Objects[obj.ObjectID] = cloneObject(obj)
		return ""
	case ChangeUpdateObject:
		before := c.ObjectBefore
		cur, ok := st.Objects[before.ObjectID]
		if !ok {
			return fmt.Sprintf("UpdateObject: 对象 %q 在快照中不存在", before.ObjectID)
		}
		if !objectEqual(cur, before) {
			return fmt.Sprintf("UpdateObject: 对象 %q 的变化前状态与快照不一致", before.ObjectID)
		}
		st.Objects[before.ObjectID] = cloneObject(c.ObjectAfter)
		return ""
	case ChangeRemoveObject:
		before := c.ObjectBefore
		cur, ok := st.Objects[before.ObjectID]
		if !ok {
			return fmt.Sprintf("RemoveObject: 对象 %q 在快照中不存在", before.ObjectID)
		}
		if !objectEqual(cur, before) {
			return fmt.Sprintf("RemoveObject: 对象 %q 的变化前状态与快照不一致", before.ObjectID)
		}
		n := 0
		for _, l := range st.Links {
			if l.SourceID == before.ObjectID || l.TargetID == before.ObjectID {
				n++
			}
		}
		if n > 0 {
			return fmt.Sprintf("RemoveObject: 快照中仍有 %d 条链接引用对象 %q", n, before.ObjectID)
		}
		delete(st.Objects, before.ObjectID)
		return ""
	case ChangeAddLink:
		l := c.LinkAfter
		lt, ok := st.Schema.LinkTypes[l.LinkTypeID]
		if !ok {
			return fmt.Sprintf("AddLink: 链接类型 %q 不存在", l.LinkTypeID)
		}
		if _, ok := st.Links[l.Key()]; ok {
			return fmt.Sprintf("AddLink: 链接 %v 在快照中已存在", l.Key())
		}
		src, ok := st.Objects[l.SourceID]
		if !ok {
			return fmt.Sprintf("AddLink: 源对象 %q 在快照中不存在", l.SourceID)
		}
		if src.ObjectType != lt.SourceType {
			return fmt.Sprintf("AddLink: 源对象 %q 类型 %q 与链接类型 %q 要求的 %q 不符",
				l.SourceID, src.ObjectType, l.LinkTypeID, lt.SourceType)
		}
		dst, ok := st.Objects[l.TargetID]
		if !ok {
			return fmt.Sprintf("AddLink: 目标对象 %q 在快照中不存在", l.TargetID)
		}
		if dst.ObjectType != lt.TargetType {
			return fmt.Sprintf("AddLink: 目标对象 %q 类型 %q 与链接类型 %q 要求的 %q 不符",
				l.TargetID, dst.ObjectType, l.LinkTypeID, lt.TargetType)
		}
		if isUniqueSource(lt) {
			for _, existing := range st.Links {
				if existing.LinkTypeID == l.LinkTypeID && existing.SourceID == l.SourceID {
					return fmt.Sprintf("AddLink: 链接 %v 违反链接类型 %q 的 uniqueSource 约束",
						l.Key(), l.LinkTypeID)
				}
			}
		}
		st.Links[l.Key()] = l
		return ""
	case ChangeRemoveLink:
		l := c.LinkBefore
		cur, ok := st.Links[l.Key()]
		if !ok {
			return fmt.Sprintf("RemoveLink: 链接 %v 在快照中不存在", l.Key())
		}
		if cur != l {
			return fmt.Sprintf("RemoveLink: 链接 %v 的变化前状态与快照不一致", l.Key())
		}
		delete(st.Links, l.Key())
		return ""
	}
	return fmt.Sprintf("未知的变化种类 %d", int(c.Kind))
}

// --- 参照模型：阶段 3（直接逐项核对） ---

// refCompare 朴素核对：目标每一项逐一比对，并用长度核对多余项。
// 报告约定与主实现一致（计数不符只报一条计数项）。
func refCompare(st *State, log *DeltaLog) []Mismatch {
	var out []Mismatch

	if len(st.Schema.ObjectTypes) != len(log.TargetSchema.ObjectTypes) {
		out = append(out, Mismatch{Category: CategorySchema, Key: "(objectTypeCount)",
			Detail: fmt.Sprintf("对象类型总数不一致: 重放结果 %d, 声明目标 %d",
				len(st.Schema.ObjectTypes), len(log.TargetSchema.ObjectTypes))})
	}
	for id, want := range log.TargetSchema.ObjectTypes {
		got, ok := st.Schema.ObjectTypes[id]
		if !ok {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "重放结果缺少声明目标中的对象类型"})
			continue
		}
		if !objectTypeEqual(got, want) {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "对象类型定义与声明目标不一致"})
		}
	}

	if len(st.Schema.LinkTypes) != len(log.TargetSchema.LinkTypes) {
		out = append(out, Mismatch{Category: CategorySchema, Key: "(linkTypeCount)",
			Detail: fmt.Sprintf("链接类型总数不一致: 重放结果 %d, 声明目标 %d",
				len(st.Schema.LinkTypes), len(log.TargetSchema.LinkTypes))})
	}
	for id, want := range log.TargetSchema.LinkTypes {
		got, ok := st.Schema.LinkTypes[id]
		if !ok {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "重放结果缺少声明目标中的链接类型"})
			continue
		}
		if !linkTypeEqual(got, want) {
			out = append(out, Mismatch{Category: CategorySchema, Key: id,
				Detail: "链接类型定义与声明目标不一致"})
		}
	}

	if len(st.Objects) != len(log.TargetObjects) {
		out = append(out, Mismatch{Category: CategoryObject, Key: "(count)",
			Detail: fmt.Sprintf("对象总数不一致: 重放结果 %d, 声明目标 %d",
				len(st.Objects), len(log.TargetObjects))})
	}
	for id, want := range log.TargetObjects {
		got, ok := st.Objects[id]
		if !ok {
			out = append(out, Mismatch{Category: CategoryObject, Key: id,
				Detail: "重放结果缺少声明目标中的对象"})
			continue
		}
		if !objectEqual(got, want) {
			out = append(out, Mismatch{Category: CategoryObject, Key: id,
				Detail: "对象取值与声明目标不一致"})
		}
	}

	if len(st.Links) != len(log.TargetLinks) {
		out = append(out, Mismatch{Category: CategoryLink, Key: "(count)",
			Detail: fmt.Sprintf("链接总数不一致: 重放结果 %d, 声明目标 %d",
				len(st.Links), len(log.TargetLinks))})
	}
	for key, want := range log.TargetLinks {
		got, ok := st.Links[key]
		if !ok {
			out = append(out, Mismatch{Category: CategoryLink, Key: fmt.Sprint(key),
				Detail: "重放结果缺少声明目标中的链接"})
			continue
		}
		if got != want {
			out = append(out, Mismatch{Category: CategoryLink, Key: fmt.Sprint(key),
				Detail: "链接与声明目标不一致"})
		}
	}

	sort.Slice(out, func(a, b int) bool {
		if out[a].Category != out[b].Category {
			return out[a].Category < out[b].Category
		}
		if out[a].Key != out[b].Key {
			return out[a].Key < out[b].Key
		}
		return out[a].Detail < out[b].Detail
	})
	return out
}
