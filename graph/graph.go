// Package graph 提供并发安全的对象图。
//
// 对象图由带类型的节点（Object）与带类型的有向边（Link）组成。
// 图支持并发读写；模式匹配基于不可变快照，因此同一图上并发匹配、
// 以及以任意边插入顺序构建的同构图，都会得到完全相同的匹配集合。
package graph

import (
	"sort"
	"sync"
)

// Object 是对象图中的节点：一个带类型与属性的对象。
type Object struct {
	ID         string
	Type       string
	Attributes map[string]any
}

// Link 是对象图中的有向边：Source --Type--> Target。
type Link struct {
	Type   string
	Source string
	Target string
}

// snapshot 是图的不可变视图，匹配过程中始终基于快照进行。
type snapshot struct {
	nodes     map[string]*Object
	byType    map[string][]string
	out       map[string]map[string][]string
	in        map[string]map[string][]string
	edges     []edge
	edgeIndex map[edge]struct{}
}

// edge 是内部使用的有向边标识。
type edge struct {
	typ    string
	source string
	target string
}

// Graph 是并发安全的对象图。
type Graph struct {
	mu   sync.RWMutex
	snap *snapshot
}

// New 创建空对象图。
func New() *Graph {
	return &Graph{snap: emptySnapshot()}
}

func emptySnapshot() *snapshot {
	return &snapshot{
		nodes:     map[string]*Object{},
		byType:    map[string][]string{},
		out:       map[string]map[string][]string{},
		in:        map[string]map[string][]string{},
		edges:     nil,
		edgeIndex: map[edge]struct{}{},
	}
}

// AddObject 加入或替换一个对象；属性会被复制，避免调用方后续修改影响图。
func (g *Graph) AddObject(o Object) error {
	if o.ID == "" {
		return ErrEmptyID
	}
	if o.Type == "" {
		return ErrEmptyType
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	next := g.snap.clone()
	if _, exists := next.nodes[o.ID]; !exists {
		next.byType[o.Type] = append(next.byType[o.Type], o.ID)
		sort.Strings(next.byType[o.Type])
	}
	attrs := make(map[string]any, len(o.Attributes))
	for key, value := range o.Attributes {
		attrs[key] = value
	}
	next.nodes[o.ID] = &Object{ID: o.ID, Type: o.Type, Attributes: attrs}
	g.snap = next
	return nil
}

// AddLink 加入一条有向边；两端对象必须已存在；重复边被忽略。
func (g *Graph) AddLink(l Link) error {
	if l.Type == "" {
		return ErrEmptyType
	}
	g.mu.Lock()
	defer g.mu.Unlock()

	if _, ok := g.snap.nodes[l.Source]; !ok {
		return ErrMissingEndpoint
	}
	if _, ok := g.snap.nodes[l.Target]; !ok {
		return ErrMissingEndpoint
	}
	if l.Source == l.Target {
		return ErrSelfLoop
	}
	e := edge{typ: l.Type, source: l.Source, target: l.Target}
	if _, dup := g.snap.edgeIndex[e]; dup {
		return nil
	}
	next := g.snap.clone()
	next.edgeIndex[e] = struct{}{}
	next.edges = append(next.edges, e)

	outAt := next.out[e.source]
	if outAt == nil {
		outAt = map[string][]string{}
		next.out[e.source] = outAt
	}
	outAt[e.typ] = appendIfAbsent(outAt[e.typ], e.target)
	sort.Strings(outAt[e.typ])

	inAt := next.in[e.target]
	if inAt == nil {
		inAt = map[string][]string{}
		next.in[e.target] = inAt
	}
	inAt[e.typ] = appendIfAbsent(inAt[e.typ], e.source)
	sort.Strings(inAt[e.typ])

	sort.Slice(next.edges, func(i, j int) bool {
		return next.edges[i].less(next.edges[j])
	})
	g.snap = next
	return nil
}

// Snapshot 返回当前图的不可变快照（供匹配器使用）。
func (g *Graph) Snapshot() Snapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	// 写操作采用写时复制并整体替换 snap；因此返回的指针永不被后续写入修改。
	return g.snap
}

// Object 按 ID 查询快照中的对象。
func (s *snapshot) Object(id string) (*Object, bool) {
	o, ok := s.nodes[id]
	return o, ok
}

// Snapshot 是图的只读视图。匹配在快照上进行，不受并发写入影响。
type Snapshot interface {
	Object(id string) (*Object, bool)
	ObjectsByType(typ string) []string
	OutNeighbors(nodeID, edgeType string) []string
	InNeighbors(nodeID, edgeType string) []string
}

// AllObjectIDs 返回快照中全部对象 ID（已排序）。
func (s *snapshot) AllObjectIDs() []string {
	ids := make([]string, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ObjectsByType 返回指定类型的全部对象 ID（已排序，顺序与插入顺序无关）。
func (s *snapshot) ObjectsByType(typ string) []string {
	return append([]string(nil), s.byType[typ]...)
}

// OutNeighbors 返回 nodeID 经 edgeType 出边可达的对象 ID（已排序）。
func (s *snapshot) OutNeighbors(nodeID, edgeType string) []string {
	return append([]string(nil), s.out[nodeID][edgeType]...)
}

// InNeighbors 返回经 edgeType 指向 nodeID 的对象 ID（已排序）。
func (s *snapshot) InNeighbors(nodeID, edgeType string) []string {
	return append([]string(nil), s.in[nodeID][edgeType]...)
}

func (e edge) less(other edge) bool {
	if e.typ != other.typ {
		return e.typ < other.typ
	}
	if e.source != other.source {
		return e.source < other.source
	}
	return e.target < other.target
}

func appendIfAbsent(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

// clone 复制所有可变结构；副本中的切片与映射与原图完全独立。
func (s *snapshot) clone() *snapshot {
	nodes := make(map[string]*Object, len(s.nodes))
	for id, node := range s.nodes {
		attrs := make(map[string]any, len(node.Attributes))
		for key, value := range node.Attributes {
			attrs[key] = value
		}
		nodes[id] = &Object{ID: node.ID, Type: node.Type, Attributes: attrs}
	}
	byType := make(map[string][]string, len(s.byType))
	for typ, ids := range s.byType {
		byType[typ] = append([]string(nil), ids...)
	}
	out := make(map[string]map[string][]string, len(s.out))
	for node, byEdge := range s.out {
		copied := make(map[string][]string, len(byEdge))
		for typ, ids := range byEdge {
			copied[typ] = append([]string(nil), ids...)
		}
		out[node] = copied
	}
	in := make(map[string]map[string][]string, len(s.in))
	for node, byEdge := range s.in {
		copied := make(map[string][]string, len(byEdge))
		for typ, ids := range byEdge {
			copied[typ] = append([]string(nil), ids...)
		}
		in[node] = copied
	}
	edges := append([]edge(nil), s.edges...)
	edgeIndex := make(map[edge]struct{}, len(s.edgeIndex))
	for e := range s.edgeIndex {
		edgeIndex[e] = struct{}{}
	}
	return &snapshot{
		nodes:     nodes,
		byType:    byType,
		out:       out,
		in:        in,
		edges:     edges,
		edgeIndex: edgeIndex,
	}
}
