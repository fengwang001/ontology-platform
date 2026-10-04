package territory

import (
	"errors"
	"sort"
)

// 全局约束（见题设）。
const (
	Root       = "WORLD"
	MaxDepth   = 5
	MaxNodes   = 100_000
	MaxExclude = 8
)

// Span 是半开叶编号区间 [Lo, Hi)。
type Span struct {
	Lo int
	Hi int
}

var (
	// ErrEmptyCode 表示出现了空的节点代码。
	ErrEmptyCode = errors.New("territory: empty node code")
	// ErrRootMissing 表示 children 中缺少根 WORLD 的条目。
	ErrRootMissing = errors.New("territory: root WORLD missing")
	// ErrRootHasParent 表示 WORLD 出现在了某个节点的子列表中。
	ErrRootHasParent = errors.New("territory: root WORLD cannot have a parent")
	// ErrDuplicateChild 表示同一节点被多次列为子节点。
	ErrDuplicateChild = errors.New("territory: duplicate child")
	// ErrOrphanNode 表示存在从根不可达的节点（含成环）。
	ErrOrphanNode = errors.New("territory: node unreachable from root")
	// ErrDepthExceeded 表示节点深度超过 MaxDepth。
	ErrDepthExceeded = errors.New("territory: depth exceeded")
	// ErrTooManyNodes 表示节点总数超过 MaxNodes。
	ErrTooManyNodes = errors.New("territory: too many nodes")
)

// Tree 是构造后不可变的地域树。
type Tree struct {
	parent map[string]string
	kids   map[string][]string
	lo     map[string]int
	hi     map[string]int
	depth  map[string]int
	leaves []string
}

// NewTree 以“父节点 -> 子节点列表”构造地域树；根必须为 WORLD。
// 每个非根节点恰有一个父节点；节点代码非空，深度不超过 MaxDepth，
// 节点总数不超过 MaxNodes。违反时返回相应哨兵错误。
func NewTree(children map[string][]string) (*Tree, error) {
	if _, ok := children[Root]; !ok {
		return nil, ErrRootMissing
	}
	for parent, kids := range children {
		if parent == "" {
			return nil, ErrEmptyCode
		}
		seen := make(map[string]struct{}, len(kids))
		for _, kid := range kids {
			if kid == "" {
				return nil, ErrEmptyCode
			}
			if kid == Root {
				return nil, ErrRootHasParent
			}
			if _, dup := seen[kid]; dup {
				return nil, ErrDuplicateChild
			}
			seen[kid] = struct{}{}
		}
	}

	t := &Tree{
		parent: make(map[string]string),
		kids:   make(map[string][]string, len(children)),
		lo:     make(map[string]int),
		hi:     make(map[string]int),
		depth:  map[string]int{Root: 0},
	}
	nodeSet := map[string]struct{}{Root: {}}
	for parent, kids := range children {
		sorted := append([]string(nil), kids...)
		sort.Strings(sorted)
		t.kids[parent] = sorted
		nodeSet[parent] = struct{}{}
		for _, kid := range sorted {
			if _, dup := t.parent[kid]; dup {
				return nil, ErrDuplicateChild
			}
			t.parent[kid] = parent
			nodeSet[kid] = struct{}{}
		}
	}
	if len(nodeSet) > MaxNodes {
		return nil, ErrTooManyNodes
	}

	// 从根 DFS：校验可达性、深度，并按确定性顺序为叶编号。
	next := 0
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{Root: gray}
	visited := 0
	var visit func(string) error
	visit = func(node string) error {
		visited++
		if d := t.depth[node]; d > MaxDepth {
			return ErrDepthExceeded
		}
		kids := t.kids[node]
		if len(kids) == 0 {
			t.lo[node] = next
			t.hi[node] = next + 1
			t.leaves = append(t.leaves, node)
			next++
			return nil
		}
		lo := next
		for _, kid := range kids {
			if color[kid] != white {
				if color[kid] == gray {
					return ErrOrphanNode
				}
				continue
			}
			color[kid] = gray
			t.depth[kid] = t.depth[node] + 1
			if err := visit(kid); err != nil {
				return err
			}
		}
		t.lo[node] = lo
		t.hi[node] = next
		color[node] = black
		return nil
	}
	if err := visit(Root); err != nil {
		return nil, err
	}
	if visited != len(nodeSet) {
		return nil, ErrOrphanNode
	}
	return t, nil
}

// Has 报告节点是否在树中。
func (t *Tree) Has(code string) bool {
	_, ok := t.lo[code]
	return ok
}

// IsLeaf 报告节点是否为叶；未知节点返回 false。
func (t *Tree) IsLeaf(code string) bool {
	lo, ok := t.lo[code]
	return ok && t.hi[code] == lo+1 && len(t.kids[code]) == 0
}

// LeafRange 返回节点覆盖的连续叶区间 [lo,hi)；未知节点第二返回值为 false。
func (t *Tree) LeafRange(code string) (Span, bool) {
	lo, ok := t.lo[code]
	if !ok {
		return Span{}, false
	}
	return Span{Lo: lo, Hi: t.hi[code]}, true
}

// Leaves 返回全部叶节点代码，顺序为确定性的深度优先次序。
func (t *Tree) Leaves() []string { return append([]string(nil), t.leaves...) }

// Parent 返回节点的父节点；根或未知节点第二返回值为 false。
func (t *Tree) Parent(code string) (string, bool) {
	p, ok := t.parent[code]
	return p, ok
}

// IsProperDescendant 报告 node 是否为 ancestor 的真后代。
func (t *Tree) IsProperDescendant(node, ancestor string) bool {
	if !t.Has(node) || !t.Has(ancestor) || node == ancestor {
		return false
	}
	for cur := node; ; {
		p, ok := t.parent[cur]
		if !ok {
			return false
		}
		if p == ancestor {
			return true
		}
		cur = p
	}
}

// Cover 返回 node 覆盖挖去 excludes 后剩余的有序不相交叶区间，
// 以及被挖去的有序洞区间。调用方需自行保证 excludes 合法。
func (t *Tree) Cover(node string, excludes []string) (keep []Span, holes []Span) {
	r, ok := t.LeafRange(node)
	if !ok {
		return nil, nil
	}
	holes = make([]Span, 0, len(excludes))
	for _, ex := range excludes {
		if er, ok := t.LeafRange(ex); ok {
			holes = append(holes, er)
		}
	}
	sort.Slice(holes, func(i, j int) bool { return holes[i].Lo < holes[j].Lo })
	cur := r.Lo
	for _, h := range holes {
		if h.Lo > cur {
			keep = append(keep, Span{Lo: cur, Hi: h.Lo})
		}
		if h.Hi > cur {
			cur = h.Hi
		}
	}
	if cur < r.Hi {
		keep = append(keep, Span{Lo: cur, Hi: r.Hi})
	}
	return keep, holes
}
