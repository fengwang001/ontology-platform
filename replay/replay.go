package replay

import "fmt"

// 本文件实现第二阶段：前提环境校验 + 受控重放。
//
// 第一阶段已保证差异记录自身自洽，因此本阶段发现的任何应用失败
// 都意味着良好快照（重放环境）与差异记录产生时的环境不一致，
// 归为 PreconditionFailed。重放在覆盖层上进行，绝不修改原始快照。
//
// 变化严格按差异记录声明的顺序逐条应用：结构层变化应用后，后续
// 实例层变化才会基于已生效的结构（含约束）重新核对；在结构层变化
// 应用之前，不会对任何依赖它的实例变化做先行校验。

// replayLog 在覆盖层 st 上按原始顺序逐条应用差异记录。
// 全部应用成功返回 (-1, "")；否则返回触发前提违例的变化下标与原因。
func replayLog(st *overlay, log *DeltaLog) (int, string) {
	for i := range log.Changes {
		if reason := applyChange(st, &log.Changes[i]); reason != "" {
			return i, reason
		}
	}
	return -1, ""
}

// applyChange 校验前提并应用一条变化；返回空串表示成功。
func applyChange(st *overlay, c *Change) string {
	switch c.Kind {
	case ChangeAddObjectType:
		st.addObjectType(c.ObjectTypeAfter)
		return ""
	case ChangeRemoveObjectType:
		if n := st.objectTypeUsage(c.TypeID); n > 0 {
			return fmt.Sprintf("RemoveObjectType: 快照中仍存在 %d 个类型 %q 的对象", n, c.TypeID)
		}
		st.removeObjectType(c.TypeID)
		return ""
	case ChangeAddProperty:
		st.mutableObjectType(c.TypeID).Properties[c.Property] = struct{}{}
		return ""
	case ChangeRemoveProperty:
		key := TypePropertyKey{TypeID: c.TypeID, Property: c.Property}
		if n := st.propertyUsage(key); n > 0 {
			return fmt.Sprintf("RemoveProperty: 快照中仍有 %d 个类型 %q 的对象持有属性 %q",
				n, c.TypeID, c.Property)
		}
		delete(st.mutableObjectType(c.TypeID).Properties, c.Property)
		return ""
	case ChangeAddLinkType:
		st.addLinkType(c.LinkTypeAfter)
		return ""
	case ChangeRemoveLinkType:
		if n := st.linkTypeUsage(c.LinkTypeID); n > 0 {
			return fmt.Sprintf("RemoveLinkType: 快照中仍存在 %d 条类型 %q 的链接", n, c.LinkTypeID)
		}
		st.removeLinkType(c.LinkTypeID)
		return ""
	case ChangeAddLinkTypeConstraint:
		st.mutableLinkType(c.LinkTypeID).Constraints[c.Constraint] = c.ConstraintValue
		return ""
	case ChangeRemoveLinkTypeConstraint:
		delete(st.mutableLinkType(c.LinkTypeID).Constraints, c.Constraint)
		return ""
	case ChangeAddObject:
		return applyAddObject(st, c)
	case ChangeUpdateObject:
		return applyUpdateObject(st, c)
	case ChangeRemoveObject:
		return applyRemoveObject(st, c)
	case ChangeAddLink:
		return applyAddLink(st, c)
	case ChangeRemoveLink:
		return applyRemoveLink(st, c)
	}
	return fmt.Sprintf("未知的变化种类 %d", int(c.Kind))
}

func applyAddObject(st *overlay, c *Change) string {
	obj := c.ObjectAfter
	if _, ok := st.getObject(obj.ObjectID); ok {
		return fmt.Sprintf("AddObject: 对象 %q 在快照中已存在", obj.ObjectID)
	}
	st.putObject(obj, nil)
	return ""
}

func applyUpdateObject(st *overlay, c *Change) string {
	before := c.ObjectBefore
	cur, ok := st.getObject(before.ObjectID)
	if !ok {
		return fmt.Sprintf("UpdateObject: 对象 %q 在快照中不存在", before.ObjectID)
	}
	if !objectEqual(cur, before) {
		return fmt.Sprintf("UpdateObject: 对象 %q 的变化前状态与快照不一致", before.ObjectID)
	}
	st.putObject(c.ObjectAfter, &cur)
	return ""
}

func applyRemoveObject(st *overlay, c *Change) string {
	before := c.ObjectBefore
	cur, ok := st.getObject(before.ObjectID)
	if !ok {
		return fmt.Sprintf("RemoveObject: 对象 %q 在快照中不存在", before.ObjectID)
	}
	if !objectEqual(cur, before) {
		return fmt.Sprintf("RemoveObject: 对象 %q 的变化前状态与快照不一致", before.ObjectID)
	}
	if n := st.linkEndpointUsage(before.ObjectID); n > 0 {
		return fmt.Sprintf("RemoveObject: 快照中仍有 %d 条链接引用对象 %q", n, before.ObjectID)
	}
	st.removeObject(cur)
	return ""
}

func applyAddLink(st *overlay, c *Change) string {
	l := c.LinkAfter
	lt, ok := st.getLinkType(l.LinkTypeID)
	if !ok {
		return fmt.Sprintf("AddLink: 链接类型 %q 不存在", l.LinkTypeID)
	}
	if _, ok := st.getLink(l.Key()); ok {
		return fmt.Sprintf("AddLink: 链接 %v 在快照中已存在", l.Key())
	}
	src, ok := st.getObject(l.SourceID)
	if !ok {
		return fmt.Sprintf("AddLink: 源对象 %q 在快照中不存在", l.SourceID)
	}
	if src.ObjectType != lt.SourceType {
		return fmt.Sprintf("AddLink: 源对象 %q 类型 %q 与链接类型 %q 要求的 %q 不符",
			l.SourceID, src.ObjectType, l.LinkTypeID, lt.SourceType)
	}
	dst, ok := st.getObject(l.TargetID)
	if !ok {
		return fmt.Sprintf("AddLink: 目标对象 %q 在快照中不存在", l.TargetID)
	}
	if dst.ObjectType != lt.TargetType {
		return fmt.Sprintf("AddLink: 目标对象 %q 类型 %q 与链接类型 %q 要求的 %q 不符",
			l.TargetID, dst.ObjectType, l.LinkTypeID, lt.TargetType)
	}
	// 约束在结构层变化生效后，于本条实例变化处基于当前有效状态重新核对。
	if isUniqueSource(lt) {
		sk := LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}
		if n := st.linkSourceUsage(sk); n > 0 {
			return fmt.Sprintf("AddLink: 链接 %v 违反链接类型 %q 的 uniqueSource 约束", l.Key(), l.LinkTypeID)
		}
	}
	st.putLink(l)
	return ""
}

func applyRemoveLink(st *overlay, c *Change) string {
	l := c.LinkBefore
	cur, ok := st.getLink(l.Key())
	if !ok {
		return fmt.Sprintf("RemoveLink: 链接 %v 在快照中不存在", l.Key())
	}
	if cur != l {
		return fmt.Sprintf("RemoveLink: 链接 %v 的变化前状态与快照不一致", l.Key())
	}
	st.removeLink(l)
	return ""
}
