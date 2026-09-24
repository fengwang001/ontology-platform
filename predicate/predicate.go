// Package predicate 定义过滤谓词树：列比较、AND、OR、NOT 与布尔常量。
// 本包只负责结构与遍历，不负责求值与权限判定。
package predicate

// Op 标识比较类型。
type Op int

const (
	OpEq     Op = iota // column = value
	OpIsNull           // column IS NULL
)

// Kind 标识节点种类。
type Kind int

const (
	KindConst Kind = iota
	KindCompare
	KindAnd
	KindOr
	KindNot
)

// Node 是谓词树节点。
// Const 为常量节点（Value 表示真假）；Compare 为列比较；
// And/Or 使用 Children；Not 使用 Children[0]。
type Node struct {
	Kind     Kind
	Const    bool
	Column   string
	Op       Op
	Value    string
	Children []*Node
}

// 构造辅助函数，降低调用方出错概率。

// ConstNode 返回布尔常量节点。
func ConstNode(v bool) *Node { return &Node{Kind: KindConst, Const: v} }

// Eq 返回 column = value 节点。
func Eq(column, value string) *Node {
	return &Node{Kind: KindCompare, Column: column, Op: OpEq, Value: value}
}

// IsNull 返回 column IS NULL 节点。
func IsNull(column string) *Node {
	return &Node{Kind: KindCompare, Column: column, Op: OpIsNull}
}

// And 返回合取节点。
func And(children ...*Node) *Node { return &Node{Kind: KindAnd, Children: children} }

// Or 返回析取节点。
func Or(children ...*Node) *Node { return &Node{Kind: KindOr, Children: children} }

// Not 返回否定节点。
func Not(child *Node) *Node { return &Node{Kind: KindNot, Children: []*Node{child}} }

// Count 返回以 root 为根的节点总数（nil 谓词计 0）。
func Count(root *Node) int {
	if root == nil {
		return 0
	}
	n := 1
	for _, child := range root.Children {
		n += Count(child)
	}
	return n
}
