package bikefence

import "sort"

// box 是轴对齐包围盒。
type box struct {
	minX, minY, maxX, maxY int64
}

func bboxOf(vs []Point) box {
	b := box{minX: vs[0].X, maxX: vs[0].X, minY: vs[0].Y, maxY: vs[0].Y}
	for _, p := range vs[1:] {
		if p.X < b.minX {
			b.minX = p.X
		}
		if p.X > b.maxX {
			b.maxX = p.X
		}
		if p.Y < b.minY {
			b.minY = p.Y
		}
		if p.Y > b.maxY {
			b.maxY = p.Y
		}
	}
	return b
}

func (b box) containsPoint(p Point) bool {
	return p.X >= b.minX && p.X <= b.maxX && p.Y >= b.minY && p.Y <= b.maxY
}

func (b box) union(o box) box {
	return box{
		minX: minInt64(b.minX, o.minX),
		minY: minInt64(b.minY, o.minY),
		maxX: maxInt64(b.maxX, o.maxX),
		maxY: maxInt64(b.maxY, o.maxY),
	}

}

func (b box) area() int64 {
	// 对均匀分布数据，面积与扩展代价正相关；坐标可能很大，溢出风险由调用数据规模规避，
	// STR 打包只需相对排序，加 1 避免退化。
	return (b.maxX - b.minX + 1) * (b.maxY - b.minY + 1)
}

// rtNode 是 R 树节点；leaf 为 true 时 entries 存放叶子序号。
type rtNode struct {
	bbox    box
	leaf    bool
	entries []int
}

// RTree 是静态 STR（Sort-Tile-Recursive）打包的 R 树。
// 围栏一旦登记即不可变，采用静态打包可保证查询节点访问数稳定且可复现。
type RTree struct {
	nodes []rtNode
	root  int
	leaf  []*Fence
}

const nodeCapacity = 16

type rtItem struct {
	b   box
	ref int
}

// BuildRTree 对全部围栏做一次性 STR 打包。每次登记新围栏后重建，
// 围栏规模为运营配置量级，重建 O(n log n) 可接受，换来查询路径可证明。
func BuildRTree(fences []*Fence) *RTree {
	t := &RTree{leaf: fences}
	if len(fences) == 0 {
		t.root = -1
		return t
	}

	items := make([]rtItem, len(fences))
	for i, f := range fences {
		items[i] = rtItem{f.bbox, i}
	}

	level := t.packItems(items, true)
	for len(level) > 1 {
		next := make([]rtItem, len(level))
		for i, ni := range level {
			next[i] = rtItem{t.nodes[ni].bbox, ni}
		}
		level = t.packItems(next, false)
	}
	t.root = level[0]
	return t
}

// packItems 按 STR 算法把一组条目打包成若干新节点，返回节点序号。
func (t *RTree) packItems(items []rtItem, leafLevel bool) []int {
	sort.Slice(items, func(i, j int) bool {
		if items[i].b.minX != items[j].b.minX {
			return items[i].b.minX < items[j].b.minX
		}
		return items[i].ref < items[j].ref
	})
	// 切片数 = ceil(n / cap) 的上取整开方，使每页近似方形。
	n := len(items)
	slots := (n + nodeCapacity - 1) / nodeCapacity
	slices := 1
	for slices*slices < slots {
		slices++
	}
	sliceSize := nodeCapacity * slices

	var ids []int
	for base := 0; base < n; base += sliceSize {
		end := base + sliceSize
		if end > n {
			end = n
		}
		group := items[base:end]
		sort.SliceStable(group, func(i, j int) bool {
			if group[i].b.minY != group[j].b.minY {
				return group[i].b.minY < group[j].b.minY
			}
			return group[i].ref < group[j].ref
		})
		for lo := 0; lo < len(group); lo += nodeCapacity {
			hi := lo + nodeCapacity
			if hi > len(group) {
				hi = len(group)
			}
			node := rtNode{leaf: leafLevel}
			for k := lo; k < hi; k++ {
				node.entries = append(node.entries, group[k].ref)
				if k == lo {
					node.bbox = group[k].b
				} else {
					node.bbox = node.bbox.union(group[k].b)
				}
			}
			t.nodes = append(t.nodes, node)
			ids = append(ids, len(t.nodes)-1)
		}
	}
	return ids
}

// PointQueryStats 返回包围盒可能包含 p 的全部围栏候选与访问节点数。
// 访问节点数即“检索开销”：R 树高度 + 命中分支数，不随围栏总数线性增长。
func (t *RTree) PointQueryStats(p Point) (candidates []*Fence, nodesVisited int) {
	if t.root < 0 {
		return nil, 0
	}
	stack := []int{t.root}
	for len(stack) > 0 {
		ni := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := &t.nodes[ni]
		nodesVisited++
		if !n.bbox.containsPoint(p) {
			continue
		}
		if n.leaf {
			for _, ref := range n.entries {
				f := t.leaf[ref]
				if f.bbox.containsPoint(p) {
					candidates = append(candidates, f)
				}
			}
			continue
		}
		for _, ref := range n.entries {
			if t.nodes[ref].bbox.containsPoint(p) {
				stack = append(stack, ref)
			}
		}
	}
	return candidates, nodesVisited
}

// height 返回树高（叶子层为 1），用于复杂度证明。
func (t *RTree) height() int {
	if t.root < 0 {
		return 0
	}
	h := 1
	for ni := t.root; !t.nodes[ni].leaf; ni = t.nodes[ni].entries[0] {
		h++
	}
	return h
}
