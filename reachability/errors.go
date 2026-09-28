// Package reachability 在有向边增删过程中增量维护所有可达点对。
//
// 边带重数：同一条有向边可被重复加入，重数为正即边存在，重数归零即边删除。
// 可达定义：从节点 u 到节点 v 存在一条长度至少为 1 的有向路径时，u 可达 v。
// 自环允许，且自环可使节点达到自身。
package reachability

import "errors"

// MaxMultiplicity 是单条有向边允许的最大重数。
// 加入后会使重数超过该上限的操作会被拒绝且不产生任何副作用。
const MaxMultiplicity = 1 << 20

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidArgument 参数非法（例如批量加入的条数不是正整数）。
	ErrInvalidArgument = errors.New("reachability: invalid argument")
	// ErrEmptyNodeName 节点名为空字符串。
	ErrEmptyNodeName = errors.New("reachability: empty node name")
	// ErrEdgeNotFound 删除一条当前不存在（重数为 0）的边。
	ErrEdgeNotFound = errors.New("reachability: edge not found")
	// ErrMultiplicityOverflow 加入边会使重数超过 MaxMultiplicity。
	ErrMultiplicityOverflow = errors.New("reachability: multiplicity overflow")
)

// Pair 是一个有序点对（有向）。
type Pair struct {
	From string
	To   string
}

// Edge 是一条带重数的有向边的快照。
type Edge struct {
	From         string
	To           string
	Multiplicity int
}
