package ontology

import "fmt"

// Builder 模拟本体平台产生快照的过程：从空模式或既有快照出发，
// 应用一批结构/实例变更后构建出新的不可变快照。
// Builder 只做机械操作，不保证结果合法；合法性由 ValidateSnapshot 判定。
// 所有实例修改采用写时复制，已构建的旧快照不会被后续修改影响。
type Builder struct {
	rev  uint64
	snap Snapshot
}

// NewBuilder 创建一个空模式的构建器。
func NewBuilder(lineage string, rev uint64) *Builder {
	return &Builder{
		rev: rev,
		snap: Snapshot{
			Lineage:  lineage,
			Revision: rev,
			Schema:   Schema{Types: map[string]ObjectType{}, LinkTypes: map[string]LinkType{}},
			Objects:  map[string]Object{},
			Links:    map[string]Link{},
			Tombs:    map[string]Tombstone{},
		},
	}
}

// FromSnapshot 以既有快照为起点创建构建器（浅拷贝 + 写时复制）。
func FromSnapshot(s *Snapshot) *Builder {
	b := NewBuilder(s.Lineage, s.Revision)
	for id, t := range s.Schema.Types {
		props := make(map[string]Property, len(t.Props))
		for pid, p := range t.Props {
			props[pid] = p
		}
		t.Props = props
		b.snap.Schema.Types[id] = t
	}
	for id, lt := range s.Schema.LinkTypes {
		b.snap.Schema.LinkTypes[id] = lt
	}
	for k, o := range s.Objects {
		b.snap.Objects[k] = o
	}
	for k, l := range s.Links {
		b.snap.Links[k] = l
	}
	for k, tb := range s.Tombs {
		b.snap.Tombs[k] = tb
	}
	return b
}

// Advance 推进全局修订号，之后的变更以新修订号标记。
func (b *Builder) Advance(rev uint64) {
	if rev <= b.rev {
		panic(fmt.Sprintf("revision must increase: %d <= %d", rev, b.rev))
	}
	b.rev = rev
	b.snap.Revision = rev
}

// Build 产出当前状态的不可变快照。
func (b *Builder) Build() *Snapshot {
	s := b.snap
	return &s
}

func (b *Builder) objectType(id string) ObjectType {
	t, ok := b.snap.Schema.Types[id]
	if !ok {
		panic(fmt.Sprintf("unknown object type %q", id))
	}
	return t
}

// AddObjectType 新增对象类型，props 中必须包含主键属性。
func (b *Builder) AddObjectType(id, name string, pk Property, props ...Property) {
	if _, ok := b.snap.Schema.Types[id]; ok {
		panic(fmt.Sprintf("object type %q already exists", id))
	}
	t := ObjectType{ID: id, Name: name, PrimaryKey: pk.ID, Props: map[string]Property{pk.ID: pk}}
	for _, p := range props {
		t.Props[p.ID] = p
	}
	b.snap.Schema.Types[id] = t
}

// RemoveObjectType 整体废弃对象类型：级联删除其实例、
// 引用它的链接类型及对应链接，全部留下墓碑。
func (b *Builder) RemoveObjectType(id string) {
	b.objectType(id)
	for ltID, lt := range b.snap.Schema.LinkTypes {
		if lt.Source == id || lt.Target == id {
			b.RemoveLinkType(ltID)
		}
	}
	for key, o := range b.snap.Objects {
		if o.TypeID == id {
			delete(b.snap.Objects, key)
			b.snap.Tombs[key] = Tombstone{ModRev: b.rev}
		}
	}
	delete(b.snap.Schema.Types, id)
}

// RenameObjectType 修改对象类型显示名。
func (b *Builder) RenameObjectType(id, newName string) {
	t := b.objectType(id)
	t.Name = newName
	b.snap.Schema.Types[id] = t
}

// AddProperty 为对象类型新增属性。
func (b *Builder) AddProperty(typeID string, p Property) {
	t := b.objectType(typeID)
	if _, ok := t.Props[p.ID]; ok {
		panic(fmt.Sprintf("property %q already exists on type %q", p.ID, typeID))
	}
	t.Props[p.ID] = p
	b.snap.Schema.Types[typeID] = t
}

// RemoveProperty 移除属性（不能是主键），并从该类型全部实例上
// 抹除对应取值——这是平台侧的真实数据变更，会提升实例修订号。
func (b *Builder) RemoveProperty(typeID, propID string) {
	t := b.objectType(typeID)
	if t.PrimaryKey == propID {
		panic(fmt.Sprintf("cannot remove primary key property %q of type %q", propID, typeID))
	}
	if _, ok := t.Props[propID]; !ok {
		panic(fmt.Sprintf("unknown property %q on type %q", propID, typeID))
	}
	delete(t.Props, propID)
	b.snap.Schema.Types[typeID] = t
	for key, o := range b.snap.Objects {
		if o.TypeID != typeID {
			continue
		}
		if _, ok := o.Values[propID]; !ok {
			continue
		}
		vals := copyValues(o.Values)
		delete(vals, propID)
		o.Values = vals
		o.ModRev = b.rev
		b.snap.Objects[key] = o
	}
}

// RenameProperty 修改属性的对外名称。实例以属性 ID 索引，不受影响。
func (b *Builder) RenameProperty(typeID, propID, newKey string) {
	t := b.objectType(typeID)
	p, ok := t.Props[propID]
	if !ok {
		panic(fmt.Sprintf("unknown property %q on type %q", propID, typeID))
	}
	p.Key = newKey
	t.Props[propID] = p
	b.snap.Schema.Types[typeID] = t
}

