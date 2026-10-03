package model

import "errors"

type NodeType int

const (
	Start NodeType = iota
	Task
	AndSplit
	XorSplit
	OrSplit
	AndJoin
	OrJoin
	End
)

// Edge 是有向边 (U,V)。同一节点出边的编号由 Edges 中的加入顺序决定。
type Edge struct {
	U int
	V int
}

// Graph 描述一个流程定义，节点编号 1..N（N<=64）。
type Graph struct {
	N     int
	Kinds []NodeType // 长度 N+1，下标 1..N
	Edges []Edge
}

var (
	ErrStructure = errors.New("model: bad structure")
	ErrCycle     = errors.New("model: graph contains cycle")
	ErrReach     = errors.New("model: unreachable node")
	ErrChoice    = errors.New("model: invalid choice")
)
