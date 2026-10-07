// Package naive 是差异比对的朴素参照模型：不依赖修订号与墓碑，
// 对两份快照做全量连接，按规则逐步独立判定。它用于在随机生成的
// 快照对上与主实现 ontology.Compare 交叉验证。
package naive

import (
	"sort"

	"ontology"
)

// Compare 以全量扫描方式独立复算两份快照的差异。
// 输出与 ontology.Compare 的 Schema/Instances 部分语义一致。
func Compare(oldS, newS *ontology.Snapshot) (*ontology.Result, error) {
	if err := ontology.ValidateSnapshot(oldS); err != nil {
		return nil, &ontology.CompareError{Class: ontology.ErrClassCorrupt, Detail: "base snapshot: " + err.Error()}
	}
	if err := ontology.ValidateSnapshot(newS); err != nil {
		return nil, &ontology.CompareError{Class: ontology.ErrClassCorrupt, Detail: "target snapshot: " + err.Error()}
	}
	sd := diffSchema(oldS.Schema, newS.Schema)
	if err := sd.Validate(); err != nil {
		return nil, err
	}
	if err := checkBasis(oldS.Schema, newS.Schema); err != nil {
		return nil, err
	}
	id := &ontology.InstanceDiff{}
	id.Objects = diffObjects(oldS, newS)
	id.Links = diffLinks(oldS, newS)
	return &ontology.Result{Schema: sd, Instances: id}, nil
}

// ---- 结构层：独立复算 ----

func diffSchema(oldS, newS ontology.Schema) *ontology.SchemaDiff {
	d := &ontology.SchemaDiff{}
	for _, id := range unionKeys(typeKeys(oldS), typeKeys(newS)) {
		o, inOld := oldS.Types[id]
		n, inNew := newS.Types[id]
		switch {
		case !inOld:
			d.ObjectTypes = append(d.ObjectTypes, ontology.ObjectTypeDiff{TypeID: id, Disposition: ontology.DispAdded})
		case !inNew:
			d.ObjectTypes = append(d.ObjectTypes, ontology.ObjectTypeDiff{TypeID: id, Disposition: ontology.DispRemoved})
		default:
			td := ontology.ObjectTypeDiff{TypeID: id, Disposition: ontology.DispModified}
			if o.Name != n.Name {
				td.Renamed = &ontology.Rename{OldKey: o.Name, NewKey: n.Name}
			}
			if o.PrimaryKey != n.PrimaryKey {
				td.PrimaryKeyChanged = true
			}
			td.Props = diffProps(o.Props, n.Props)
			if td.Renamed != nil || td.PrimaryKeyChanged || len(td.Props) > 0 {
				d.ObjectTypes = append(d.ObjectTypes, td)
			}
		}
	}
	for _, id := range unionKeys(linkTypeKeys(oldS), linkTypeKeys(newS)) {
		o, inOld := oldS.LinkTypes[id]
		n, inNew := newS.LinkTypes[id]
		switch {
		case !inOld:
			d.LinkTypes = append(d.LinkTypes, ontology.LinkTypeDiff{LinkTypeID: id, Disposition: ontology.DispAdded})
		case !inNew:
			d.LinkTypes = append(d.LinkTypes, ontology.LinkTypeDiff{LinkTypeID: id, Disposition: ontology.DispRemoved})
		default:
			ltd := ontology.LinkTypeDiff{LinkTypeID: id, Disposition: ontology.DispModified}
			if o.Name != n.Name {
				ltd.Renamed = &ontology.Rename{OldKey: o.Name, NewKey: n.Name}
			}
			if o.Source != n.Source || o.Target != n.Target {
				ltd.EndpointsChanged = true
			}
			if ltd.Renamed != nil || ltd.EndpointsChanged {
				d.LinkTypes = append(d.LinkTypes, ltd)
			}
		}
	}
	return d
}

