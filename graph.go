package ontology

import (
	"fmt"
	"sync"
)

// object 是对象实例的内部表示。
type object struct {
	typ      ObjectTypeID
	isolated bool
}

// linkType 是链接类型的内部表示。
type linkType struct {
	spec       LinkTypeSpec
	restricted map[Principal]bool // nil 表示所有人可见
}

// visibleTo 报告该链接类型对给定权限主体是否可见。
func (lt *linkType) visibleTo(p Principal) bool {
	if lt.restricted == nil {
		return true
	}
	return lt.restricted[p]
}

// link 是链接实例的内部表示。
type link struct {
	id   LinkID
	typ  *linkType
	from ObjectID
	to   ObjectID
}

// otherEnd 返回链接在对象 u 另一侧的对象。调用前需保证 u 是端点之一，
// 且遍历方向合法（单向链接只会出现在 from 端的出边表中）。
func otherEnd(l *link, u ObjectID) ObjectID {
	if l.from == u {
		return l.to
	}
	return l.from
}

// Graph 是本体链接图。所有变更与查询都通过一把 sync.RWMutex 串行化：
// 写操作持有写锁，查询在整个计算期间持有读锁，因此每个操作都可视为在
// 获得锁的瞬间原子生效，查询看到的必然是某一时刻的完整快照。
type Graph struct {
	mu          sync.RWMutex
	categories  []Category
	catRank     map[Category]int
	objectTypes map[ObjectTypeID]ObjectTypeSpec
	linkTypes   map[LinkTypeID]*linkType
	objects     map[ObjectID]*object
	links       map[LinkID]*link
	out         map[ObjectID][]*link // 出边邻接表：单向链接只挂在 from 端
}

// NewGraph 以固定的对象类型集合与固定的类别集合创建图。
// categories 的顺序即类别字典序平局消解所用的全序。
func NewGraph(objectTypes []ObjectTypeSpec, categories []Category) (*Graph, error) {
	if len(categories) == 0 {
		return nil, fmt.Errorf("ontology: category set must not be empty")
	}
	g := &Graph{
		catRank:     make(map[Category]int, len(categories)),
		objectTypes: make(map[ObjectTypeID]ObjectTypeSpec, len(objectTypes)),
		linkTypes:   make(map[LinkTypeID]*linkType),
		objects:     make(map[ObjectID]*object),
		links:       make(map[LinkID]*link),
		out:         make(map[ObjectID][]*link),
	}
	for i, c := range categories {
		if _, dup := g.catRank[c]; dup {
			return nil, fmt.Errorf("ontology: duplicate category %q", c)
		}
		g.catRank[c] = i
		g.categories = append(g.categories, c)
	}
	for _, t := range objectTypes {
		if t.ID == "" {
			return nil, fmt.Errorf("ontology: empty object type id")
		}
		if _, dup := g.objectTypes[t.ID]; dup {
			return nil, fmt.Errorf("ontology: duplicate object type %q", t.ID)
		}
		g.objectTypes[t.ID] = t
	}
	return g, nil
}

// RegisterLinkType 注册（或整体替换）一个链接类型。
// 替换会影响该类型已有链接实例的可见性与代价，属于一次写操作。
func (g *Graph) RegisterLinkType(spec LinkTypeSpec) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if spec.ID == "" {
		return fmt.Errorf("ontology: empty link type id")
	}
	if _, ok := g.objectTypes[spec.From]; !ok {
		return fmt.Errorf("ontology: unknown from object type %q", spec.From)
	}
	if _, ok := g.objectTypes[spec.To]; !ok {
		return fmt.Errorf("ontology: unknown to object type %q", spec.To)
	}
	if _, ok := g.catRank[spec.Category]; !ok {
		return fmt.Errorf("ontology: unknown category %q", spec.Category)
	}
	if spec.Cost < 0 || spec.Cost > MaxLinkCost {
		return fmt.Errorf("ontology: link cost %d out of range [0, %d]", spec.Cost, MaxLinkCost)
	}
	lt := &linkType{spec: spec}
	if len(spec.RestrictedTo) > 0 {
		lt.restricted = make(map[Principal]bool, len(spec.RestrictedTo))
		for _, p := range spec.RestrictedTo {
			lt.restricted[p] = true
		}
	}
	g.linkTypes[spec.ID] = lt
	return nil
}

// AddObject 添加一个对象实例。
func (g *Graph) AddObject(id ObjectID, typ ObjectTypeID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id == "" {
		return fmt.Errorf("ontology: empty object id")
	}
	if _, ok := g.objectTypes[typ]; !ok {
		return fmt.Errorf("ontology: unknown object type %q", typ)
	}
	if _, dup := g.objects[id]; dup {
		return fmt.Errorf("ontology: duplicate object %q", id)
	}
	g.objects[id] = &object{typ: typ}
	return nil
}

