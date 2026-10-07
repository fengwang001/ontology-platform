package ontology

import (
	"fmt"
	"sort"
)

// Disposition 是结构元素在一次比对中的处置类别，三者互斥：
// 同一元素在一次比对中只能取其一。
type Disposition string

const (
	DispAdded    Disposition = "added"
	DispRemoved  Disposition = "removed"
	DispModified Disposition = "modified"
)

// Rename 描述一次标识变化：同一元素（ID 不变）的对外名称发生变化。
type Rename struct {
	OldKey string `json:"oldKey"`
	NewKey string `json:"newKey"`
}

// Retype 描述一次取值类型变化，与 Rename 是相互独立的差异维度，
// 可以同时出现在同一个属性上。
type Retype struct {
	OldType PropertyType `json:"oldType"`
	NewType PropertyType `json:"newType"`
	Class   RetypeClass  `json:"class"`
}

// PropertyDiff 是单个属性的结构层差异。
type PropertyDiff struct {
	PropID      string      `json:"propId"`
	Disposition Disposition `json:"disposition"`
	Renamed     *Rename     `json:"renamed,omitempty"`
	Retyped     *Retype     `json:"retyped,omitempty"`
}

// ObjectTypeDiff 是单个对象类型的结构层差异。
type ObjectTypeDiff struct {
	TypeID            string         `json:"typeId"`
	Disposition       Disposition    `json:"disposition"`
	Renamed           *Rename        `json:"renamed,omitempty"`
	PrimaryKeyChanged bool           `json:"primaryKeyChanged,omitempty"`
	Props             []PropertyDiff `json:"props,omitempty"`
}

// LinkTypeDiff 是单个链接类型的结构层差异。
type LinkTypeDiff struct {
	LinkTypeID       string      `json:"linkTypeId"`
	Disposition      Disposition `json:"disposition"`
	Renamed          *Rename     `json:"renamed,omitempty"`
	EndpointsChanged bool        `json:"endpointsChanged,omitempty"`
}

// SchemaDiff 是完整的结构层差异。所有列表按键排序，保证输出确定。
type SchemaDiff struct {
	ObjectTypes []ObjectTypeDiff `json:"objectTypes"`
	LinkTypes   []LinkTypeDiff   `json:"linkTypes"`
}

// Empty 报告结构层是否完全没有差异。
func (d *SchemaDiff) Empty() bool {
	return len(d.ObjectTypes) == 0 && len(d.LinkTypes) == 0
}

// ObjectTypeDiff 按类型 ID 查找差异，未找到返回 nil。
func (d *SchemaDiff) ObjectTypeDiff(typeID string) *ObjectTypeDiff {
	for i := range d.ObjectTypes {
		if d.ObjectTypes[i].TypeID == typeID {
			return &d.ObjectTypes[i]
		}
	}
	return nil
}

// DiffSchema 计算两份结构定义之间的差异。
// 元素身份由稳定 ID 决定：同 ID 不同 Key 判定为重命名，
// 而不是删除加新增。纯函数，不修改输入。
func DiffSchema(oldS, newS Schema) *SchemaDiff {
	d := &SchemaDiff{}
	for _, id := range sortedUnion(keysOfTypes(oldS), keysOfTypes(newS)) {
		o, inOld := oldS.Types[id]
		n, inNew := newS.Types[id]
		switch {
		case !inOld:
			d.ObjectTypes = append(d.ObjectTypes, ObjectTypeDiff{TypeID: id, Disposition: DispAdded})
		case !inNew:
			d.ObjectTypes = append(d.ObjectTypes, ObjectTypeDiff{TypeID: id, Disposition: DispRemoved})
		default:
			td := ObjectTypeDiff{TypeID: id, Disposition: DispModified}
			if o.Name != n.Name {
				td.Renamed = &Rename{OldKey: o.Name, NewKey: n.Name}
			}
			if o.PrimaryKey != n.PrimaryKey {
				td.PrimaryKeyChanged = true
			}
			td.Props = diffProps(o.Props, n.Props)
			if td.Renamed == nil && !td.PrimaryKeyChanged && len(td.Props) == 0 {
				continue
			}
			d.ObjectTypes = append(d.ObjectTypes, td)
		}
	}
	for _, id := range sortedUnion(keysOfLinkTypes(oldS), keysOfLinkTypes(newS)) {
		o, inOld := oldS.LinkTypes[id]
		n, inNew := newS.LinkTypes[id]
		switch {
		case !inOld:
			d.LinkTypes = append(d.LinkTypes, LinkTypeDiff{LinkTypeID: id, Disposition: DispAdded})
		case !inNew:
			d.LinkTypes = append(d.LinkTypes, LinkTypeDiff{LinkTypeID: id, Disposition: DispRemoved})
		default:
			ltd := LinkTypeDiff{LinkTypeID: id, Disposition: DispModified}
			if o.Name != n.Name {
				ltd.Renamed = &Rename{OldKey: o.Name, NewKey: n.Name}
			}
			if o.Source != n.Source || o.Target != n.Target {
				ltd.EndpointsChanged = true
			}
			if ltd.Renamed == nil && !ltd.EndpointsChanged {
				continue
			}
			d.LinkTypes = append(d.LinkTypes, ltd)
		}
	}
	return d
}

