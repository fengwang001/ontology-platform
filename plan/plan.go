// Package plan 对差分隐私查询计划做纯函数式求值：结构校验、分区集与整数微单位开销。
package plan

import "errors"

const (
	maxNodes       = 256
	maxDepth       = 8
	maxChildren    = 16
	maxLeafParts   = 16
	maxLeafCost    = 1_000_000_000
	maxSampleBound = 1_000_000
	maxCost        = 1_000_000_000_000
)

var (
	// ErrInvalidPlan：计划结构越界（节点数/深度/子数/分区数/取值范围）或开销超过 10^12。
	ErrInvalidPlan = errors.New("plan: invalid query plan")
	// ErrNotDisjoint：Par 的若干子计划分区集合相交。
	ErrNotDisjoint = errors.New("plan: parallel children are not partition-disjoint")
)

// nodes 为非导出计数器，记最近一次求值访问的节点数；恰等于计划节点总数。
var nodes int
// Node 是查询计划树节点。
type Node interface{ planNode() }

// Leaf 是叶子查询：Cost 为开销，Parts 为其访问的分区号集合。
type Leaf struct {
	Cost  int64
	Parts []int
}

// Seq 为顺序组合，开销为各子开销之和。
type Seq struct{ Children []Node }

// Par 为并行组合，要求各子分区两两不相交，开销取各子最大值。
type Par struct{ Children []Node }

// Sample 为子采样放大，开销为 ceil(子开销*Num/Den)，取整仅此一处。
type Sample struct {
	Num, Den int64
	Child    Node
}

func (Leaf) planNode()   {}
func (Seq) planNode()    {}
func (Par) planNode()    {}
func (Sample) planNode() {}

// Eval 返回计划开销与分区集。结构不合法返回 ErrInvalidPlan，
// Par 的子分区相交返回 ErrNotDisjoint。
func Eval(root Node) (int64, map[int]struct{}, error) {
	nodes = 0
	cost, parts, err := evalAt(root, 1)
	if err != nil {
		return 0, nil, err
	}
	return cost, parts, nil
}

func evalAt(n Node, depth int) (int64, map[int]struct{}, error) {
	nodes++
	if nodes > maxNodes {
		return 0, nil, ErrInvalidPlan
	}
	if depth > maxDepth || depth < 1 {
		return 0, nil, ErrInvalidPlan
	}

	switch p := n.(type) {
	case nil:
		return 0, nil, ErrInvalidPlan

	case Leaf:
		if p.Cost < 1 || p.Cost > maxLeafCost {
			return 0, nil, ErrInvalidPlan
		}
		if len(p.Parts) < 1 || len(p.Parts) > maxLeafParts {
			return 0, nil, ErrInvalidPlan
		}
		parts := make(map[int]struct{}, len(p.Parts))
		for _, part := range p.Parts {
			if part < 0 {
				return 0, nil, ErrInvalidPlan
			}
			if _, dup := parts[part]; dup {
				return 0, nil, ErrInvalidPlan
			}
			parts[part] = struct{}{}
		}
		return p.Cost, parts, nil

	case Seq:
		return evalChildren(p.Children, depth, false)

	case Par:
		return evalChildren(p.Children, depth, true)

	case Sample:
		if p.Num < 1 || p.Den < p.Num || p.Den > maxSampleBound || p.Child == nil {
			return 0, nil, ErrInvalidPlan
		}
		childCost, childParts, err := evalAt(p.Child, depth+1)
		if err != nil {
			return 0, nil, err
		}
		cost := ceilDiv(childCost*p.Num, p.Den)
		if cost > maxCost {
			return 0, nil, ErrInvalidPlan
		}
		return cost, childParts, nil

	default:
		return 0, nil, ErrInvalidPlan
	}
}

func evalChildren(children []Node, depth int, parallel bool) (int64, map[int]struct{}, error) {
	if len(children) < 1 || len(children) > maxChildren {
		return 0, nil, ErrInvalidPlan
	}

	// 第一遍：单趟求值全部子树。任何子树结构非法优先于 Par 分区相交。
	type childResult struct {
		cost  int64
		parts map[int]struct{}
	}
	results := make([]childResult, len(children))
	var cost int64
	for i, child := range children {
		childCost, childParts, err := evalAt(child, depth+1)
		if err != nil {
			return 0, nil, err
		}
		results[i] = childResult{cost: childCost, parts: childParts}
		if parallel {
			if childCost > cost {
				cost = childCost
			}
		} else {
			cost += childCost
			if cost > maxCost {
				return 0, nil, ErrInvalidPlan
			}
		}
	}

	// 第二遍：分区并集；Par 要求两两不相交。
	parts := map[int]struct{}{}
	for _, res := range results {
		for part := range res.parts {
			if parallel {
				if _, seen := parts[part]; seen {
					return 0, nil, ErrNotDisjoint
				}
			}
			parts[part] = struct{}{}
		}
	}
	if cost > maxCost {
		return 0, nil, ErrInvalidPlan
	}
	return cost, parts, nil
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }
