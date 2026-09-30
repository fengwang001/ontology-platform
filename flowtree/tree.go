package flowtree

import "sort"

// childrenOf 返回 parent 的子列表（parent 为 0 时是虚拟根的子）。
// 调用方须持有 a.mu。
func (a *Allocator) childrenOf(parent int) []int {
	if parent == 0 {
		return a.rootCh
	}
	if n, ok := a.nodes[parent]; ok {
		return n.children
	}
	return nil
}

// setChildrenOf 设置 parent 的子列表（parent 为 0 时写虚拟根）。
// 调用方须持有 a.mu。
func (a *Allocator) setChildrenOf(parent int, kids []int) {
	if parent == 0 {
		a.rootCh = kids
		return
	}
	a.nodes[parent].children = kids
}

// insertChild 按编号升序把 child 插入 parent 的子列表。
// 调用方须持有 a.mu。
func (a *Allocator) insertChild(parent, child int) {
	kids := a.childrenOf(parent)
	pos := sort.SearchInts(kids, child)
	kids = append(kids, 0)
	copy(kids[pos+1:], kids[pos:])
	kids[pos] = child
	a.setChildrenOf(parent, kids)
}

// removeChild 从 parent 的子列表中删除 child；不存在时不变。
// 调用方须持有 a.mu。
func (a *Allocator) removeChild(parent, child int) {
	kids := a.childrenOf(parent)
	pos := sort.SearchInts(kids, child)
	if pos == len(kids) || kids[pos] != child {
		return
	}
	a.setChildrenOf(parent, append(kids[:pos], kids[pos+1:]...))
}

// isDescendant 报告 maybe 是否为 id 的后代（不含 id 自身）。
// 调用方须持有 a.mu。
func (a *Allocator) isDescendant(id, maybe int) bool {
	for cur := maybe; cur != 0; cur = a.nodes[cur].parent {
		if cur == id {
			return true
		}
	}
	return false
}

// attach 按开流规则把已构造好的节点 n 挂到 parent 之下。
//
// 非独占：n 直接成为 parent 的子。
// 独占：n 成为 parent 的唯一子，parent 原有的全部子改挂到 n 下（权重不变）。
// 调用方须持有 a.mu，且 n 已在 a.nodes 中、parent 合法（0 或存活流）。
func (a *Allocator) attach(n *node, parent int, exclusive bool) {
	if !exclusive {
		n.parent = parent
		a.insertChild(parent, n.id)
		return
	}

	oldKids := a.childrenOf(parent)
	moved := append([]int(nil), oldKids...)
	for _, kid := range moved {
		a.nodes[kid].parent = n.id
	}
	n.children = moved
	n.parent = parent
	a.setChildrenOf(parent, []int{n.id})
}

// Open 开流。
//
// 拒绝顺序：编号非正或已用过 -> 父不存在 -> 权重越界。
// 非独占时，id 成为 parent 的子；独占时 id 成为 parent 的唯一子，
// parent 原有的全部子改挂到 id 之下（权重不变）。
func (a *Allocator) Open(id, parent, weight int, exclusive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if id <= 0 || a.used[id] {
		return ErrStreamUsed
	}
	if parent != 0 {
		if _, ok := a.nodes[parent]; !ok {
			return ErrParentMissing
		}
	}
	if weight < minWeight || weight > maxWeight {
		return ErrWeightRange
	}

	n := &node{id: id, parent: parent, weight: weight}
	a.nodes[id] = n
	a.used[id] = true
	a.attach(n, parent, exclusive)
	return nil
}

// Reroot 重设依赖：把 id 改挂到 newParent 之下并赋予新权重。
//
// 拒绝顺序：流不存在 -> 新父不存在 -> 新父是自身 -> 权重越界。
// 若 newParent 是 id 的后代，先把 newParent 连同其子树整体改挂到
// id 的原父之下（newParent 自身权重保持不变），断开后代关系后再按
// 开流规则（含独占语义）挂接 id。
func (a *Allocator) Reroot(id, newParent, newWeight int, exclusive bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	n, ok := a.nodes[id]
	if !ok {
		return ErrStreamMissing
	}
	if newParent != 0 {
		if _, ok := a.nodes[newParent]; !ok {
			return ErrNewParentMissing
		}
	}
	if newParent == id {
		return ErrParentIsSelf
	}
	if newWeight < minWeight || newWeight > maxWeight {
		return ErrWeightRange
	}

	oldParent := n.parent

	// newParent 是后代：把 newParent 子树整体上提到 id 的原父之下。
	if newParent != 0 && a.isDescendant(id, newParent) {
		pn := a.nodes[newParent]
		// 断开 id -> ... -> newParent 链：newParent 离开它当前的父。
		a.removeChild(pn.parent, newParent)
		pn.parent = oldParent
		a.insertChild(oldParent, newParent)
	}

	// 摘下 id，再按开流规则挂到新父。
	a.removeChild(oldParent, id)
	n.weight = newWeight
	n.children = nil
	a.attach(n, newParent, exclusive)
	return nil
}

// Close 关闭流。
//
// id 的每个子改挂到 id 的父；子的新权重为
// max(1, floor(weight(id) * weight(child) / Σ各子权重))，
// 其中求和只针对 id 的直接子。id 随后从树中移除，编号不可再次使用。
func (a *Allocator) Close(id int) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	n, ok := a.nodes[id]
	if !ok {
		return ErrStreamMissing
	}

	parent := n.parent
	kids := append([]int(nil), n.children...)

	sum := 0
	for _, kid := range kids {
		sum += a.nodes[kid].weight
	}

	// 先把 id 从父的子列表删除，再插入各子（子编号可能乱序，逐个插入）。
	a.removeChild(parent, id)
	for _, kid := range kids {
		ch := a.nodes[kid]
		if sum > 0 {
			nw := n.weight * ch.weight / sum
			if nw < 1 {
				nw = 1
			}
			ch.weight = nw
		}
		ch.parent = parent
		a.insertChild(parent, kid)
	}

	delete(a.nodes, id)
	return nil
}

// Parent 返回流的当前父节点（虚拟根为 0）；流不存在时第二个返回值为 false。
func (a *Allocator) Parent(id int) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.nodes[id]
	if !ok {
		return 0, false
	}
	return n.parent, true
}

// Weight 返回流的当前权重；流不存在时第二个返回值为 false。
func (a *Allocator) Weight(id int) (int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	n, ok := a.nodes[id]
	if !ok {
		return 0, false
	}
	return n.weight, true
}

// Children 返回 parent 的直接子编号快照（parent 为 0 时取虚拟根的子）。
// parent 不存在时第二个返回值为 false。
func (a *Allocator) Children(parent int) ([]int, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if parent != 0 {
		if _, ok := a.nodes[parent]; !ok {
			return nil, false
		}
	}
	return append([]int(nil), a.childrenOf(parent)...), true
}
