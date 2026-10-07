package ontology

import (
	"fmt"
	"strings"
)

// propAlignment 是同一属性（稳定 ID）在两份快照中的定义对。
// Old/New 之一为 nil 表示该属性在对应快照中不存在。
type propAlignment struct {
	Old *Property
	New *Property
}

// typeAlignment 是一个存续对象类型的口径：属性按 ID 对齐。
type typeAlignment struct {
	Old   *ObjectType
	New   *ObjectType
	Props map[string]propAlignment
}

// Alignment 是结构层差异解释出的实例层比对口径。
type Alignment struct {
	// Types 包含两份快照中都存续的对象类型的口径。
	Types map[string]*typeAlignment
	// DeprecatedTypes 是在目标快照中被整体废弃的对象类型。
	DeprecatedTypes map[string]bool
	// LinkTypes 包含两份快照中都存续的链接类型（端点已校验一致）。
	LinkTypes map[string]*LinkType
	// DeprecatedLinkTypes 是在目标快照中被移除的链接类型。
	DeprecatedLinkTypes map[string]bool
}

// BuildAlignment 在结构层差异的基础上构建实例层比对口径。
// 以下结构变化使实例身份无法跨快照对齐，返回 ErrClassBasis：
//   - 存续对象类型的主键属性被移除、被改型或主键指定被更换；
//   - 存续链接类型的端点对象类型被更换。
func BuildAlignment(oldS, newS Schema, sd *SchemaDiff) (*Alignment, error) {
	al := &Alignment{
		Types:               map[string]*typeAlignment{},
		DeprecatedTypes:     map[string]bool{},
		LinkTypes:           map[string]*LinkType{},
		DeprecatedLinkTypes: map[string]bool{},
	}
	var problems []string
	for id, o := range oldS.Types {
		n, ok := newS.Types[id]
		if !ok {
			al.DeprecatedTypes[id] = true
			continue
		}
		ta := &typeAlignment{Old: &o, New: &n, Props: map[string]propAlignment{}}
		for pid := range o.Props {
			pa := propAlignment{Old: ptrProp(o.Props[pid])}
			if np, ok := n.Props[pid]; ok {
				pa.New = &np
			}
			ta.Props[pid] = pa
		}
		for pid := range n.Props {
			if _, ok := ta.Props[pid]; ok {
				continue
			}
			ta.Props[pid] = propAlignment{New: ptrProp(n.Props[pid])}
		}
		al.Types[id] = ta
		if o.PrimaryKey != n.PrimaryKey {
			problems = append(problems, fmt.Sprintf(
				"object type %q: primary key designation changed from %q to %q", id, o.PrimaryKey, n.PrimaryKey))
			continue
		}
		opk := o.Props[o.PrimaryKey]
		npk := n.Props[n.PrimaryKey]
		if opk.Type != npk.Type {
			problems = append(problems, fmt.Sprintf(
				"object type %q: primary key property %q retyped from %q to %q", id, o.PrimaryKey, opk.Type, npk.Type))
		}
	}
	for id, o := range oldS.LinkTypes {
		n, ok := newS.LinkTypes[id]
		if !ok {
			al.DeprecatedLinkTypes[id] = true
			continue
		}
		lt := n
		al.LinkTypes[id] = &lt
		if o.Source != n.Source || o.Target != n.Target {
			problems = append(problems, fmt.Sprintf(
				"link type %q: endpoints changed from (%q,%q) to (%q,%q)",
				id, o.Source, o.Target, n.Source, n.Target))
		}
	}
	if len(problems) > 0 {
		return nil, &CompareError{
			Class:  ErrClassBasis,
			Detail: "instance alignment basis undetermined: " + strings.Join(problems, "; "),
		}
	}
	return al, nil
}

func ptrProp(p Property) *Property { return &p }