func diffProps(oldP, newP map[string]ontology.Property) []ontology.PropertyDiff {
	var out []ontology.PropertyDiff
	for _, id := range unionKeys(propKeys(oldP), propKeys(newP)) {
		o, inOld := oldP[id]
		n, inNew := newP[id]
		switch {
		case !inOld:
			out = append(out, ontology.PropertyDiff{PropID: id, Disposition: ontology.DispAdded})
		case !inNew:
			out = append(out, ontology.PropertyDiff{PropID: id, Disposition: ontology.DispRemoved})
		default:
			pd := ontology.PropertyDiff{PropID: id, Disposition: ontology.DispModified}
			if o.Key != n.Key {
				pd.Renamed = &ontology.Rename{OldKey: o.Key, NewKey: n.Key}
			}
			if o.Type != n.Type {
				pd.Retyped = &ontology.Retype{OldType: o.Type, NewType: n.Type, Class: ontology.ClassifyRetype(o.Type, n.Type)}
			}
			if pd.Renamed != nil || pd.Retyped != nil {
				out = append(out, pd)
			}
		}
	}
	return out
}

// ---- 口径：独立校验 ----

func checkBasis(oldS, newS ontology.Schema) error {
	var problems []string
	for id, o := range oldS.Types {
		n, ok := newS.Types[id]
		if !ok {
			continue
		}
		if o.PrimaryKey != n.PrimaryKey {
			problems = append(problems, "type "+id+": primary key changed")
			continue
		}
		if o.Props[o.PrimaryKey].Type != n.Props[n.PrimaryKey].Type {
			problems = append(problems, "type "+id+": primary key retyped")
		}
	}
	for id, o := range oldS.LinkTypes {
		n, ok := newS.LinkTypes[id]
		if !ok {
			continue
		}
		if o.Source != n.Source || o.Target != n.Target {
			problems = append(problems, "link type "+id+": endpoints changed")
		}
	}
	if len(problems) > 0 {
		return &ontology.CompareError{Class: ontology.ErrClassBasis, Detail: "naive: " + join(problems)}
	}
	return nil
}

// ---- 实例层：全量连接，逐步判定 ----

func diffObjects(oldS, newS *ontology.Snapshot) []ontology.ObjectDiff {
	var out []ontology.ObjectDiff
	for _, key := range unionKeys(objectKeys(oldS), objectKeys(newS)) {
		o, inOld := oldS.Objects[key]
		n, inNew := newS.Objects[key]
		switch {
		case !inOld:
			out = append(out, ontology.ObjectDiff{Key: key, TypeID: n.TypeID, Kind: ontology.ObjectCreated})
		case !inNew:
			reason := ontology.ReasonObjectExplicit
			if _, ok := newS.Schema.Types[o.TypeID]; !ok {
				reason = ontology.ReasonTypeDeprecated
			}
			out = append(out, ontology.ObjectDiff{Key: key, TypeID: o.TypeID, Kind: ontology.ObjectDeleted, Reason: reason})
		default:
			changes := diffValues(oldS.Schema, newS.Schema, o.TypeID, o.Values, n.Values)
			if len(changes) > 0 {
				out = append(out, ontology.ObjectDiff{Key: key, TypeID: o.TypeID, Kind: ontology.ObjectUpdated, Changes: changes})
			}
		}
	}
	return out
}

func diffLinks(oldS, newS *ontology.Snapshot) []ontology.LinkDiff {
	var out []ontology.LinkDiff
	for _, key := range unionKeys(linkKeys(oldS), linkKeys(newS)) {
		o, inOld := oldS.Links[key]
		n, inNew := newS.Links[key]
		switch {
		case !inOld:
			out = append(out, ontology.LinkDiff{Key: key, TypeID: n.TypeID, Kind: ontology.LinkCreated})
		case !inNew:
			d := ontology.LinkDiff{Key: key, TypeID: o.TypeID, Kind: ontology.LinkDeleted}
			if _, ok := newS.Schema.LinkTypes[o.TypeID]; !ok {
				d.Reason = ontology.ReasonLinkTypeDeprecated
			} else {
				var missing []ontology.ObjectRef
				if !objectAlive(newS, o.Source) {
					missing = append(missing, o.Source)
				}
				if !objectAlive(newS, o.Target) {
					missing = append(missing, o.Target)
				}
				if len(missing) > 0 {
					d.Reason = ontology.ReasonEndpointDeleted
					d.MissingEndpoints = missing
				} else {
					d.Reason = ontology.ReasonLinkExplicit
				}
			}
			out = append(out, d)
		}
	}
	return out
}

// objectAlive 以逐对象扫描（而非键查询）判定端点是否存活，
// 与主实现的键索引路径保持独立。
func objectAlive(s *ontology.Snapshot, ref ontology.ObjectRef) bool {
	for _, o := range s.Objects {
		if o.TypeID == ref.TypeID && valuesEqual(o.PK, ref.PK) {
			return true
		}
	}
	return false
}