func diffProps(oldP, newP map[string]Property) []PropertyDiff {
	var out []PropertyDiff
	ids := map[string]bool{}
	for id := range oldP {
		ids[id] = true
	}
	for id := range newP {
		ids[id] = true
	}
	for _, id := range sortedKeys(ids) {
		o, inOld := oldP[id]
		n, inNew := newP[id]
		switch {
		case !inOld:
			out = append(out, PropertyDiff{PropID: id, Disposition: DispAdded})
		case !inNew:
			out = append(out, PropertyDiff{PropID: id, Disposition: DispRemoved})
		default:
			pd := PropertyDiff{PropID: id, Disposition: DispModified}
			if o.Key != n.Key {
				pd.Renamed = &Rename{OldKey: o.Key, NewKey: n.Key}
			}
			if o.Type != n.Type {
				pd.Retyped = &Retype{OldType: o.Type, NewType: n.Type, Class: ClassifyRetype(o.Type, n.Type)}
			}
			if pd.Renamed == nil && pd.Retyped == nil {
				continue
			}
			out = append(out, pd)
		}
	}
	return out
}

// Validate 校验结构层差异的内部一致性（错误类别二：结构层差异
// 判定冲突）。对合法快照，DiffSchema 的输出必然一致；该校验是
// 比对管线的契约守卫，任何元素被同时判定为多种互斥处置、或
// 差异维度与处置自相矛盾时，返回 *CompareError（ErrClassConflict）。
func (d *SchemaDiff) Validate() error {
	seenTypes := map[string]bool{}
	for _, td := range d.ObjectTypes {
		if seenTypes[td.TypeID] {
			return conflictf("object type %q appears in multiple diff entries", td.TypeID)
		}
		seenTypes[td.TypeID] = true
		switch td.Disposition {
		case DispAdded, DispRemoved:
			if td.Renamed != nil || td.PrimaryKeyChanged || len(td.Props) > 0 {
				return conflictf("object type %q: disposition %q carries modification dimensions", td.TypeID, td.Disposition)
			}
		case DispModified:
			if td.Renamed == nil && !td.PrimaryKeyChanged && len(td.Props) == 0 {
				return conflictf("object type %q: modified without any dimension", td.TypeID)
			}
		default:
			return conflictf("object type %q: unknown disposition %q", td.TypeID, td.Disposition)
		}
		seenProps := map[string]bool{}
		for _, pd := range td.Props {
			if seenProps[pd.PropID] {
				return conflictf("object type %q: property %q appears in multiple diff entries", td.TypeID, pd.PropID)
			}
			seenProps[pd.PropID] = true
			if err := validatePropDiff(td.TypeID, pd); err != nil {
				return err
			}
		}
	}
	seenLinks := map[string]bool{}
	for _, ltd := range d.LinkTypes {
		if seenLinks[ltd.LinkTypeID] {
			return conflictf("link type %q appears in multiple diff entries", ltd.LinkTypeID)
		}
		seenLinks[ltd.LinkTypeID] = true
		switch ltd.Disposition {
		case DispAdded, DispRemoved:
			if ltd.Renamed != nil || ltd.EndpointsChanged {
				return conflictf("link type %q: disposition %q carries modification dimensions", ltd.LinkTypeID, ltd.Disposition)
			}
		case DispModified:
			if ltd.Renamed == nil && !ltd.EndpointsChanged {
				return conflictf("link type %q: modified without any dimension", ltd.LinkTypeID)
			}
		default:
			return conflictf("link type %q: unknown disposition %q", ltd.LinkTypeID, ltd.Disposition)
		}
	}
	return nil
}

func validatePropDiff(typeID string, pd PropertyDiff) error {
	switch pd.Disposition {
	case DispAdded, DispRemoved:
		if pd.Renamed != nil || pd.Retyped != nil {
			return conflictf("property %q of type %q: disposition %q carries modification dimensions", pd.PropID, typeID, pd.Disposition)
		}
	case DispModified:
		if pd.Renamed == nil && pd.Retyped == nil {
			return conflictf("property %q of type %q: modified without any dimension", pd.PropID, typeID)
		}
		if pd.Renamed != nil && pd.Renamed.OldKey == pd.Renamed.NewKey {
			return conflictf("property %q of type %q: rename with identical keys", pd.PropID, typeID)
		}
		if pd.Retyped != nil {
			if pd.Retyped.OldType == pd.Retyped.NewType {
				return conflictf("property %q of type %q: retype with identical types", pd.PropID, typeID)
			}
			if ClassifyRetype(pd.Retyped.OldType, pd.Retyped.NewType) != pd.Retyped.Class {
				return conflictf("property %q of type %q: retype class %q inconsistent with types", pd.PropID, typeID, pd.Retyped.Class)
			}
		}
	default:
		return conflictf("property %q of type %q: unknown disposition %q", pd.PropID, typeID, pd.Disposition)
	}
	return nil
}

func conflictf(format string, args ...any) error {
	return &CompareError{Class: ErrClassConflict, Detail: fmt.Sprintf(format, args...)}
}

func keysOfTypes(s Schema) map[string]bool {
	out := map[string]bool{}
	for id := range s.Types {
		out[id] = true
	}
	return out
}

func keysOfLinkTypes(s Schema) map[string]bool {
	out := map[string]bool{}
	for id := range s.LinkTypes {
		out[id] = true
	}
	return out
}

func sortedUnion(a, b map[string]bool) []string {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return sortedKeys(out)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
