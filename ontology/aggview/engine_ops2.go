package aggview

import (
	"fmt"
	"math/big"
)

// MembershipVersion 查询成员当前归属版本（乐观并发的前置读取）。
func (e *Engine) MembershipVersion(viewName, member ID) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	vw := e.views[viewName]
	if vw == nil {
		return 0
	}
	typ, ok := e.store.ObjectType(member)
	if !ok {
		return 0
	}
	return vw.version(member, typ)
}

// MoveToGroup 把成员在视图内的全部归属替换为 newGroup（单分组归属语义）。
// expectedVersion 为调用方先前读取的归属版本；与当前版本不一致则以 KindConflict
// 拒绝，且不改变任何聚合结果。原分组扣除、新分组增加与链接替换在同一处理单元完成。
func (e *Engine) MoveToGroup(viewName, member, newGroup ID, expectedVersion int64) (*Change, error) {
	input := fmt.Sprintf("view=%s member=%s newGroup=%s expectedVersion=%d",
		viewName, member, newGroup, expectedVersion)
	return e.txn("MoveToGroup", input, func(m *mutator) error {
		vw := e.views[viewName]
		if vw == nil {
			return &plainError{"view not found: " + string(viewName)}
		}
		typ, ok := e.store.ObjectType(member)
		if !ok {
			return &plainError{"object not found: " + string(member)}
		}
		// 错误优先级：分组不存在 > 类型未声明 > 并发冲突。
		gTyp, exists := e.store.ObjectType(newGroup)
		if !exists || gTyp != vw.def.GroupType {
			return errGroupNotFound
		}
		sd, declared := vw.srcByObj[typ]
		if !declared {
			return errTypeNotDeclared
		}
		cur := vw.version(member, typ)
		if cur != expectedVersion {
			m.reason = fmt.Sprintf("currentVersion=%d != expected=%d, rejected without side effect",
				cur, expectedVersion)
			return errConflict
		}
		oldGroups := m.txn.linksFrom(member, sd.src.LinkType)
		val := m.txn.getProp(member, sd.src.PropType)
		kOld := max1(len(oldGroups))
		m.reason += fmt.Sprintf("oldGroups=%v value=%s policy=%d; ",
			oldGroups, formatValue(val), sd.src.Policy)
		changed := false
		for _, g := range oldGroups {
			if g == newGroup {
				continue
			}
			m.apply(vw, g, typ,
				new(big.Rat).Neg(sd.contribution(val, kOld)), countDelta(val.Present, -1))
			if _, err := m.removeLink(member, sd.src.LinkType, g); err != nil {
				return err
			}
			changed = true
		}
		if contains(oldGroups, newGroup) {
			delta := new(big.Rat).Sub(sd.contribution(val, 1), sd.contribution(val, kOld))
			if delta.Sign() != 0 {
				m.apply(vw, newGroup, typ, delta, 0)
			}
		} else {
			m.apply(vw, newGroup, typ, sd.contribution(val, 1), countDelta(val.Present, 1))
			if _, err := m.addLink(member, sd.src.LinkType, newGroup); err != nil {
				return err
			}
			changed = true
		}
		if changed {
			m.bumpVersion(vw, member, typ)
		}
		return nil
	})
}

// DeleteObject 删除实例。
//   - 被聚合实例被删除：在同一处理单元内按其最后已知属性值扣除对各归属分组的贡献。
//   - 分组实例被删除：归属它的全部实例解除该归属（其他分组归属不受影响），
//     分组自身的聚合桶移除；被聚合实例自身不受影响。
func (e *Engine) DeleteObject(id ID) (*Change, error) {
	input := fmt.Sprintf("object=%s", id)
	return e.txn("DeleteObject", input, func(m *mutator) error {
		typ, ok := e.store.ObjectType(id)
		if !ok {
			return &plainError{"object not found: " + string(id)}
		}
		for _, vw := range e.views {
			if typ == vw.def.GroupType {
				for _, sd := range vw.def.Sources {
					sdDef := vw.srcByObj[sd.ObjectType]
					members := m.txn.linksTo(sdDef.src.LinkType, id)
					for _, member := range members {
						groups := m.txn.linksFrom(member, sdDef.src.LinkType)
						k := max1(len(groups))
						val := m.txn.getProp(member, sdDef.src.PropType)
						m.reason += fmt.Sprintf("group-deleted view=%s member=%s k=%d; ",
							vw.def.Name, member, k)
						m.apply(vw, id, sdDef.src.ObjectType,
							new(big.Rat).Neg(sdDef.contribution(val, k)), countDelta(val.Present, -1))
						m.reshare(vw, sdDef, without(groups, id), k, k-1, val)
						if _, err := m.removeLink(member, sdDef.src.LinkType, id); err != nil {
							return err
						}
						m.bumpVersion(vw, member, sdDef.src.ObjectType)
					}
				}
				m.deleteBucket(vw, id)
			}
			if sd, isSource := vw.srcByObj[typ]; isSource {
				groups := m.txn.linksFrom(id, sd.src.LinkType)
				k := max1(len(groups))
				val := m.txn.getProp(id, sd.src.PropType)
				m.reason += fmt.Sprintf("member-deleted view=%s groups=%v lastValue=%s; ",
					vw.def.Name, groups, formatValue(val))
				for _, g := range groups {
					m.apply(vw, g, typ,
						new(big.Rat).Neg(sd.contribution(val, k)), countDelta(val.Present, -1))
				}
				delete(vw.versions, id)
			}
		}
		if _, err := m.deleteObject(id); err != nil {
			return err
		}
		return nil
	})
}

// Query 返回某分组当前的聚合结果；不存在的分组/视图结果为零值。
// 结果恒等价于对当前真实归属且属性存在的全部实例重新求和。
func (e *Engine) Query(viewName, group ID) Aggregate {
	e.mu.Lock()
	defer e.mu.Unlock()
	vw := e.views[viewName]
	if vw == nil {
		return Aggregate{Sum: big.NewRat(0, 1)}
	}
	a := vw.query(group)
	return Aggregate{Sum: new(big.Rat).Set(a.Sum), Count: a.Count}
}
