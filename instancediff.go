package ontology

import "sort"

// ObjectDiffKind 是对象实例差异的类别：创建、删除、更新，三者互斥。
type ObjectDiffKind string

const (
	ObjectCreated ObjectDiffKind = "created"
	ObjectDeleted ObjectDiffKind = "deleted"
	ObjectUpdated ObjectDiffKind = "updated"
)

// ObjectDeleteReason 区分对象删除的原因。
type ObjectDeleteReason string

const (
	// ReasonObjectExplicit 对象本身被显式删除。
	ReasonObjectExplicit ObjectDeleteReason = "explicit"
	// ReasonTypeDeprecated 对象类型被整体废弃，其全部实例统一归因于此，
	// 即使数据层面的最终效果与显式删除相同，也不与之混同。
	ReasonTypeDeprecated ObjectDeleteReason = "type_deprecated"
)

// ChangeKind 是单个属性取值差异的类别。
type ChangeKind string

const (
	// ChangeValueChanged 取值真实发生了变化（旧值按新类型解读有效）。
	ChangeValueChanged ChangeKind = "value_changed"
	// ChangeTypeIncompatible 属性类型收紧，旧值按新类型解读不再有效。
	// 与取值真实变化是两个不同的差异类别。
	ChangeTypeIncompatible ChangeKind = "type_incompatible"
	// ChangeValueAdded 旧快照无此取值，新快照出现。
	ChangeValueAdded ChangeKind = "value_added"
	// ChangeValueRemoved 旧快照有此取值，新快照不再存在。
	ChangeValueRemoved ChangeKind = "value_removed"
)

// PropertyChange 精确到单个属性的取值差异。
// OldKey/NewKey 记录属性在两侧快照中的名称：属性被重命名时
// 二者不同，但 PropID 相同——标识变化与取值变化分别呈现。
type PropertyChange struct {
	PropID   string       `json:"propId"`
	OldKey   string       `json:"oldKey,omitempty"`
	NewKey   string       `json:"newKey,omitempty"`
	Kind     ChangeKind   `json:"kind"`
	OldType  PropertyType `json:"oldType,omitempty"`
	NewType  PropertyType `json:"newType,omitempty"`
	OldValue *Value       `json:"oldValue,omitempty"`
	NewValue *Value       `json:"newValue,omitempty"`
}

// ObjectDiff 是单个对象实例的差异。Kind 为 Updated 时
// Changes 精确列出发生变化的属性；未变化的属性不出现，
// 未发生任何属性变化的对象不会出现在差异结果中。
type ObjectDiff struct {
	Key     string             `json:"key"`
	TypeID  string             `json:"typeId"`
	Kind    ObjectDiffKind     `json:"kind"`
	Reason  ObjectDeleteReason `json:"reason,omitempty"`
	Changes []PropertyChange   `json:"changes,omitempty"`
}

// LinkDiffKind 是链接实例差异的类别。
type LinkDiffKind string

const (
	LinkCreated LinkDiffKind = "created"
	LinkDeleted LinkDiffKind = "deleted"
)

// LinkDeleteReason 区分链接消失的原因，三者互斥且都可区分报告。
type LinkDeleteReason string

const (
	// ReasonLinkExplicit 链接被显式删除（端点对象仍然存活）。
	ReasonLinkExplicit LinkDeleteReason = "explicit"
	// ReasonEndpointDeleted 链接本身未变，但其端点对象被删除而失效。
	ReasonEndpointDeleted LinkDeleteReason = "endpoint_deleted"
	// ReasonLinkTypeDeprecated 链接类型被移除。
	ReasonLinkTypeDeprecated LinkDeleteReason = "link_type_deprecated"
)

// LinkDiff 是单个链接实例的差异。
type LinkDiff struct {
	Key              string           `json:"key"`
	TypeID           string           `json:"typeId"`
	Kind             LinkDiffKind     `json:"kind"`
	Reason           LinkDeleteReason `json:"reason,omitempty"`
	MissingEndpoints []ObjectRef      `json:"missingEndpoints,omitempty"`
}