// RetypeProperty 调整属性取值类型。平台迁移实例数据：
// 按新类型解读不再有效的取值被抹除（并提升实例修订号），
// 其余取值原样保留。
func (b *Builder) RetypeProperty(typeID, propID string, newType PropertyType) {
	t := b.objectType(typeID)
	p, ok := t.Props[propID]
	if !ok {
		panic(fmt.Sprintf("unknown property %q on type %q", propID, typeID))
	}
	p.Type = newType
	t.Props[propID] = p
	b.snap.Schema.Types[typeID] = t
	for key, o := range b.snap.Objects {
		if o.TypeID != typeID {
			continue
		}
		v, ok := o.Values[propID]
		if !ok || v.ValidFor(newType) {
			continue
		}
		vals := copyValues(o.Values)
		delete(vals, propID)
		o.Values = vals
		o.ModRev = b.rev
		b.snap.Objects[key] = o
	}
}

// ChangePrimaryKey 更换主键属性，并按新主键重建该类型实例的身份。
func (b *Builder) ChangePrimaryKey(typeID, propID string) {
	t := b.objectType(typeID)
	if _, ok := t.Props[propID]; !ok {
		panic(fmt.Sprintf("unknown property %q on type %q", propID, typeID))
	}
	t.PrimaryKey = propID
	b.snap.Schema.Types[typeID] = t
	rebuilt := map[string]Object{}
	for key, o := range b.snap.Objects {
		if o.TypeID != typeID {
			continue
		}
		delete(b.snap.Objects, key)
		pk, ok := o.Values[propID]
		if !ok {
			continue
		}
		o.PK = pk
		rebuilt[o.key()] = o
	}
	for key, o := range rebuilt {
		b.snap.Objects[key] = o
	}
}

// AddLinkType 新增链接类型。
func (b *Builder) AddLinkType(id, name, source, target string) {
	if _, ok := b.snap.Schema.LinkTypes[id]; ok {
		panic(fmt.Sprintf("link type %q already exists", id))
	}
	b.objectType(source)
	b.objectType(target)
	b.snap.Schema.LinkTypes[id] = LinkType{ID: id, Name: name, Source: source, Target: target}
}

// RemoveLinkType 移除链接类型并删除其全部实例（留下墓碑）。
func (b *Builder) RemoveLinkType(id string) {
	if _, ok := b.snap.Schema.LinkTypes[id]; !ok {
		panic(fmt.Sprintf("unknown link type %q", id))
	}
	for key, l := range b.snap.Links {
		if l.TypeID == id {
			delete(b.snap.Links, key)
			b.snap.Tombs[key] = Tombstone{Link: true, ModRev: b.rev}
		}
	}
	delete(b.snap.Schema.LinkTypes, id)
}

// SetLinkTypeEndpoints 调整链接类型两端的对象类型，
// 既有实例全部删除（留下墓碑）。
func (b *Builder) SetLinkTypeEndpoints(id, source, target string) {
	lt, ok := b.snap.Schema.LinkTypes[id]
	if !ok {
		panic(fmt.Sprintf("unknown link type %q", id))
	}
	b.objectType(source)
	b.objectType(target)
	for key, l := range b.snap.Links {
		if l.TypeID == id {
			delete(b.snap.Links, key)
			b.snap.Tombs[key] = Tombstone{Link: true, ModRev: b.rev}
		}
	}
	lt.Source = source
	lt.Target = target
	b.snap.Schema.LinkTypes[id] = lt
}

// PutObject 写入（新建或整体替换）一个对象实例。
// values 以属性 ID 索引；主键属性取值由 pk 自动补齐。
func (b *Builder) PutObject(typeID string, pk Value, values map[string]Value) {
	t := b.objectType(typeID)
	vals := copyValues(values)
	vals[t.PrimaryKey] = pk
	o := Object{TypeID: typeID, PK: pk, Values: vals, ModRev: b.rev}
	key := o.key()
	b.snap.Objects[key] = o
	delete(b.snap.Tombs, key)
}

// DeleteObject 显式删除对象实例，并级联删除引用它的链接。
func (b *Builder) DeleteObject(typeID string, pk Value) {
	ref := ObjectRef{TypeID: typeID, PK: pk}
	key := ref.key()
	if _, ok := b.snap.Objects[key]; !ok {
		panic(fmt.Sprintf("object %q does not exist", key))
	}
	delete(b.snap.Objects, key)
	b.snap.Tombs[key] = Tombstone{ModRev: b.rev}
	for lkey, l := range b.snap.Links {
		if l.Source.key() == key || l.Target.key() == key {
			delete(b.snap.Links, lkey)
			b.snap.Tombs[lkey] = Tombstone{Link: true, ModRev: b.rev}
		}
	}
}

// PutLink 写入一个链接实例。
func (b *Builder) PutLink(typeID string, source, target ObjectRef) {
	l := Link{TypeID: typeID, Source: source, Target: target, ModRev: b.rev}
	key := l.key()
	b.snap.Links[key] = l
	delete(b.snap.Tombs, key)
}

// DeleteLink 显式删除一个链接实例（端点对象不受影响）。
func (b *Builder) DeleteLink(typeID string, source, target ObjectRef) {
	l := Link{TypeID: typeID, Source: source, Target: target}
	key := l.key()
	if _, ok := b.snap.Links[key]; !ok {
		panic(fmt.Sprintf("link %q does not exist", key))
	}
	delete(b.snap.Links, key)
	b.snap.Tombs[key] = Tombstone{Link: true, ModRev: b.rev}
}

func copyValues(in map[string]Value) map[string]Value {
	out := make(map[string]Value, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
