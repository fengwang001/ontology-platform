// Package ontology 实现通用本体模型（对象、带方向/多重性的链接、两级权限）
// 以及在调用者可见且可遍历的链接子图上的确定性环检测器 HasCycle。
package ontology

import (
	"fmt"
	"strings"
	"sync"
)

// Direction 表示链接类型的方向语义。
type Direction int

const (
	// Directed 有向：只能沿 Source -> Target 方向遍历。
	Directed Direction = iota
	// Bidirectional 双向：两个方向都可遍历。
	Bidirectional
)

// ObjectType 描述一类对象。
type ObjectType struct {
	Name string
}

// LinkType 描述一类链接：方向、是否允许自环、是否允许同一对象对间的多重同类型链接。
type LinkType struct {
	Name       string
	Direction  Direction
	AllowSelf  bool
	AllowMulti bool
	SourceType string
	TargetType string
}

// Object 是本体中的一个对象实例。
type Object struct {
	ID   string
	Type string
}

// Link 是两个对象实例之间的一条链接实例。
type Link struct {
	ID     string
	Type   string
	Source string
	Target string
}

// Permissions 描述单个调用者的权限矩阵。
type Permissions struct {
	// ExistObject 存在性权限：对哪些对象有权“看到其存在”。
	ExistObject map[string]bool
	// TraverseLink 遍历权限：对哪些链接实例有权遍历。
	TraverseLink map[string]bool
}

// Result 是 HasCycle 的判定结果。
type Result struct {
	HasCycle bool
	// Evidence 是构成某个环的“最小对象集合之一”（恰为该简单环上的全部顶点）。
	// 无环时为 nil；环为自环时含 1 个对象；双向往返环含 2 个对象。
	Evidence []string
}

// Metrics 是内部度量：仅统计一次判定中实际访问的可见对象/链接数目。
// 不随调用者完全不可见的对象与链接规模增长。
type Metrics struct {
	VisibleObjects int
	ScannedLinks   int
	VisitedObjects int
	VisitedLinks   int
}

// 判定次序与错误定义。
var (
	ErrInvalidCaller = fmt.Errorf("invalid caller identifier")
	ErrAlreadyExists = fmt.Errorf("already exists")
	ErrNotFound      = fmt.Errorf("not found")
	ErrInvalidLink   = fmt.Errorf("invalid link")
)

// Graph 是并发安全的本体链接图。
//
// 所有变更（对象/链接增删、权限变更）在写锁下整体生效，保证每次 HasCycle
// 在共享锁下看到的是某个串行时刻的完整状态（线性一致性快照）。
type Graph struct {
	mu sync.RWMutex

	objectTypes map[string]ObjectType
	linkTypes   map[string]LinkType
	objects     map[string]Object
	links       map[string]Link

	// incident 是无方向的实例邻接索引：对象 -> 与其关联的链接 ID。
	// 注意：该索引包含全部链接（含对某调用者不可见的），但 HasCycle
	// 只会从调用者的可见对象集合出发访问其中条目，不可见对象永远不会
	// 被展开，因此不可见规模不进入判定开销。
	incident map[string][]string

	// pairIndex 用于落实 LinkType.AllowMulti：
	// 有向链接键为 "type|src->dst"，双向链接键为 "type|lo<->hi"。
	pairIndex map[string]map[string]bool

	permissions map[string]Permissions
}

// NewGraph 创建空图。
func NewGraph() *Graph {
	return &Graph{
		objectTypes: map[string]ObjectType{},
		linkTypes:   map[string]LinkType{},
		objects:     map[string]Object{},
		links:       map[string]Link{},
		incident:    map[string][]string{},
		pairIndex:   map[string]pairKeySet{},
		permissions: map[string]Permissions{},
	}
}

type pairKeySet = map[string]bool

// validateCaller 规定调用者标识：非空、去空白后长度不超过 128。
// 参数非法在所有判定分支之前处理。
func validateCaller(caller string) error {
	if strings.TrimSpace(caller) == "" || len(caller) > 128 {
		return ErrInvalidCaller
	}
	return nil
}

func (g *Graph) AddObjectType(t ObjectType) error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: empty object type name", ErrInvalidLink)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[t.Name]; ok {
		return fmt.Errorf("%w: object type %q", ErrAlreadyExists, t.Name)
	}
	g.objectTypes[t.Name] = t
	return nil
}

func (g *Graph) AddLinkType(t LinkType) error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("%w: empty link type name", ErrInvalidLink)
	}
	if t.Direction != Directed && t.Direction != Bidirectional {
		return fmt.Errorf("%w: bad direction", ErrInvalidLink)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[t.Name]; ok {
		return fmt.Errorf("%w: link type %q", ErrAlreadyExists, t.Name)
	}
	g.linkTypes[t.Name] = t
	return nil
}