// InstanceDiff 是完整的实例层差异。所有列表按键排序，保证输出确定。
type InstanceDiff struct {
	Objects []ObjectDiff `json:"objects"`
	Links   []LinkDiff   `json:"links"`
}

// Empty 报告实例层是否完全没有差异。
func (d *InstanceDiff) Empty() bool {
	return len(d.Objects) == 0 && len(d.Links) == 0
}

// diffInstances 计算实例层差异。fastPath 为 true（同一谱系）时
// 只检查 ModRev 大于基准修订号的实体与墓碑，工作量与未变化实体
// 总数无关；否则回退为全量连接（Stats.FullScan 置位）。
func diffInstances(oldS, newS *Snapshot, al *Alignment, fastPath bool) (*InstanceDiff, Stats, error) {
	d := &InstanceDiff{}
	var st Stats
	st.FullScan = !fastPath

	objCand, linkCand, tombs := candidates(oldS, newS, fastPath, &st)

	for _, key := range objCand {
		cur := newS.Objects[key]
		base, ok := oldS.Objects[key]
		st.BaseLookups++
		if !ok {
			d.Objects = append(d.Objects, ObjectDiff{Key: key, TypeID: cur.TypeID, Kind: ObjectCreated})
			continue
		}
		changes := diffObjectValues(al, cur.TypeID, base.Values, cur.Values)
		if len(changes) > 0 {
			d.Objects = append(d.Objects, ObjectDiff{
				Key: key, TypeID: cur.TypeID, Kind: ObjectUpdated, Changes: changes,
			})
		}
	}
	for _, key := range linkCand {
		cur := newS.Links[key]
		st.BaseLookups++
		if _, ok := oldS.Links[key]; !ok {
			d.Links = append(d.Links, LinkDiff{Key: key, TypeID: cur.TypeID, Kind: LinkCreated})
		}
	}
	for _, tomb := range tombs {
		key := tomb.key
		if !tomb.link {
			base, ok := oldS.Objects[key]
			st.BaseLookups++
			if !ok {
				// 在比对窗口内创建又删除：净效果为零，不产生差异。
				continue
			}
			reason := ReasonObjectExplicit
			if al.DeprecatedTypes[base.TypeID] {
				reason = ReasonTypeDeprecated
			}
			d.Objects = append(d.Objects, ObjectDiff{
				Key: key, TypeID: base.TypeID, Kind: ObjectDeleted, Reason: reason,
			})
			continue
		}
		base, ok := oldS.Links[key]
		st.BaseLookups++
		if !ok {
			continue
		}
		d.Links = append(d.Links, classifyLinkDeletion(base, newS, al))
	}

	sort.Slice(d.Objects, func(i, j int) bool { return d.Objects[i].Key < d.Objects[j].Key })
	sort.Slice(d.Links, func(i, j int) bool { return d.Links[i].Key < d.Links[j].Key })
	return d, st, nil
}

// tombKey 是一个待判定的删除候选：实体键加类别标记。
type tombKey struct {
	key  string
	link bool
}

