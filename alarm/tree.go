package alarm

import "math/rand/v2"

// 活动列表以平衡二叉树（Treap）维护，保证插入 / 删除 / 全量遍历的
// 开销只与当前活动报警条数相关，与历史事件总数无关。
//
// 排序键（严格升序，较小者排在列表前面）：
//  1. 优先级：紧急 > 高 > 低
//  2. 确认层级：未确认（激活未确认 / 返回未确认）先于已确认
//  3. 最近一次进入激活的时刻：较早者在前
//  4. 编号：较小者在前

type treeNode struct {
	p        *activePoint
	priority uint64
	left     *treeNode
	right    *treeNode
}

type activeTree struct {
	root *treeNode
}

// less 定义活动列表顺序：返回 true 表示 a 应排在 b 之前。
func treeLess(a, b *activePoint) bool {
	if a.priority != b.priority {
		return a.priority > b.priority
	}
	au := unackedTier(a.state)
	bu := unackedTier(b.state)
	if au != bu {
		return au // 未确认(true)在前
	}
	if a.lastActiveAt != b.lastActiveAt {
		return a.lastActiveAt < b.lastActiveAt
	}
	return a.id < b.id
}

func unackedTier(s State) bool {
	return s == StateActiveUnacked || s == StateReturnUnacked
}

func (t *activeTree) insert(p *activePoint) {
	t.root = treapInsert(t.root, &treeNode{p: p, priority: rand.Uint64()})
}

func (t *activeTree) erase(p *activePoint) {
	t.root = treapEraseByID(t.root, p)
}

// inorder 按排序列出全部活动报警；结果长度等于树中节点数。
func (t *activeTree) inorder(buf []*activePoint) []*activePoint {
	buf = buf[:0]
	var walk func(n *treeNode)
	walk = func(n *treeNode) {
		if n == nil {
			return
		}
		walk(n.left)
		buf = append(buf, n.p)
		walk(n.right)
	}
	walk(t.root)
	return buf
}

func treapInsert(root, n *treeNode) *treeNode {
	if root == nil {
		return n
	}
	if treeLess(n.p, root.p) {
		root.left = treapInsert(root.left, n)
		if root.left.priority > root.priority {
			root = rotateRight(root)
		}
	} else {
		root.right = treapInsert(root.right, n)
		if root.right.priority > root.priority {
			root = rotateLeft(root)
		}
	}
	return root
}

// treapEraseByID 按“节点身份就是该点”删除，而不是依赖当前排序键：
// 点的确认层级/最近激活时刻可能在其停留于树中期间发生变化，
// 用键比较会因顺序变化而找不到旧节点（曾导致树中残留重复节点）。
func treapEraseByID(root *treeNode, p *activePoint) *treeNode {
	if root == nil {
		return nil
	}
	if root.p == p {
		return treapMerge(root.left, root.right)
	}
	root.left = treapEraseByID(root.left, p)
	root.right = treapEraseByID(root.right, p)
	return root
}

// treapMerge 合并两棵子树（a 中全部键小于 b 中全部键）。
// 因为被删除节点的左右子树仍满足该性质，合并只需按堆优先级旋转，
// 不再依赖被删点当前的键值，天然不会成环。
func treapMerge(a, b *treeNode) *treeNode {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	if a.priority > b.priority {
		a.right = treapMerge(a.right, b)
		return a
	}
	b.left = treapMerge(a, b.left)
	return b
}

func rotateRight(n *treeNode) *treeNode {
	x := n.left
	n.left = x.right
	x.right = n
	return x
}

func rotateLeft(n *treeNode) *treeNode {
	x := n.right
	n.right = x.left
	x.left = n
	return x
}