func diffValues(oldS, newS ontology.Schema, typeID string, oldV, newV map[string]ontology.Value) []ontology.PropertyChange {
	oldT := oldS.Types[typeID]
	newT := newS.Types[typeID]
	var out []ontology.PropertyChange
	for _, pid := range unionKeys(propKeys(oldT.Props), propKeys(newT.Props)) {
		oldDef, hasOldDef := oldT.Props[pid]
		newDef, hasNewDef := newT.Props[pid]
		oldVal, oldHas := oldV[pid]
		newVal, newHas := newV[pid]
		pc := ontology.PropertyChange{PropID: pid}
		if hasOldDef {
			pc.OldKey = oldDef.Key
			pc.OldType = oldDef.Type
		}
		if hasNewDef {
			pc.NewKey = newDef.Key
			pc.NewType = newDef.Type
		}
		retyped := hasOldDef && hasNewDef && oldDef.Type != newDef.Type
		switch {
		case oldHas && !newHas:
			v := oldVal
			pc.OldValue = &v
			if retyped && !validFor(oldVal, newDef.Type) {
				pc.Kind = ontology.ChangeTypeIncompatible
			} else {
				pc.Kind = ontology.ChangeValueRemoved
			}
		case !oldHas && newHas:
			v := newVal
			pc.NewValue = &v
			pc.Kind = ontology.ChangeValueAdded
		case oldHas && newHas:
			if retyped && !validFor(oldVal, newDef.Type) {
				v, w := oldVal, newVal
				pc.OldValue, pc.NewValue = &v, &w
				pc.Kind = ontology.ChangeTypeIncompatible
			} else if !valuesEqual(oldVal, newVal) {
				v, w := oldVal, newVal
				pc.OldValue, pc.NewValue = &v, &w
				pc.Kind = ontology.ChangeValueChanged
			} else {
				continue
			}
		default:
			continue
		}
		out = append(out, pc)
	}
	return out
}

// ---- 本地工具（与主实现独立） ----

func validFor(v ontology.Value, t ontology.PropertyType) bool {
	switch t {
	case ontology.TypeString:
		return v.Kind == ontology.TypeString
	case ontology.TypeInt:
		return v.Kind == ontology.TypeInt
	case ontology.TypeFloat:
		return v.Kind == ontology.TypeInt || v.Kind == ontology.TypeFloat
	case ontology.TypeBool:
		return v.Kind == ontology.TypeBool
	}
	return false
}

func valuesEqual(a, b ontology.Value) bool {
	numeric := func(v ontology.Value) bool {
		return v.Kind == ontology.TypeInt || v.Kind == ontology.TypeFloat
	}
	if a.Kind == b.Kind {
		switch a.Kind {
		case ontology.TypeString:
			return a.Str == b.Str
		case ontology.TypeInt:
			return a.Int == b.Int
		case ontology.TypeFloat:
			return a.Flt == b.Flt
		case ontology.TypeBool:
			return a.Bol == b.Bol
		}
		return true
	}
	if numeric(a) && numeric(b) {
		toF := func(v ontology.Value) float64 {
			if v.Kind == ontology.TypeInt {
				return float64(v.Int)
			}
			return v.Flt
		}
		return toF(a) == toF(b)
	}
	return false
}

func typeKeys(s ontology.Schema) map[string]bool {
	out := map[string]bool{}
	for id := range s.Types {
		out[id] = true
	}
	return out
}

func linkTypeKeys(s ontology.Schema) map[string]bool {
	out := map[string]bool{}
	for id := range s.LinkTypes {
		out[id] = true
	}
	return out
}

func propKeys(props map[string]ontology.Property) map[string]bool {
	out := map[string]bool{}
	for id := range props {
		out[id] = true
	}
	return out
}

func objectKeys(s *ontology.Snapshot) map[string]bool {
	out := map[string]bool{}
	for k := range s.Objects {
		out[k] = true
	}
	return out
}

func linkKeys(s *ontology.Snapshot) map[string]bool {
	out := map[string]bool{}
	for k := range s.Links {
		out[k] = true
	}
	return out
}

func unionKeys(a, b map[string]bool) []string {
	out := map[string]bool{}
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "; "
		}
		out += p
	}
	return out
}