// candidates 选出需要检查的实体键。fastPath 下只取修订号大于
// 基准快照修订号的实体与墓碑；否则取全量键集。
func candidates(oldS, newS *Snapshot, fastPath bool, st *Stats) (objKeys, linkKeys []string, tombs []tombKey) {
	if fastPath {
		for key, o := range newS.Objects {
			if o.ModRev > oldS.Revision {
				objKeys = append(objKeys, key)
			}
		}
		for key, l := range newS.Links {
			if l.ModRev > oldS.Revision {
				linkKeys = append(linkKeys, key)
			}
		}
		for key, tb := range newS.Tombs {
			if tb.ModRev > oldS.Revision {
				tombs = append(tombs, tombKey{key: key, link: tb.Link})
			}
		}
	} else {
		for key := range newS.Objects {
			objKeys = append(objKeys, key)
		}
		for key := range newS.Links {
			linkKeys = append(linkKeys, key)
		}
		// 全量模式下删除通过“旧有新无”发现，不依赖墓碑。
		for key := range oldS.Objects {
			if _, ok := newS.Objects[key]; !ok {
				tombs = append(tombs, tombKey{key: key})
			}
		}
		for key := range oldS.Links {
			if _, ok := newS.Links[key]; !ok {
				tombs = append(tombs, tombKey{key: key, link: true})
			}
		}
	}
	st.Candidates = len(objKeys) + len(linkKeys)
	st.Tombstones = len(tombs)
	sort.Strings(objKeys)
	sort.Strings(linkKeys)
	sort.Slice(tombs, func(i, j int) bool { return tombs[i].key < tombs[j].key })
	return objKeys, linkKeys, tombs
}

// classifyLinkDeletion 判定一条消失的链接的删除原因：
// 链接类型被移除 > 端点对象被删除 > 显式删除。
// 判定完全由数据推导，不依赖平台写入的删除标记。
func classifyLinkDeletion(base Link, newS *Snapshot, al *Alignment) LinkDiff {
	d := LinkDiff{Key: base.key(), TypeID: base.TypeID, Kind: LinkDeleted}
	if al.DeprecatedLinkTypes[base.TypeID] {
		d.Reason = ReasonLinkTypeDeprecated
		return d
	}
	var missing []ObjectRef
	if _, ok := newS.Objects[base.Source.key()]; !ok {
		missing = append(missing, base.Source)
	}
	if _, ok := newS.Objects[base.Target.key()]; !ok {
		missing = append(missing, base.Target)
	}
	if len(missing) > 0 {
		d.Reason = ReasonEndpointDeleted
		d.MissingEndpoints = missing
		return d
	}
	d.Reason = ReasonLinkExplicit
	return d
}

// diffObjectValues 在口径对齐的基础上逐属性比对同一对象的两侧取值。
// 属性按稳定 ID 对齐：重命名（Key 变化）不会产生删除加新增，
// 只会体现为同一条 PropertyChange 上 OldKey != NewKey。
func diffObjectValues(al *Alignment, typeID string, oldV, newV map[string]Value) []PropertyChange {
	ta := al.Types[typeID]
	if ta == nil {
		return nil
	}
	var out []PropertyChange
	for _, pid := range sortedKeys(propIDSet(ta.Props)) {
		pa := ta.Props[pid]
		oldVal, oldHas := oldV[pid]
		newVal, newHas := newV[pid]
		pc := PropertyChange{PropID: pid}
		if pa.Old != nil {
			pc.OldKey = pa.Old.Key
			pc.OldType = pa.Old.Type
		}
		if pa.New != nil {
			pc.NewKey = pa.New.Key
			pc.NewType = pa.New.Type
		}
		retyped := pa.Old != nil && pa.New != nil && pa.Old.Type != pa.New.Type
		switch {
		case oldHas && !newHas:
			v := oldVal
			pc.OldValue = &v
			if retyped && !oldVal.ValidFor(pa.New.Type) {
				pc.Kind = ChangeTypeIncompatible
			} else {
				pc.Kind = ChangeValueRemoved
			}
		case !oldHas && newHas:
			v := newVal
			pc.NewValue = &v
			pc.Kind = ChangeValueAdded
		case oldHas && newHas:
			if retyped && !oldVal.ValidFor(pa.New.Type) {
				v, w := oldVal, newVal
				pc.OldValue, pc.NewValue = &v, &w
				pc.Kind = ChangeTypeIncompatible
			} else if !EqualValues(oldVal, newVal) {
				v, w := oldVal, newVal
				pc.OldValue, pc.NewValue = &v, &w
				pc.Kind = ChangeValueChanged
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

func propIDSet(props map[string]propAlignment) map[string]bool {
	out := make(map[string]bool, len(props))
	for id := range props {
		out[id] = true
	}
	return out
}
