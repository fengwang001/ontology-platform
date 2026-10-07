package whiteboard

import (
	"fmt"
	"sort"
)

// orderList 维护全体元素自底到顶的全序。
// 基于隐式笛卡尔树：AddTop、Remove、Rank、Kth、MoveSegment 均为 O(log n) 期望复杂度，
// 其中 MoveSegment 对 k 个移动元素为 O(k log n)。
type orderList struct {
	t     *treap
	root  *node
	nodes map[string]*node
}

func newOrderList() *orderList {
	return &orderList{t: &treap{}, nodes: make(map[string]*node)}
}

func (o *orderList) len() int { return nodeSize(o.root) }

func (o *orderList) has(id string) bool {
	_, ok := o.nodes[id]
	return ok
}

// addTop 把新元素放到最顶，O(log n)。
func (o *orderList) addTop(id string) {
	n := &node{id: id, prio: priority(id), size: 1}
	o.nodes[id] = n
	o.root = o.t.merge(o.root, n)
	o.root.parent = nil
}

// remove 摘除一个元素，O(log n)。
func (o *orderList) remove(id string) {
	n := o.nodes[id]
	r := o.t.rank(n)
	a, bc := o.t.split(o.root, r)
	_, c := o.t.split(bc, 1)
	o.root = o.t.merge(a, c)
	if o.root != nil {
		o.root.parent = nil
	}
	delete(o.nodes, id)
}

// rank 返回元素自底起的名次（从 0 开始），O(log n)。
func (o *orderList) rank(id string) (int, bool) {
	n, ok := o.nodes[id]
	if !ok {
		return 0, false
	}
	return o.t.rank(n), true
}

// kth 返回名次为 k 的元素标识，O(log n)。
func (o *orderList) kth(k int) string {
	return o.t.kth(o.root, k).id
}

// order 返回自底向上的完整标识序列，O(n)。
func (o *orderList) order() []string {
	out := make([]string, 0, o.len())
	var stack []*node
	cur := o.root
	for cur != nil || len(stack) > 0 {
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
		cur = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		out = append(out, cur.id)
		cur = cur.right
	}
	return out
}

// sortByRank 按当前名次自底向上排序，O(k log n)。
func (o *orderList) sortByRank(ids []string) {
	type item struct {
		id string
		r  int
	}
	items := make([]item, len(ids))
	for i, id := range ids {
		items[i] = item{id, o.t.rank(o.nodes[id])}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].r < items[j].r })
	for i := range items {
		ids[i] = items[i].id
	}
}

// moveSegment 把 ids（任意顺序给出）按当前相对次序作为连续一段，
// 移动到 anchor 的上方（above=true）或下方（below=false），紧贴 anchor。
// 调用方保证：ids 与 anchor 均存在，且 anchor 不在 ids 内。O(k log n)。
func (o *orderList) moveSegment(ids []string, anchor string, above bool) {
	// 先记录每个节点的当前名次，按名次从大到小摘除，
	// 这样摘除高位节点不影响低位节点的名次。
	type item struct {
		n *node
		r int
	}
	items := make([]item, len(ids))
	for i, id := range ids {
		n := o.nodes[id]
		items[i] = item{n, o.t.rank(n)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].r > items[j].r })

	extracted := make([]*node, 0, len(ids)) // 自顶向下的摘除顺序
	for _, it := range items {
		a, bc := o.t.split(o.root, it.r)
		b, c := o.t.split(bc, 1)
		o.root = o.t.merge(a, c)
		if o.root != nil {
			o.root.parent = nil
		}
		b.parent, b.left, b.right, b.size = nil, nil, nil, 1
		extracted = append(extracted, b)
	}

	// 逆序（即自底向上）依次合并，段内相对次序与原次序一致。
	var seg *node
	for i := len(extracted) - 1; i >= 0; i-- {
		seg = o.t.merge(seg, extracted[i])
		seg.parent = nil
	}

	ar := o.t.rank(o.nodes[anchor])
	pos := ar
	if above {
		pos = ar + 1
	}
	l, r := o.t.split(o.root, pos)
	if l != nil {
		l.parent = nil
	}
	if r != nil {
		r.parent = nil
	}
	o.root = o.t.merge(o.t.merge(l, seg), r)
	o.root.parent = nil
}

// validate 校验树结构不变量（仅供测试使用），O(n)。
func (o *orderList) validate() error {
	var walk func(n, p *node) (int, error)
	walk = func(n, p *node) (int, error) {
		if n == nil {
			return 0, nil
		}
		if n.parent != p {
			return 0, fmt.Errorf("node %s: bad parent pointer", n.id)
		}
		ls, err := walk(n.left, n)
		if err != nil {
			return 0, err
		}
		rs, err := walk(n.right, n)
		if err != nil {
			return 0, err
		}
		if n.size != 1+ls+rs {
			return 0, fmt.Errorf("node %s: bad size %d, want %d", n.id, n.size, 1+ls+rs)
		}
		if n.left != nil && !higher(n, n.left) {
			return 0, fmt.Errorf("node %s: heap violation (left)", n.id)
		}
		if n.right != nil && !higher(n, n.right) {
			return 0, fmt.Errorf("node %s: heap violation (right)", n.id)
		}
		return n.size, nil
	}
	sz, err := walk(o.root, nil)
	if err != nil {
		return err
	}
	if sz != len(o.nodes) {
		return fmt.Errorf("tree size %d != node map size %d", sz, len(o.nodes))
	}
	return nil
}
