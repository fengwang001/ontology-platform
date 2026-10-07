package replay

import "fmt"

// 本文件实现第一阶段：差异记录自身自洽性校验（InconsistentDelta）。
//
// 判定规则（关键取舍）：
//   - 本阶段只使用差异记录本身与良好快照的结构层（对象类型/链接类型
//     定义），绝不读取快照的实例内容（对象取值、链接键）。凡需要快照实例
//     内容才能判定的检查（对象/链接是否存在、变化前状态是否与快照一致、
//     快照中是否仍有引用等），一律推迟到第二阶段，归为“前提环境不满足”。
//   - 差异记录内部可判定的矛盾（重复添加、删除后再次更新、与自身此前产生
//     的状态不符、实例变化引用了尚未声明的结构、结构变化晚于依赖它的实例
//     变化等），在本阶段判定为不自洽，整体拒绝，绝不开始重放。
//   - 本阶段按差异记录声明的原始顺序模拟结构层演化，绝不自行调整顺序。

// deltaSim 是自洽性校验使用的模拟状态：结构层为快照结构的克隆，
// 实例层只跟踪差异记录自身触及的对象与链接。
type deltaSim struct {
	schema Schema

	objects map[string]ObjectInstance // 差异记录触及对象的当前完整状态
	removed map[string]bool           // 被差异记录删除的对象

	links        map[LinkKey]LinkInstance // 差异记录添加且仍存活的链接
	removedLinks map[LinkKey]bool         // 被差异记录删除的链接

	linkSourceCounts map[LinkSourceKey]int // 差异记录活跃链接的 (类型,源) 计数
}

func newDeltaSim(snapSchema Schema) *deltaSim {
	return &deltaSim{
		schema:           cloneSchema(snapSchema),
		objects:          map[string]ObjectInstance{},
		removed:          map[string]bool{},
		links:            map[LinkKey]LinkInstance{},
		removedLinks:     map[LinkKey]bool{},
		linkSourceCounts: map[LinkSourceKey]int{},
	}
}

// inconsistent 构造一个 InconsistentDelta 判定结果。
func inconsistent(index int, format string, args ...any) *Result {
	return &Result{
		Verdict:     VerdictInconsistentDelta,
		ChangeIndex: index,
		Reason:      fmt.Sprintf(format, args...),
	}
}

// checkDelta 对差异记录做自身自洽性校验。返回 nil 表示自洽。
// 只读取 snap 的结构层，不读取实例内容，不做任何修改。
func checkDelta(snap *Snapshot, log *DeltaLog) *Result {
	sim := newDeltaSim(snap.Schema)
	for i := range log.Changes {
		if r := sim.checkChange(i, &log.Changes[i]); r != nil {
			return r
		}
	}
	return nil
}

// checkChange 校验一条变化并将其效果应用到模拟状态。
func (sim *deltaSim) checkChange(i int, c *Change) *Result {
	if c.Kind < ChangeAddObjectType || c.Kind > ChangeRemoveLink {
		return inconsistent(i, "未知的变化种类 %d", int(c.Kind))
	}
	switch c.Kind {
	case ChangeAddObjectType:
		return sim.checkAddObjectType(i, c)
	case ChangeRemoveObjectType:
		return sim.checkRemoveObjectType(i, c)
	case ChangeAddProperty:
		return sim.checkAddProperty(i, c)
	case ChangeRemoveProperty:
		return sim.checkRemoveProperty(i, c)
	case ChangeAddLinkType:
		return sim.checkAddLinkType(i, c)
	case ChangeRemoveLinkType:
		return sim.checkRemoveLinkType(i, c)
	case ChangeAddLinkTypeConstraint:
		return sim.checkAddLinkTypeConstraint(i, c)
	case ChangeRemoveLinkTypeConstraint:
		return sim.checkRemoveLinkTypeConstraint(i, c)
	case ChangeAddObject:
		return sim.checkAddObject(i, c)
	case ChangeUpdateObject:
		return sim.checkUpdateObject(i, c)
	case ChangeRemoveObject:
		return sim.checkRemoveObject(i, c)
	case ChangeAddLink:
		return sim.checkAddLink(i, c)
	case ChangeRemoveLink:
		return sim.checkRemoveLink(i, c)
	}
	return inconsistent(i, "未处理的变化种类 %d", int(c.Kind))
}

func (sim *deltaSim) checkAddObjectType(i int, c *Change) *Result {
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
	def := ObjectTypeDef{TypeID: c.TypeID, Properties: map[string]struct{}{}}
	for p := range c.ObjectTypeAfter.Properties {
		def.Properties[p] = struct{}{}
	}
	sim.schema.ObjectTypes[c.TypeID] = def
	return nil
}

func (sim *deltaSim) checkRemoveObjectType(i int, c *Change) *Result {
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
	return nil
}

func (sim *deltaSim) checkAddProperty(i int, c *Change) *Result {
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
	return nil
}

func (sim *deltaSim) checkRemoveProperty(i int, c *Change) *Result {
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
	return nil
}

func (sim *deltaSim) checkAddLinkType(i int, c *Change) *Result {
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
	def := LinkTypeDef{
		TypeID:      c.LinkTypeID,
		SourceType:  c.LinkTypeAfter.SourceType,
		TargetType:  c.LinkTypeAfter.TargetType,
		Constraints: map[string]PropertyValue{},
	}
	for k, v := range c.LinkTypeAfter.Constraints {
		def.Constraints[k] = v
	}
	sim.schema.LinkTypes[c.LinkTypeID] = def
	return nil
}

func (sim *deltaSim) checkRemoveLinkType(i int, c *Change) *Result {
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
	return nil
}

func (sim *deltaSim) checkAddLinkTypeConstraint(i int, c *Change) *Result {
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
	return nil
}

func (sim *deltaSim) checkRemoveLinkTypeConstraint(i int, c *Change) *Result {
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
	return nil
}

// checkObjectShape 校验一个对象实例声明在结构层是否合法：
// 类型已声明、所有属性均已声明。
func (sim *deltaSim) checkObjectShape(i int, verb string, obj ObjectInstance) *Result {
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

func (sim *deltaSim) checkAddObject(i int, c *Change) *Result {
	obj := c.ObjectAfter
	if obj.ObjectID == "" {
		return inconsistent(i, "AddObject 缺少对象 ID")
	}
	if _, ok := sim.objects[obj.ObjectID]; ok {
		return inconsistent(i, "AddObject: 对象 %q 已被差异记录自身添加", obj.ObjectID)
	}
	// 对象是否已存在于快照中属于实例内容，留待重放前提校验。
	if r := sim.checkObjectShape(i, "AddObject", obj); r != nil {
		return r
	}
	sim.objects[obj.ObjectID] = cloneObject(obj)
	delete(sim.removed, obj.ObjectID)
	return nil
}

func (sim *deltaSim) checkUpdateObject(i int, c *Change) *Result {
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
	if r := sim.checkObjectShape(i, "UpdateObject(before)", before); r != nil {
		return r
	}
	if r := sim.checkObjectShape(i, "UpdateObject(after)", after); r != nil {
		return r
	}
	if sim.removed[before.ObjectID] {
		return inconsistent(i, "UpdateObject: 对象 %q 已被差异记录自身删除", before.ObjectID)
	}
	if cur, ok := sim.objects[before.ObjectID]; ok {
		if !objectEqual(cur, before) {
			return inconsistent(i, "UpdateObject: 对象 %q 的变化前状态与差异记录自身此前产生的状态不一致",
				before.ObjectID)
		}
	}
	// 否则对象来自快照，存在性与变化前状态留待重放前提校验。
	sim.objects[after.ObjectID] = cloneObject(after)
	return nil
}

func (sim *deltaSim) checkRemoveObject(i int, c *Change) *Result {
	before := c.ObjectBefore
	if before.ObjectID == "" {
		return inconsistent(i, "RemoveObject 缺少对象 ID")
	}
	if r := sim.checkObjectShape(i, "RemoveObject", before); r != nil {
		return r
	}
	if sim.removed[before.ObjectID] {
		return inconsistent(i, "RemoveObject: 对象 %q 已被差异记录自身删除", before.ObjectID)
	}
	if cur, ok := sim.objects[before.ObjectID]; ok {
		if !objectEqual(cur, before) {
			return inconsistent(i, "RemoveObject: 对象 %q 的变化前状态与差异记录自身此前产生的状态不一致",
				before.ObjectID)
		}
	}
	for _, l := range sim.links {
		if l.SourceID == before.ObjectID || l.TargetID == before.ObjectID {
			return inconsistent(i, "RemoveObject: 差异记录自身创建的链接 %v 仍引用对象 %q",
				l.Key(), before.ObjectID)
		}
	}
	// 快照中的链接是否引用该对象，留待重放前提校验。
	delete(sim.objects, before.ObjectID)
	sim.removed[before.ObjectID] = true
	return nil
}

// checkEndpoint 校验链接端点：若端点对象由差异记录自身触及，
// 可立即判定存在性与类型匹配；否则留待重放前提校验。
func (sim *deltaSim) checkEndpoint(i int, verb, objectID, expectType string) *Result {
	if sim.removed[objectID] {
		return inconsistent(i, "%s: 端点对象 %q 已被差异记录自身删除", verb, objectID)
	}
	if obj, ok := sim.objects[objectID]; ok {
		if obj.ObjectType != expectType {
			return inconsistent(i, "%s: 端点对象 %q 类型为 %q，与链接类型要求的 %q 不符",
				verb, objectID, obj.ObjectType, expectType)
		}
	}
	return nil
}

func (sim *deltaSim) checkAddLink(i int, c *Change) *Result {
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
	if r := sim.checkEndpoint(i, "AddLink", l.SourceID, lt.SourceType); r != nil {
		return r
	}
	if r := sim.checkEndpoint(i, "AddLink", l.TargetID, lt.TargetType); r != nil {
		return r
	}
	// 约束在结构层变化生效后、于本条实例变化处重新核对；
	// 本阶段只能核对差异记录自身产生的链接，快照中的链接留待重放前提校验。
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
	return nil
}

func (sim *deltaSim) checkRemoveLink(i int, c *Change) *Result {
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
	// 否则链接来自快照，存在性与变化前状态留待重放前提校验。
	sim.removedLinks[key] = true
	return nil
}

// isUniqueSource 报告链接类型当前是否声明了 uniqueSource 约束。
func isUniqueSource(lt LinkTypeDef) bool {
	v, ok := lt.Constraints[ConstraintUniqueSource]
	return ok && valuesEqual(v, true)
}
