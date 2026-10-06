package reflow

import "sort"

// compute 按节点当前属性与子节点尺寸全量计算本节点尺寸：
// 固定模式取固定值；内容模式取子节点沿主轴累加再加内边距，无子取内边距。
func (n *node) compute() {
	if n.width.Kind == ModeFixed {
		n.w = n.width.Value
	} else {
		var sum int64
		for _, c := range n.children {
			sum += c.w
		}
		n.w = 2*n.padX + sum
	}
	if n.height.Kind == ModeFixed {
		n.h = n.height.Value
	} else {
		var sum int64
		for _, c := range n.children {
			sum += c.h
		}
		n.h = 2*n.padY + sum
	}
}

// commitLocked 执行一次重排：只重算各含脏边界最小子树中的被影响节点。
// 算法：
//  1. 取当前挂在主树上的含脏边界，按 id 确定全局顺序；
//  2. 每个边界内：先重算子树内所有自身脏节点（子先于父、同父按次序），
//     再沿父链闭包传播尺寸变化，直到尺寸不再变化或到达边界即止；
//  3. 清空本边界子树内的所有脏标记；摘下子树的脏标记保留。
func (k *Kernel) commitLocked(op string) []Change {
	if !k.pending {
		k.logf("%s: 无脏标记，跳过重排", op)
		return nil
	}
	var dirtyBoundaries []*node
	if k.root.selfDirty || k.root.subDirty {
		dirtyBoundaries = append(dirtyBoundaries, k.root)
	}
	for b := range k.boundaries {
		if b == k.root {
			continue
		}
		if b.parent == nil {
			// 已摘下的边界不属于任何主树最小子树，留待重新插入。
			continue
		}
		if b.selfDirty || b.subDirty {
			dirtyBoundaries = append(dirtyBoundaries, b)
		}
	}
	sort.Slice(dirtyBoundaries, func(i, j int) bool {
		return dirtyBoundaries[i].id < dirtyBoundaries[j].id
	})

	var changes []Change
	processed := make(map[*node]bool)
	for _, b := range dirtyBoundaries {
		changes = append(changes, k.reflowBoundary(b, op, processed)...)
	}

	// 清理仍在主树上但已无任何脏标记的边界集合项。
	for b := range k.boundaries {
		if b == k.root {
			continue
		}
		if b.parent == nil {
			continue
		}
		if !b.selfDirty && !b.subDirty {
			delete(k.boundaries, b)
		}
	}
	if !k.anyAttachedDirty() {
		k.pending = false
	}
	k.logf("%s: 重排结束，共重算 %d 个节点", op, len(changes))
	return changes
}

func (k *Kernel) anyAttachedDirty() bool {
	if k.root.selfDirty || k.root.subDirty {
		return true
	}
	for b := range k.boundaries {
		if b.parent != nil && (b.selfDirty || b.subDirty) {
			return true
		}
	}
	return false
}

// reflowBoundary 重算单个含脏边界的最小子树。
func (k *Kernel) reflowBoundary(b *node, op string, processed map[*node]bool) []Change {
	var changes []Change
	// 第一趟：子树内所有自身脏节点（不含边界本身），后序重算。
	queue := k.postorderDirty(b, b)
	for len(queue) > 0 {
		forced := make(map[*node]bool)
		next := make(map[*node]bool)
		for _, n := range queue {
			if processed[n] {
				continue
			}
			old := Size{W: n.w, H: n.h}
			n.compute()
			k.stats.RecomputeVisits++
			k.logf("%s: 重算节点 %d (%d,%d)->(%d,%d)", op, n.id, old.W, old.H, n.w, n.h)
			newSize := Size{W: n.w, H: n.h}
			changes = append(changes, Change{ID: n.id, Old: old, New: newSize})
			processed[n] = true
			// 仅逐跳传播：本节点尺寸变了才需要父节点，父节点重算后
			// 若尺寸不再变化，传播链在该跳终止；到达边界即止。

			if newSize == old || n.parent == nil || n == b || n.parent == b {
				continue
			}
			if !n.parent.isBoundaryFor(k.root) {
				forced[n.parent] = true
			}
		}
		for p := range forced {
			if !processed[p] {
				next[p] = true
			}
		}
		if len(next) == 0 {
			break
		}
		queue = k.postorderFromSet(b, b, next)
	}
	// 边界固定尺寸，始终在其子节点之后重算一次（后序末尾）；
	// 尺寸变化传到边界即止，绝不向边界外扩散。
	old := Size{W: b.w, H: b.h}
	b.compute()
	k.stats.RecomputeVisits++
	k.logf("%s: 重算边界 %d (%d,%d)->(%d,%d)，变化不外散", op, b.id, old.W, old.H, b.w, b.h)
	changes = append(changes, Change{ID: b.id, Old: old, New: Size{W: b.w, H: b.h}})
	processed[b] = true
	k.clearDirtySubtree(b, b, processed)
	return changes
}

// postorderDirty 收集边界子树内（嵌套边界内部除外）所有自身脏节点，
// 以及边界本身（固定尺寸也须重算以留痕），按后序：子先父、同父按序。
func (k *Kernel) postorderDirty(n, stop *node) []*node {
	var out []*node
	var walk func(cur *node)
	walk = func(cur *node) {
		if cur != stop && cur.isBoundaryFor(k.root) {
			return
		}
		for _, c := range cur.children {
			if c.subDirty || c.selfDirty {
				walk(c)
			}
		}
		if cur != stop && cur.selfDirty {
			out = append(out, cur)
		}
	}
	if n.selfDirty || n.subDirty {
		walk(n)
	}
	return out
}

// postorderFromSet 把已含脏节点与尺寸传播强制集合合并，输出合法后序。
func (k *Kernel) postorderFromSet(n, stop *node, set map[*node]bool) []*node {
	var out []*node
	var walk func(cur *node)
	walk = func(cur *node) {
		if cur != stop && cur.isBoundaryFor(k.root) {
			return
		}
		for _, c := range cur.children {
			if set[c] || c.subDirty || c.selfDirty {
				walk(c)
			}
		}
		if set[cur] {
			out = append(out, cur)
		}
	}
	walk(n)
	return out
}

// clearDirtySubtree 清空一次重排覆盖区域内的脏标记；
// 尚未轮到自己重排的嵌套边界不能被外层清掉标记，摘下子树也不在此列。
func (k *Kernel) clearDirtySubtree(n, stop *node, processed map[*node]bool) {
	n.selfDirty = false
	n.subDirty = false
	for _, c := range n.children {
		if c.isBoundaryFor(k.root) {
			if processed[c] {
				c.selfDirty = false
				c.subDirty = false
			}
			continue
		}
		if c.subDirty || c.selfDirty {
			k.clearDirtySubtree(c, stop, processed)
		}
	}
}
