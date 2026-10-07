package aggview

import (
	"fmt"
	"math/big"
)

// SetProperty 写入数值属性（AbsentValue 表示清除为不存在）。
// 仅触及该实例当前归属分组的聚合结果；属性写入与聚合更新同一处理单元完成，
// 不扫描任何与该实例无关的分组。
func (e *Engine) SetProperty(id, prop ID, v Value) (*Change, error) {
	input := fmt.Sprintf("object=%s prop=%s value=%s", id, prop, formatValue(v))
	return e.txn("SetProperty", input, func(m *mutator) error {
		typ, ok := e.store.ObjectType(id)
		if !ok {
			return &plainError{"object not found: " + string(id)}
		}
		old := m.txn.getProp(id, prop)
		if _, err := m.setProp(id, prop, v); err != nil {
			return err
		}
		for _, vw := range e.views {
			sd, ok := vw.srcByObj[typ]
			if !ok || sd.src.PropType != prop {
				continue
			}
			groups := m.txn.linksFrom(id, sd.src.LinkType)
			k := len(groups)
			m.reason += fmt.Sprintf("view=%s policy=%d groups=%d old=%s new=%s; ",
				vw.def.Name, sd.src.Policy, k, formatValue(old), formatValue(v))
			for _, g := range groups {
				delta := new(big.Rat).Sub(sd.contribution(v, k), sd.contribution(old, k))
				dCount := countDelta(v.Present, 1) - countDelta(old.Present, 1)
				m.apply(vw, g, sd.src.ObjectType, delta, dCount)
			}
		}
		return nil
	})
}

// AddLink 创建 from->to 的链接；命中视图归属链接时，增量为新分组增加贡献，
// EvenShare 策略下同时对既有分组做份额重摊（同一处理单元）。
func (e *Engine) AddLink(from, linkType, to ID) (*Change, error) {
	input := fmt.Sprintf("from=%s link=%s to=%s", from, linkType, to)
	return e.txn("AddLink", input, func(m *mutator) error {
		fromType, ok := e.store.ObjectType(from)
		if !ok {
			return &plainError{"object not found: " + string(from)}
		}
		for _, vw := range e.byLink[linkType] {
			sd := vw.srcByLink[linkType]
			if sd.src.ObjectType != fromType {
				continue
			}
			toTyp, exists := e.store.ObjectType(to)
			if !exists || toTyp != vw.def.GroupType {
				return errGroupNotFound
			}
		}
		added, err := m.addLink(from, linkType, to)
		if err != nil || !added {
			return err
		}
		for _, vw := range e.byLink[linkType] {
			sd := vw.srcByLink[linkType]
			if sd.src.ObjectType != fromType {
				continue
			}
			groups := m.txn.linksFrom(from, sd.src.LinkType)
			k := len(groups) // 已含新链接
			val := m.txn.getProp(from, sd.src.PropType)
			m.reason += fmt.Sprintf("view=%s add-group=%s groups=%d value=%s; ",
				vw.def.Name, to, k, formatValue(val))
			m.reshare(vw, sd, without(groups, to), k-1, k, val)
			m.apply(vw, to, sd.src.ObjectType,
				sd.contribution(val, k), countDelta(val.Present, 1))
			m.bumpVersion(vw, from, sd.src.ObjectType)
		}
		return nil
	})
}

// RemoveLink 删除归属链接：原分组扣除与（EvenShare 下）剩余分组重摊
// 在同一处理单元内完成，不存在“只扣未摊/只摊未扣”的中间状态。
func (e *Engine) RemoveLink(from, linkType, to ID) (*Change, error) {
	input := fmt.Sprintf("from=%s link=%s to=%s", from, linkType, to)
	return e.txn("RemoveLink", input, func(m *mutator) error {
		fromType, ok := e.store.ObjectType(from)
		if !ok {
			return &plainError{"object not found: " + string(from)}
		}
		removed, err := m.removeLink(from, linkType, to)
		if err != nil || !removed {
			return err
		}
		for _, vw := range e.byLink[linkType] {
			sd := vw.srcByLink[linkType]
			if sd.src.ObjectType != fromType {
				continue
			}
			groups := m.txn.linksFrom(from, sd.src.LinkType)
			k := len(groups) // 已不含被删链接
			val := m.txn.getProp(from, sd.src.PropType)
			m.reason += fmt.Sprintf("view=%s remove-group=%s groups=%d value=%s; ",
				vw.def.Name, to, k, formatValue(val))
			m.apply(vw, to, sd.src.ObjectType,
				new(big.Rat).Neg(sd.contribution(val, k+1)), countDelta(val.Present, -1))
			m.reshare(vw, sd, groups, k+1, k, val)
			m.bumpVersion(vw, from, sd.src.ObjectType)
		}
		return nil
	})
}
