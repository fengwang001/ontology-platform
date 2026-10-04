// Package territory 维护一棵不可变的地域树，并提供基于 DFS 叶编号的
// 连续叶区间与「挖洞覆盖集」表示。
package territory

import "errors"

// 哨兵错误：errors.Is 可区分。
var (
	ErrInvalidTree   = errors.New("territory: invalid tree")
	ErrUnknownNode   = errors.New("territory: unknown node")
	ErrNotDescendant = errors.New("territory: exclude is not a proper descendant")
	ErrEmptyCover    = errors.New("territory: coverage is empty")
)

// Segment 是一段半开连续叶区间 [Lo, Hi)。
type Segment struct {
	Lo int
	Hi int
}

// Tree 为不可变地域树。
type Tree struct {
	kids    map[string][]string
	parent  map[string]string
	depth   map[string]int
	rng     map[string]Segment
	leaf    map[string]bool
	leafCnt int
}

// New 根据「父代码 -> 子代码列表（按 DFS 顺序）」构造树，根必须为 WORLD。
func New(children map[string][]string) (*Tree, error) {
	const root = "WORLD"
	t := &Tree{
		kids:   make(map[string][]string, len(children)),
		parent: make(map[string]string),
		depth:  make(map[string]int),
		rng:    make(map[string]Segment),
		leaf:   make(map[string]bool),
	}

	// 结构校验：代码非空、子女不重复、单父，节点总数受限。
	seenChild := make(map[string]bool)
	total := 0
	for p, ks := range children {
		if p == "" {
			return nil, ErrInvalidTree
		}
		total++
		if total > 100000 {
			return nil, ErrInvalidTree
		}
		cp := make([]string, len(ks))
		copy(cp, ks)
		t.kids[p] = cp
		for _, k := range ks {
			if k == "" {
				return nil, ErrInvalidTree
			}
			if seenChild[k] {
				return nil, ErrInvalidTree
			}
			seenChild[k] = true
		}
	}
	if _, ok := t.kids[""]; ok {
		return nil, ErrInvalidTree
	}

	// 自根 DFS：可达性、单父、深度、子女顺序、叶编号一次完成。
	type frame struct {
		code  string
		depth int
	}
	stack := []frame{{root, 0}}
	depthSeen := map[string]int{root: 0}
	order := []string{}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		order = append(order, f.code)
		if f.depth > 5 {
			return nil, ErrInvalidTree
		}
		t.depth[f.code] = f.depth
		for _, k := range t.kids[f.code] {
			if k == root {
				return nil, ErrInvalidTree
			}
			if d, dup := depthSeen[k]; dup {
				// 已访问：重复父或环。
				_ = d
				return nil, ErrInvalidTree
			}
			depthSeen[k] = f.depth + 1
			t.parent[k] = f.code
		}
		// 逆序压栈以保持 children 声明顺序的 DFS。
		for i := len(t.kids[f.code]) - 1; i >= 0; i-- {
			stack = append(stack, frame{t.kids[f.code][i], f.depth + 1})
		}
	}

	// 所有节点（含只作为叶出现、未在 map 中列出的）都必须自根可达。
	if len(depthSeen) > 100000 {
		return nil, ErrInvalidTree
	}
	for p := range t.kids {
		if _, ok := depthSeen[p]; !ok {
			return nil, ErrInvalidTree
		}
	}

	// 叶的 DFS 编号与节点区间。迭代式后序：first/min 与 last/max。
	idx := 0
	var dfs func(code string) Segment
	dfs = func(code string) Segment {
		ks := t.kids[code]
		if len(ks) == 0 {
			t.leaf[code] = true
			r := Segment{Lo: idx, Hi: idx + 1}
			idx++
			t.rng[code] = r
			return r
		}
		lo, hi := -1, -1
		for _, k := range ks {
			kr := dfs(k)
			if lo < 0 || kr.Lo < lo {
				lo = kr.Lo
			}
			if kr.Hi > hi {
				hi = kr.Hi
			}
		}
		r := Segment{Lo: lo, Hi: hi}
		t.rng[code] = r
		return r
	}
	dfs(root)
	t.leafCnt = idx
	return t, nil
}

// Has 报告代码是否在树中。
func (t *Tree) Has(code string) bool {
	_, ok := t.rng[code]
	return ok
}

// IsLeaf 报告代码是否为叶节点（代码未知时为 false）。
func (t *Tree) IsLeaf(code string) bool { return t.leaf[code] }

// Leaves 返回树中叶的总数。
func (t *Tree) Leaves() int { return t.leafCnt }

// IsProperDescendant 报告 desc 是否为 anc 的真后代；任一代码未知返回 false。
func (t *Tree) IsProperDescendant(anc, desc string) bool {
	if !t.Has(anc) || !t.Has(desc) {
		return false
	}
	for p := t.parent[desc]; p != ""; p = t.parent[p] {
		if p == anc {
			return true
		}
	}
	return false
}

// NodeRange 返回节点子树叶构成的半开区间 [Lo, Hi)。
func (t *Tree) NodeRange(code string) (Segment, bool) {
	r, ok := t.rng[code]
	return r, ok
}

// Cover 返回 node 覆盖集挖去 excludes 各子区间后的有序不相交段。
func (t *Tree) Cover(node string, excludes []string) ([]Segment, error) {
	base, ok := t.rng[node]
	if !ok {
		return nil, ErrUnknownNode
	}
	for _, e := range excludes {
		if _, ok := t.rng[e]; !ok {
			return nil, ErrUnknownNode
		}
	}
	segs := []Segment{base}
	for _, e := range excludes {
		hole := t.rng[e]
		segs = subtract(segs, hole)
	}
	if len(segs) == 0 {
		return segs, ErrEmptyCover
	}
	return segs, nil
}

// CoverEmpty 仅判定该覆盖集是否为空。
func (t *Tree) CoverEmpty(node string, excludes []string) (bool, error) {
	segs, err := t.Cover(node, excludes)
	if err == ErrEmptyCover {
		return true, nil
	}
	if err != nil {
		return true, err
	}
	return len(segs) == 0, nil
}

// Overlap 报告两段有序不相交半开区间集合是否有公共叶。
func Overlap(a, b []Segment) bool {
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := a[i].Lo
		if b[j].Lo > lo {
			lo = b[j].Lo
		}
		hi := a[i].Hi
		if b[j].Hi < hi {
			hi = b[j].Hi
		}
		if lo < hi {
			return true
		}
		if a[i].Hi < b[j].Hi {
			i++
		} else if a[i].Hi > b[j].Hi {
			j++
		} else {
			i++
			j++
		}
	}
	return false
}

// subtract 从有序不相交段集合中挖去半开区间 hole（仅取与各段相交部分）。
func subtract(segs []Segment, hole Segment) []Segment {
	out := make([]Segment, 0, len(segs)+1)
	for _, s := range segs {
		if hole.Hi <= s.Lo || hole.Lo >= s.Hi {
			out = append(out, s)
			continue
		}
		if hole.Lo > s.Lo {
			out = append(out, Segment{Lo: s.Lo, Hi: hole.Lo})
		}
		if hole.Hi < s.Hi {
			out = append(out, Segment{Lo: hole.Hi, Hi: s.Hi})
		}
	}
	return out
}
