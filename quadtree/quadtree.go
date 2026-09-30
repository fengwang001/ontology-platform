// Package quadtree 实现一个可自动分裂的二维点索引（点四叉树）。
//
// 根区域为 [0, side) x [0, side) 的左闭右开正方形，side 必须为 2 的幂。
// 每个格子均为左闭右开：恰落在分裂线上的点归入右侧或上侧子格。
// 当叶格内点数超过容量时四等分；边长为 1 的格不再分裂，允许超容量，
// 并计入溢出格数。查询矩形同为左闭右开，宽或高为零时返回空结果。
// 删除不触发合并。所有查询结果按点编号升序返回。
//
// 并发语义：Insert/Delete 与 Query 可并发调用；每次 Query 的结果
// 等价于某一时刻点集上的朴素扫描，绝不会观察到分裂进行到一半的状态。
package quadtree

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 可区分的拒绝原因。所有被拒绝的操作保证不改变任何格子或点。
var (
	// ErrInvalidSide 根区域边长非正或不是 2 的幂。
	ErrInvalidSide = errors.New("quadtree: side length must be a positive power of two")
	// ErrInvalidCapacity 容量非正。
	ErrInvalidCapacity = errors.New("quadtree: capacity must be positive")
	// ErrOutOfBounds 点落在根区域之外。
	ErrOutOfBounds = errors.New("quadtree: point outside root region")
	// ErrEmptyID 点编号为空。
	ErrEmptyID = errors.New("quadtree: point id is empty")
	// ErrDuplicateID 点编号重复。
	ErrDuplicateID = errors.New("quadtree: duplicate point id")
	// ErrNotFound 删除不存在的编号。
	ErrNotFound = errors.New("quadtree: point id not found")
	// ErrInvalidRect 查询矩形左端大于右端（或下端大于上端）。
	ErrInvalidRect = errors.New("quadtree: query rectangle min greater than max")
)

// Point 是一个带编号的整数坐标点。
type Point struct {
	ID string
	X  int64
	Y  int64
}

// Stats 报告一次查询的执行统计。
type Stats struct {
	// PointChecks 逐点判定次数（仅发生在与查询部分相交的叶格上）。
	PointChecks int
	// CellsVisited 实际访问（进入）的格子数；与查询不相交的格子不计入。
	CellsVisited int
}

// node 是四叉树节点。叶子节点把点存在 points 中；内部节点 children 非空。
type node struct {
	x, y   int64 // 格子左下角（含）
	size   int64 // 边长，区域为 [x, x+size) x [y, y+size)
	points []Point
	// children 下标：bit0 表示东（x 方向上半），bit1 表示北（y 方向上半）。
	// 恰落在分裂线上的点归右侧或上侧子格。
	children [4]*node
}

func (n *node) leaf() bool { return n.children[0] == nil }

// Quadtree 是并发安全的二维点索引。
type Quadtree struct {
	mu       sync.RWMutex
	root     *node
	capacity int
	index    map[string]Point // id -> point，用于按编号删除与查重
	overflow int              // 溢出格（边长 1 且点数超过容量的叶格）数量
	size     int              // 点的总数
}

// New 创建根区域为 [0, side) x [0, side)、叶格容量为 capacity 的索引。
// side 必须为正的 2 的幂，capacity 必须为正，否则整体拒绝。
func New(side int64, capacity int) (*Quadtree, error) {
	if side <= 0 || side&(side-1) != 0 {
		return nil, fmt.Errorf("%w: got side=%d", ErrInvalidSide, side)
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("%w: got capacity=%d", ErrInvalidCapacity, capacity)
	}
	return &Quadtree{
		root:     &node{x: 0, y: 0, size: side},
		capacity: capacity,
		index:    make(map[string]Point),
	}, nil
}

// Len 返回索引中点的总数。
func (t *Quadtree) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}

// OverflowCells 返回当前溢出格（边长为 1 且点数超过容量的叶格）的数量。
func (t *Quadtree) OverflowCells() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.overflow
}

// Side 返回根区域边长。
func (t *Quadtree) Side() int64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.root.size
}

// childIndex 返回点 (px, py) 相对格子中点所属的子格下标。
// 左闭右开：坐标等于分裂线时归右侧或上侧子格。
func childIndex(n *node, px, py int64) int {
	mid := n.size / 2
	idx := 0
	if px >= n.x+mid {
		idx |= 1
	}
	if py >= n.y+mid {
		idx |= 2
	}
	return idx
}

// Insert 插入一个点。编号为空、编号重复或点落在根区域外时整体拒绝，
// 索引不发生任何变化。
func (t *Quadtree) Insert(p Point) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.insertLocked(p)
}

