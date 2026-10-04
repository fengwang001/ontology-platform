// Package plan 描述差分隐私查询计划树并计算其隐私开销。
package plan

import "errors"

// 限制常量。
const (
	MaxNodes    = 256
	MaxDepth    = 8
	MaxChildren = 16
	MaxParts    = 16
	MaxCost     = 1_000_000_000_000
	MaxRatio    = 1_000_000
)

// 计划相关错误。
var (
	// ErrInvalidPlan 表示计划结构非法（nil、越界、参数越界等）。
	ErrInvalidPlan = errors.New("plan: invalid plan")
	// ErrNotDisjoint 表示 Par 的子节点分区集合相交。
	ErrNotDisjoint = errors.New("plan: parallel children are not disjoint")
)

// Node 是查询计划树节点。
type Node interface {
	nodeMarker()
}

// Leaf 是叶节点：开销 c，覆盖分区 parts。
type Leaf struct {
	Cost  int
	Parts []int
}

// Seq 为顺序组合，开销为各子之和。
type Seq struct {
	Children []Node
}

// Par 为并行组合，要求各子分区集合两两不相交，开销取各子最大值。
type Par struct {
	Children []Node
}

// Sample 为子采样放大，开销为 ceil(子开销*Num/Den)。
type Sample struct {
	Num   int
	Den   int
	Child Node
}

func (*Leaf) nodeMarker()   {}
func (*Seq) nodeMarker()    {}
func (*Par) nodeMarker()    {}
func (*Sample) nodeMarker() {}

// Result 是一次求值的结果。
type Result struct {
	Cost  int64
	Parts map[int]struct{}
	Nodes int
}

// Eval 校验并求值计划树。骨架占位。
//
// 一趟后序遍历同时完成结构校验、分区求并与开销计算：每个节点恰好访问一次。
// 深度按根为 1 计；节点总数不得超过 MaxNodes，深度不得超过 MaxDepth。
// 结构错误报 ErrInvalidPlan，仅 Par 子分区相交报 ErrNotDisjoint。
// 遍历不因错误提前终止，保证 Result.Nodes 等于实际访问到的节点总数；
// 当计划完全合法时，Result.Nodes 即树的节点总数。
func Eval(root Node) (Result, error) {
	r := Result{Parts: map[int]struct{}{}}
	st := &evalState{}
	cost, parts, err := st.eval(root, 1)
	r.Cost = cost
	r.Nodes = st.nodes
	for part := range parts {
		r.Parts[part] = struct{}{}
	}
	if err == nil && cost > MaxCost {
		err = ErrInvalidPlan
	}
	return r, err
}

type evalState struct {
	nodes int
}

// eval 返回 (开销, 本节点分区集合, 错误)。遍历不提前终止：每个节点计数一次。
func (s *evalState) eval(n Node, depth int) (int64, map[int]struct{}, error) {
	s.nodes++
	var firstErr error
	setErr := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}
	if n == nil {
		return 0, nil, ErrInvalidPlan
	}
	if depth > MaxDepth || s.nodes > MaxNodes {
		setErr(ErrInvalidPlan)
	}

	switch p := n.(type) {
	case *Leaf:
		if p == nil {
			return 0, nil, ErrInvalidPlan
		}
		if p.Cost < 1 || int64(p.Cost) > MaxCost || len(p.Parts) < 1 || len(p.Parts) > MaxParts {
			setErr(ErrInvalidPlan)
		}
		parts := make(map[int]struct{}, len(p.Parts))
		for _, part := range p.Parts {
			if _, dup := parts[part]; dup {
				setErr(ErrInvalidPlan)
			}
			parts[part] = struct{}{}
		}
		if p.Cost < 1 {
			return 0, parts, firstErr
		}
		return int64(p.Cost), parts, firstErr

	case *Seq:
		if p == nil || len(p.Children) < 1 || len(p.Children) > MaxChildren {
			return 0, nil, ErrInvalidPlan
		}
		var sum int64
		parts := map[int]struct{}{}
		for _, ch := range p.Children {
			c, cp, err := s.eval(ch, depth+1)
			if err != nil {
				setErr(err)
			}
			sum += c
			for part := range cp {
				parts[part] = struct{}{}
			}
		}
		return sum, parts, firstErr

	case *Par:
		if p == nil || len(p.Children) < 1 || len(p.Children) > MaxChildren {
			return 0, nil, ErrInvalidPlan
		}
		var max int64
		parts := map[int]struct{}{}
		for _, ch := range p.Children {
			c, cp, err := s.eval(ch, depth+1)
			if err != nil {
				setErr(err)
			}
			if c > max {
				max = c
			}
			for part := range cp {
				if _, overlap := parts[part]; overlap {
					setErr(ErrNotDisjoint)
				}
				parts[part] = struct{}{}
			}
		}
		return max, parts, firstErr

	case *Sample:
		if p == nil {
			return 0, nil, ErrInvalidPlan
		}
		if p.Num < 1 || p.Den < p.Num || int64(p.Den) > MaxRatio {
			setErr(ErrInvalidPlan)
		}
		c, parts, err := s.eval(p.Child, depth+1)
		if err != nil {
			setErr(err)
		}
		if p.Num < 1 || p.Den < p.Num {
			return 0, parts, firstErr
		}
		return ceilDiv(c*int64(p.Num), int64(p.Den)), parts, firstErr

	default:
		return 0, nil, ErrInvalidPlan
	}
}

func ceilDiv(x, d int64) int64 {
	return (x + d - 1) / d
}
