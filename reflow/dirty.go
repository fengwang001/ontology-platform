package reflow

// isBoundaryFor 判定布局边界：根始终是边界；其余节点须同时满足
// 声明布局隔离、宽度固定、高度固定三个条件，缺一即穿越。
func (n *node) isBoundaryFor(root *node) bool {
	if n == root {
		return true
	}
	return n.isolated && n.width.Kind == ModeFixed && n.height.Kind == ModeFixed
}

// markSelf 把节点标为自身脏+子树含脏，然后向上传播直到（含）最近边界。
func (k *Kernel) markSelf(n *node, op string) {
	if n.selfDirty && n.subDirty {
		return
	}
	n.selfDirty = true
	n.subDirty = true
	k.pending = true
	b := n.isBoundaryFor(k.root)
	k.logf("%s: 节点 %d 自身脏；判定边界=%v", op, n.id, b)
	if b {
		if n != k.root {
			k.boundaries[n] = struct{}{}
		}
		return
	}
	k.propagateSubDirty(n.parent, op)
}

// propagateSubDirty 从 from 开始向上把子树含脏标记铺到（含）最近边界，
// 边界本身被标记但不再上传；开销只与到最近边界的深度有关。
func (k *Kernel) propagateSubDirty(from *node, op string) {
	for a := from; a != nil; a = a.parent {
		k.stats.MarkPropSteps++
		a.subDirty = true
		k.pending = true
		boundary := a.isBoundaryFor(k.root)
		k.logf("%s: 祖先 %d 子树含脏（边界=%v）", op, a.id, boundary)
		if boundary {
			if a != k.root {
				k.boundaries[a] = struct{}{}
			}
			return
		}
	}
}

// markStructureChange 处理插入/移除/移动：直接承载点自身脏；
// 由于“子节点列表本身变了”，即使该承载点重算后尺寸恰好不变，
// 其祖先也必须基于新结构重算，因此祖先链到边界一律强制重算。
func (k *Kernel) markStructureChange(n *node, op string) {
	k.markSelf(n, op)
	for p := n.parent; p != nil; p = p.parent {
		k.stats.MarkPropSteps++
		p.selfDirty = true
		p.subDirty = true
		k.pending = true
		boundary := p.isBoundaryFor(k.root)
		k.logf("%s: 结构变动，祖先 %d 强制重算（边界=%v）", op, p.id, boundary)
		if boundary {
			if p != k.root {
				k.boundaries[p] = struct{}{}
			}
			return
		}
	}
}
