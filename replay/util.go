package replay

import "reflect"

// 本文件提供深拷贝与相等性判定工具。所有校验路径都通过深拷贝
// 隔离工作状态，保证原始良好快照与差异记录不被修改。

// cloneSchema 深拷贝结构层状态。
func cloneSchema(s Schema) Schema {
	out := Schema{
		ObjectTypes: make(map[string]ObjectTypeDef, len(s.ObjectTypes)),
		LinkTypes:   make(map[string]LinkTypeDef, len(s.LinkTypes)),
	}
	for id, def := range s.ObjectTypes {
		props := make(map[string]struct{}, len(def.Properties))
		for p := range def.Properties {
			props[p] = struct{}{}
		}
		out.ObjectTypes[id] = ObjectTypeDef{TypeID: def.TypeID, Properties: props}
	}
	for id, def := range s.LinkTypes {
		constraints := make(map[string]PropertyValue, len(def.Constraints))
		for k, v := range def.Constraints {
			constraints[k] = v
		}
		out.LinkTypes[id] = LinkTypeDef{
			TypeID:      def.TypeID,
			SourceType:  def.SourceType,
			TargetType:  def.TargetType,
			Constraints: constraints,
		}
	}
	return out
}

// cloneObjects 深拷贝对象实例集合。
func cloneObjects(in map[string]ObjectInstance) map[string]ObjectInstance {
	out := make(map[string]ObjectInstance, len(in))
	for id, obj := range in {
		props := make(map[string]PropertyValue, len(obj.Properties))
		for k, v := range obj.Properties {
			props[k] = v
		}
		out[id] = ObjectInstance{ObjectID: obj.ObjectID, ObjectType: obj.ObjectType, Properties: props}
	}
	return out
}

// cloneLinks 深拷贝链接实例集合。
func cloneLinks(in map[LinkKey]LinkInstance) map[LinkKey]LinkInstance {
	out := make(map[LinkKey]LinkInstance, len(in))
	for k, l := range in {
		out[k] = l
	}
	return out
}

// stateFromSnapshot 从良好快照深拷贝出重放用的工作状态。
func stateFromSnapshot(snap *Snapshot) *State {
	return &State{
		Schema:  cloneSchema(snap.Schema),
		Objects: cloneObjects(snap.Objects),
		Links:   cloneLinks(snap.Links),
	}
}

// cloneObject 深拷贝一个对象实例。
func cloneObject(obj ObjectInstance) ObjectInstance {
	props := make(map[string]PropertyValue, len(obj.Properties))
	for k, v := range obj.Properties {
		props[k] = v
	}
	return ObjectInstance{ObjectID: obj.ObjectID, ObjectType: obj.ObjectType, Properties: props}
}

// cloneObjectTypeDef 深拷贝一个对象类型定义。
func cloneObjectTypeDef(def ObjectTypeDef) ObjectTypeDef {
	props := make(map[string]struct{}, len(def.Properties))
	for p := range def.Properties {
		props[p] = struct{}{}
	}
	return ObjectTypeDef{TypeID: def.TypeID, Properties: props}
}

// cloneLinkTypeDef 深拷贝一个链接类型定义。
func cloneLinkTypeDef(def LinkTypeDef) LinkTypeDef {
	constraints := make(map[string]PropertyValue, len(def.Constraints))
	for k, v := range def.Constraints {
		constraints[k] = v
	}
	return LinkTypeDef{
		TypeID:      def.TypeID,
		SourceType:  def.SourceType,
		TargetType:  def.TargetType,
		Constraints: constraints,
	}
}

// cloneSnapshot 深拷贝整份快照（含索引），供参照模型与测试使用。
func cloneSnapshot(snap *Snapshot) *Snapshot {
	return &Snapshot{
		Schema:  cloneSchema(snap.Schema),
		Objects: cloneObjects(snap.Objects),
		Links:   cloneLinks(snap.Links),
		Indexes: cloneIndexes(snap.Indexes),
	}
}