func (g *Graph) CreateObject(o Object) error {
	if strings.TrimSpace(o.ID) == "" {
		return fmt.Errorf("%w: empty object id", ErrInvalidLink)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[o.ID]; ok {
		return fmt.Errorf("%w: object %q", ErrAlreadyExists, o.ID)
	}
	if o.Type != "" {
		if _, ok := g.objectTypes[o.Type]; !ok {
			return fmt.Errorf("%w: object type %q", ErrNotFound, o.Type)
		}
	}
	g.objects[o.ID] = o
	g.incident[o.ID] = nil
	return nil
}

// CreateLink 校验链接类型方向、自环与多重性约束后整体加入图。
func (g *Graph) CreateLink(l Link) error {
	if strings.TrimSpace(l.ID) == "" {
		return fmt.Errorf("%w: empty link id", ErrInvalidLink)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.links[l.ID]; ok {
		return fmt.Errorf("%w: link %q", ErrAlreadyExists, l.ID)
	}
	lt, ok := g.linkTypes[l.Type]
	if !ok {
		return fmt.Errorf("%w: link type %q", ErrNotFound, l.Type)
	}
	src, ok1 := g.objects[l.Source]
	dst, ok2 := g.objects[l.Target]
	if !ok1 || !ok2 {
		return fmt.Errorf("%w: endpoint missing", ErrInvalidLink)
	}
	if lt.SourceType != "" && src.Type != lt.SourceType {
		return fmt.Errorf("%w: source type mismatch", ErrInvalidLink)
	}
	if lt.TargetType != "" && dst.Type != lt.TargetType {
		return fmt.Errorf("%w: target type mismatch", ErrInvalidLink)
	}
	if l.Source == l.Target && !lt.AllowSelf {
		return fmt.Errorf("%w: self loop disallowed by type %q", ErrInvalidLink, l.Type)
	}
	key := pairKey(l.Source, l.Target, lt.Direction, l.Type)
	if !lt.AllowMulti {
		if existing, ok := g.pairIndex[key]; ok && len(existing) > 0 {
			return fmt.Errorf("%w: duplicate link of type %q between same pair", ErrInvalidLink, l.Type)
		}
	}
	g.links[l.ID] = l
	g.incident[l.Source] = append(g.incident[l.Source], l.ID)
	if l.Target != l.Source {
		g.incident[l.Target] = append(g.incident[l.Target], l.ID)
	}
	if g.pairIndex[key] == nil {
		g.pairIndex[key] = map[string]bool{}
	}
	g.pairIndex[key][l.ID] = true
	return nil
}

func (g *Graph) DeleteLink(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.links[id]
	if !ok {
		return fmt.Errorf("%w: link %q", ErrNotFound, id)
	}
	lt := g.linkTypes[l.Type]
	key := pairKey(l.Source, l.Target, lt.Direction, l.Type)
	delete(g.pairIndex[key], id)
	if len(g.pairIndex[key]) == 0 {
		delete(g.pairIndex, key)
	}
	delete(g.links, id)
	g.incident[l.Source] = removeString(g.incident[l.Source], id)
	if l.Target != l.Source {
		g.incident[l.Target] = removeString(g.incident[l.Target], id)
	}
	return nil
}

// SetPermissions 以一次整体替换的方式生效调用者权限。
// nil 视为对应维度的空集合。
func (g *Graph) SetPermissions(caller string, p Permissions) {
	if err := validateCaller(caller); err != nil {
		panic(err)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.permissions[caller] = p.normalized()
}

// HasCycle 在调用者当前可见且可遍历的链接子图上判定是否存在环。
// 判定次序：参数非法 > 可见对象为空（无环，非错误）> 正常检测。
func (g *Graph) HasCycle(caller string) (Result, error) {
	res, _, err := g.hasCycleMetrics(caller)
	return res, err
}

func (g *Graph) hasCycleMetrics(caller string) (Result, Metrics, error) {
	if err := validateCaller(caller); err != nil {
		return Result{}, Metrics{}, err
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, res, metrics, err := g.buildSnapshot(caller)
	return res, metrics, err
}

func (p Permissions) normalized() Permissions {
	n := Permissions{
		ExistObject:  make(map[string]bool, len(p.ExistObject)),
		TraverseLink: make(map[string]bool, len(p.TraverseLink)),
	}
	for k, v := range p.ExistObject {
		if v {
			n.ExistObject[k] = true
		}
	}
	for k, v := range p.TraverseLink {
		if v {
			n.TraverseLink[k] = true
		}
	}
	return n
}

func pairKey(src, dst string, dir Direction, linkType string) string {
	if dir == Bidirectional {
		a, b := src, dst
		if a > b {
			a, b = b, a
		}
		return linkType + "|" + a + "<->" + b
	}
	return linkType + "|" + src + "->" + dst
}

func removeString(xs []string, x string) []string {
	out := xs[:0]
	for _, v := range xs {
		if v != x {
			out = append(out, v)
		}
	}
	return out
}
