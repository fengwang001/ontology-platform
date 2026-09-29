package ontology

// node 是四叉树节点。leaf 为 true 时 points 有效，否则 children[4] 有效。
// 子格顺序：0=左下(x<mx,y<my) 1=右下 2=左上 3=右上。
type node struct {
	leaf     bool
	depth    int
	children [4]*node
	points   []Point
}

func newLeaf(depth int) *node {
	return &node{leaf: true, depth: depth, points: make([]Point, 0)}
}

// childIndex 返回点 (x,y) 在四等分线 (mx,my) 下所属子格。
// 恰落在分裂线上归右侧/上侧：x>=mx 取右，y>=my 取上。
func childIndex(x, y, mx, my int64) int {
	idx := 0
	if x >= mx {
		idx |= 1
	}
	if y >= my {
		idx |= 2
	}
	return idx
}

// insert 把点放入以 (ox,oy) 为左下角、side 为边长的格。
// 调用方保证点位于该格内。叶子格超容量时递归四等分。
func (n *node) insert(p Point, ox, oy, side int64, capacity int) {
	if n.leaf {
		n.points = append(n.points, p)
		n.splitIfNeeded(ox, oy, side, capacity)
		return
	}
	mx, my := ox+side/2, oy+side/2
	idx := childIndex(p.X, p.Y, mx, my)
	cx, cy, childSide := childBounds(ox, oy, side/2, idx)
	n.children[idx].insert(p, cx, cy, childSide, capacity)
}

// splitIfNeeded 在格内点数超过容量且边长大于 1 时四等分，并把点下沉；
// 边长为 1 的格不再分裂（允许超容量）。分裂后对子格重复该处理。
func (n *node) splitIfNeeded(ox, oy, side int64, capacity int) {
	for n.leaf && side > 1 && len(n.points) > capacity {
		childSide := side / 2
		mx, my := ox+childSide, oy+childSide
		for i := range n.children {
			n.children[i] = newLeaf(n.depth + 1)
		}
		for _, p := range n.points {
			n.children[childIndex(p.X, p.Y, mx, my)].points =
				append(n.children[childIndex(p.X, p.Y, mx, my)].points, p)
		}
		n.points = nil
		n.leaf = false
	}
	if !n.leaf {
		for i, child := range n.children {
			cx, cy, _ := childBounds(ox, oy, side/2, i)
			child.splitIfNeeded(cx, cy, side/2, capacity)
		}
	}
}

// childBounds 返回第 idx 个子格的左下角与边长。
func childBounds(ox, oy, childSide int64, idx int) (int64, int64, int64) {
	cx, cy := ox, oy
	if idx&1 != 0 {
		cx += childSide
	}
	if idx&2 != 0 {
		cy += childSide
	}
	return cx, cy, childSide
}

// remove 从格中删除指定编号的点，返回是否找到。
func (n *node) remove(id string, x, y, ox, oy, side int64) bool {
	if n.leaf {
		for i, p := range n.points {
			if p.ID == id {
				n.points = append(n.points[:i], n.points[i+1:]...)
				return true
			}
		}
		return false
	}
	mx, my := ox+side/2, oy+side/2
	idx := childIndex(x, y, mx, my)
	cx, cy, childSide := childBounds(ox, oy, side/2, idx)
	return n.children[idx].remove(id, x, y, cx, cy, childSide)
}

// collect 收集落在左闭右开查询矩形 q 内的点编号。
// 完全包含于 q 的格整格收录（不逐点判定）；不相交的格立即剪枝；
// 部分相交的叶子格才逐点判定。所有访问量计入 stats。
func (n *node) collect(ox, oy, side int64, q Rect, stats *QueryStats, out *[]string) {
	stats.BoundTestedNodes++
	cell := Rect{ox, oy, ox + side, oy + side}
	if !rectsIntersect(cell, q) {
		stats.PrunedNodes++
		return
	}
	if rectContains(q, cell) {
		n.collectAll(stats, out)
		return
	}
	if n.leaf {
		for _, p := range n.points {
			stats.PointChecks++
			if q.X0 <= p.X && p.X < q.X1 && q.Y0 <= p.Y && p.Y < q.Y1 {
				*out = append(*out, p.ID)
			}
		}
		return
	}
	for i, child := range n.children {
		cx, cy, childSide := childBounds(ox, oy, side/2, i)
		child.collect(cx, cy, childSide, q, stats, out)
	}
}

// collectAll 整格收录：叶子格计入 FullyContainedLeaves 且不逐点判定。
func (n *node) collectAll(stats *QueryStats, out *[]string) {
	if n.leaf {
		stats.FullyContainedLeaves++
		for _, p := range n.points {
			*out = append(*out, p.ID)
		}
		return
	}
	for _, child := range n.children {
		child.collectAll(stats, out)
	}
}

func rectsIntersect(a, b Rect) bool {
	return a.X0 < b.X1 && b.X0 < a.X1 && a.Y0 < b.Y1 && b.Y0 < a.Y1
}

// rectContains 报告 outer 是否完整包含 inner（均为左闭右开）。
func rectContains(outer, inner Rect) bool {
	return outer.X0 <= inner.X0 && inner.X1 <= outer.X1 &&
		outer.Y0 <= inner.Y0 && inner.Y1 <= outer.Y1
}

func (n *node) countNodes() (nodes, leaves, maxDepth int) {
	if n.leaf {
		return 1, 1, n.depth
	}
	nodes, maxDepth = 1, n.depth
	for _, child := range n.children {
		cn, cl, cd := child.countNodes()
		nodes += cn
		leaves += cl
		if cd > maxDepth {
			maxDepth = cd
		}
	}
	return nodes, leaves, maxDepth
}

// countOverflow 返回边长为 side 的各叶子格中，点数超过 capacity 的格数。
func (n *node) countOverflow(side int64, capacity int) int {
	if n.leaf {
		if side == 1 && len(n.points) > capacity {
			return 1
		}
		return 0
	}
	total := 0
	for _, child := range n.children {
		total += child.countOverflow(side/2, capacity)
	}
	return total
}