// AddLink 添加一条链接实例，并校验两端对象类型与链接类型定义相容。
// 同一对象对之间允许存在多条链接（不同类型或同类型重复均可）。
func (g *Graph) AddLink(id LinkID, typ LinkTypeID, from, to ObjectID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if id == "" {
		return fmt.Errorf("ontology: empty link id")
	}
	if _, dup := g.links[id]; dup {
		return fmt.Errorf("ontology: duplicate link %q", id)
	}
	lt, ok := g.linkTypes[typ]
	if !ok {
		return fmt.Errorf("ontology: unknown link type %q", typ)
	}
	fromObj, ok := g.objects[from]
	if !ok {
		return fmt.Errorf("ontology: unknown from object %q", from)
	}
	toObj, ok := g.objects[to]
	if !ok {
		return fmt.Errorf("ontology: unknown to object %q", to)
	}
	matchDirect := fromObj.typ == lt.spec.From && toObj.typ == lt.spec.To
	matchSwapped := fromObj.typ == lt.spec.To && toObj.typ == lt.spec.From
	if !matchDirect && !(lt.spec.Bidirectional && matchSwapped) {
		return fmt.Errorf("ontology: link %q endpoint types (%q, %q) do not match link type %q",
			id, fromObj.typ, toObj.typ, typ)
	}
	l := &link{id: id, typ: lt, from: from, to: to}
	g.links[id] = l
	g.out[from] = append(g.out[from], l)
	if lt.spec.Bidirectional && to != from {
		g.out[to] = append(g.out[to], l)
	}
	return nil
}

// RemoveLink 删除一条链接实例。
func (g *Graph) RemoveLink(id LinkID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.links[id]
	if !ok {
		return fmt.Errorf("ontology: unknown link %q", id)
	}
	delete(g.links, id)
	g.out[l.from] = removeLinkFromAdj(g.out[l.from], l)
	if l.typ.spec.Bidirectional && l.to != l.from {
		g.out[l.to] = removeLinkFromAdj(g.out[l.to], l)
	}
	return nil
}

func removeLinkFromAdj(adj []*link, l *link) []*link {
	for i, x := range adj {
		if x == l {
			return append(adj[:i], adj[i+1:]...)
		}
	}
	return adj
}

// SetIsolation 设置或取消对象实例的逻辑隔离标记。
func (g *Graph) SetIsolation(id ObjectID, isolated bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	o, ok := g.objects[id]
	if !ok {
		return fmt.Errorf("ontology: unknown object %q", id)
	}
	o.isolated = isolated
	return nil
}

// ObjectInfo 是快照中对象实例的只读视图。
type ObjectInfo struct {
	Type     ObjectTypeID
	Isolated bool
}

// LinkInfo 是快照中链接实例的只读视图。
type LinkInfo struct {
	ID   LinkID
	Type LinkTypeID
	From ObjectID
	To   ObjectID
}

// Snapshot 是图在某一时刻的完整只读深拷贝，供独立实现（如朴素穷举
// 模型）与测试使用。
type Snapshot struct {
	Categories  []Category
	ObjectTypes map[ObjectTypeID]ObjectTypeSpec
	LinkTypes   map[LinkTypeID]LinkTypeSpec
	Objects     map[ObjectID]ObjectInfo
	Links       []LinkInfo
}

// Snapshot 返回图当前时刻的完整深拷贝。
func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s := Snapshot{
		Categories:  append([]Category(nil), g.categories...),
		ObjectTypes: make(map[ObjectTypeID]ObjectTypeSpec, len(g.objectTypes)),
		LinkTypes:   make(map[LinkTypeID]LinkTypeSpec, len(g.linkTypes)),
		Objects:     make(map[ObjectID]ObjectInfo, len(g.objects)),
		Links:       make([]LinkInfo, 0, len(g.links)),
	}
	for k, v := range g.objectTypes {
		s.ObjectTypes[k] = v
	}
	for k, v := range g.linkTypes {
		spec := v.spec
		spec.RestrictedTo = append([]Principal(nil), spec.RestrictedTo...)
		s.LinkTypes[k] = spec
	}
	for k, v := range g.objects {
		s.Objects[k] = ObjectInfo{Type: v.typ, Isolated: v.isolated}
	}
	for _, l := range g.links {
		s.Links = append(s.Links, LinkInfo{ID: l.id, Type: l.typ.spec.ID, From: l.from, To: l.to})
	}
	return s
}