// cloneIndexes 深拷贝快照索引。
func cloneIndexes(in SnapshotIndexes) SnapshotIndexes {
	out := SnapshotIndexes{
		ObjectTypeCounts:    make(map[string]int, len(in.ObjectTypeCounts)),
		LinkTypeCounts:      make(map[string]int, len(in.LinkTypeCounts)),
		LinkEndpointCounts:  make(map[string]int, len(in.LinkEndpointCounts)),
		LinkSourceCounts:    make(map[LinkSourceKey]int, len(in.LinkSourceCounts)),
		PropertyUsageCounts: make(map[TypePropertyKey]int, len(in.PropertyUsageCounts)),
	}
	for k, v := range in.ObjectTypeCounts {
		out.ObjectTypeCounts[k] = v
	}
	for k, v := range in.LinkTypeCounts {
		out.LinkTypeCounts[k] = v
	}
	for k, v := range in.LinkEndpointCounts {
		out.LinkEndpointCounts[k] = v
	}
	for k, v := range in.LinkSourceCounts {
		out.LinkSourceCounts[k] = v
	}
	for k, v := range in.PropertyUsageCounts {
		out.PropertyUsageCounts[k] = v
	}
	return out
}

// BuildIndexes 从快照内容派生全部索引。
func BuildIndexes(objects map[string]ObjectInstance, links map[LinkKey]LinkInstance) SnapshotIndexes {
	idx := SnapshotIndexes{
		ObjectTypeCounts:    map[string]int{},
		LinkTypeCounts:      map[string]int{},
		LinkEndpointCounts:  map[string]int{},
		LinkSourceCounts:    map[LinkSourceKey]int{},
		PropertyUsageCounts: map[TypePropertyKey]int{},
	}
	for _, obj := range objects {
		idx.ObjectTypeCounts[obj.ObjectType]++
		for p := range obj.Properties {
			idx.PropertyUsageCounts[TypePropertyKey{TypeID: obj.ObjectType, Property: p}]++
		}
	}
	for _, l := range links {
		idx.LinkTypeCounts[l.LinkTypeID]++
		idx.LinkEndpointCounts[l.SourceID]++
		idx.LinkEndpointCounts[l.TargetID]++
		idx.LinkSourceCounts[LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}]++
	}
	return idx
}

// NewSnapshot 构造一份快照并自动派生索引。
func NewSnapshot(schema Schema, objects map[string]ObjectInstance, links map[LinkKey]LinkInstance) *Snapshot {
	return &Snapshot{
		Schema:  schema,
		Objects: objects,
		Links:   links,
		Indexes: BuildIndexes(objects, links),
	}
}

// valuesEqual 判定两个属性值是否相等（不区分具体类型，按值深比较）。
func valuesEqual(a, b PropertyValue) bool {
	return reflect.DeepEqual(a, b)
}

// propsEqual 判定两个属性集合是否逐项相等。
func propsEqual(a, b map[string]PropertyValue) bool {
	if len(a) != len(b) {
		return false
	}
	for k, va := range a {
		vb, ok := b[k]
		if !ok || !valuesEqual(va, vb) {
			return false
		}
	}
	return true
}

// objectEqual 判定两个对象实例是否完全相等。
func objectEqual(a, b ObjectInstance) bool {
	return a.ObjectID == b.ObjectID &&
		a.ObjectType == b.ObjectType &&
		propsEqual(a.Properties, b.Properties)
}

// objectTypeEqual 判定两个对象类型定义是否相等。
func objectTypeEqual(a, b ObjectTypeDef) bool {
	if a.TypeID != b.TypeID || len(a.Properties) != len(b.Properties) {
		return false
	}
	for p := range a.Properties {
		if _, ok := b.Properties[p]; !ok {
			return false
		}
	}
	return true
}

// linkTypeEqual 判定两个链接类型定义是否相等。
func linkTypeEqual(a, b LinkTypeDef) bool {
	return a.TypeID == b.TypeID &&
		a.SourceType == b.SourceType &&
		a.TargetType == b.TargetType &&
		propsEqual(a.Constraints, b.Constraints)
}
