package plan

import "ontology/catalog"

// Kind 区分计划节点类型。
type Kind int

const (
	// Scan 是叶子：单表扫描。
	Scan Kind = iota
	// Join 是二元连接（含笛卡尔积）。
	Join
)

// Node 是计划树节点。Scan 时 Table 有效、Left/Right 为 nil；
// Join 时 Left/Right 有效，Cartesian=true 表示该步为笛卡尔积，
// Independent=true 表示该步对多个谓词采用了独立性假设。
type Node struct {
	Kind        Kind
	Table       *catalog.Table
	Left, Right *Node
	Cartesian   bool
	Independent bool

	Mask     uint64            // 该节点覆盖的表集合
	Names    []string          // 覆盖表名（按名排序）
	LeafSeq  []string          // 从左至右的叶子表名序列
	Card     float64           // 估计输出基数
	Cost     float64           // 估计累计代价
	Warnings []catalog.Warning // 子树内聚合的非致命警告
}

// IsScan 报告节点是否为扫描叶子。
func (n *Node) IsScan() bool { return n.Kind == Scan }

// Tables 返回该计划覆盖的表名（按名排序）。
func (n *Node) Tables() []string { return n.Names }

// Walk 前序遍历计划树。
func (n *Node) Walk(fn func(*Node)) {
	fn(n)
	if n.Left != nil {
		n.Left.Walk(fn)
	}
	if n.Right != nil {
		n.Right.Walk(fn)
	}
}

// CartesianSteps 统计计划中的笛卡尔积步数。
func (n *Node) CartesianSteps() int {
	count := 0
	n.Walk(func(x *Node) {
		if x.Kind == Join && x.Cartesian {
			count++
		}
	})
	return count
}

// FinalJoin 自顶向下返回根连接（用于断言笛卡尔积在最后一步）。
func (n *Node) FinalJoin() *Node {
	if n.Kind == Join {
		return n
	}
	return nil
}