func (t *Quadtree) insertLocked(p Point) error {
	if p.ID == "" {
		return fmt.Errorf("%w", ErrEmptyID)
	}
	if _, ok := t.index[p.ID]; ok {
		return fmt.Errorf("%w: id=%q", ErrDuplicateID, p.ID)
	}
	side := t.root.size
	if p.X < 0 || p.X >= side || p.Y < 0 || p.Y >= side {
		return fmt.Errorf("%w: id=%q at (%d,%d), root is [0,%d) x [0,%d)",
			ErrOutOfBounds, p.ID, p.X, p.Y, side, side)
	}

	n := t.root
	for !n.leaf() {
		n = n.children[childIndex(n, p.X, p.Y)]
	}
	wasOverflow := n.size == 1 && len(n.points) > t.capacity
	n.points = append(n.points, p)
	if len(n.points) > t.capacity && n.size > 1 {
		t.split(n)
	} else if !wasOverflow && n.size == 1 && len(n.points) > t.capacity {
		t.overflow++
	}
	t.index[p.ID] = p
	t.size++
	return nil
}

// split 将叶格 n 四等分并把其中的点重新分配到子格。
// 调用前需保证 len(n.points) > capacity 且 n.size > 1。
// 再分配后若边长为 1 的子格超过容量，计入溢出格数。
func (t *Quadtree) split(n *node) {
	mid := n.size / 2
	n.children[0] = &node{x: n.x, y: n.y, size: mid}             // 西南
	n.children[1] = &node{x: n.x + mid, y: n.y, size: mid}       // 东南
	n.children[2] = &node{x: n.x, y: n.y + mid, size: mid}       // 西北
	n.children[3] = &node{x: n.x + mid, y: n.y + mid, size: mid} // 东北
	pts := n.points
	n.points = nil
	for _, p := range pts {
		c := n.children[childIndex(n, p.X, p.Y)]
		c.points = append(c.points, p)
	}
	for _, c := range n.children {
		if c.size == 1 && len(c.points) > t.capacity {
			t.overflow++
		}
	}
}

// Delete 按编号删除一个点。编号不存在时整体拒绝，索引不发生任何变化。
// 删除不触发格子合并。
func (t *Quadtree) Delete(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	p, ok := t.index[id]
	if !ok {
		return fmt.Errorf("%w: id=%q", ErrNotFound, id)
	}

	n := t.root
	for !n.leaf() {
		n = n.children[childIndex(n, p.X, p.Y)]
	}
	for i, q := range n.points {
		if q.ID == id {
			n.points = append(n.points[:i], n.points[i+1:]...)
			break
		}
	}
	if n.size == 1 && len(n.points) == t.capacity {
		// 从超容量降回容量，不再是溢出格。
		t.overflow--
	}
	delete(t.index, id)
	t.size--
	return nil
}

// Query 返回落在左闭右开矩形 [x1, x2) x [y1, y2) 内、按编号升序排列的点，
// 以及本次查询的统计信息。宽或高为零时返回空结果（非错误）。
// 左端大于右端（或下端大于上端）时整体拒绝。
//
// 剪枝规则：与查询不相交的格子不访问；完全包含于查询的格子整格收集，
// 不做逐点判定；只有部分相交的叶格才逐点判定并计入 Stats.PointChecks。
func (t *Quadtree) Query(x1, y1, x2, y2 int64) ([]Point, Stats, error) {
	if x1 > x2 || y1 > y2 {
		return nil, Stats{}, fmt.Errorf("%w: [%d,%d) x [%d,%d)", ErrInvalidRect, x1, x2, y1, y2)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()

	var st Stats
	if x1 == x2 || y1 == y2 {
		return nil, st, nil
	}
	var out []Point
	t.root.query(x1, y1, x2, y2, &out, &st)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, st, nil
}

// query 递归执行范围查询。调用前保证查询矩形非空。
func (n *node) query(x1, y1, x2, y2 int64, out *[]Point, st *Stats) {
	// 不相交：不访问。
	if n.x >= x2 || n.x+n.size <= x1 || n.y >= y2 || n.y+n.size <= y1 {
		return
	}
	st.CellsVisited++
	// 完全包含：整格收集，不逐点判定。
	if n.x >= x1 && n.x+n.size <= x2 && n.y >= y1 && n.y+n.size <= y2 {
		collect(n, out)
		return
	}
	if n.leaf() {
		// 部分相交的叶格：逐点判定。
		for _, p := range n.points {
			st.PointChecks++
			if p.X >= x1 && p.X < x2 && p.Y >= y1 && p.Y < y2 {
				*out = append(*out, p)
			}
		}
		return
	}
	for _, c := range n.children {
		c.query(x1, y1, x2, y2, out, st)
	}
}

// collect 收集以 n 为根的子树中的全部点（n 完全落在查询内时调用）。
func collect(n *node, out *[]Point) {
	if n.leaf() {
		*out = append(*out, n.points...)
		return
	}
	for _, c := range n.children {
		collect(c, out)
	}
}
