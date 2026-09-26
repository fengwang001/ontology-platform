// Package dc 差分约束的登记与校验：AddConstraint、非法约束判定。不依赖其他包。
package dc

import "errors"

// 三类可判定哨兵错误，互不相同。
var (
	ErrNonPositiveN     = errors.New("dc: variable count must be positive")
	ErrVarOutOfRange    = errors.New("dc: constraint references undefined variable")
	ErrNegativeSelfLoop = errors.New("dc: negative-weight self-loop implies 0<=w<0")
)

// Constraint 一条差分约束 x_v - x_u <= w，即有向边 u->v 权 w。
type Constraint struct {
	U, V int
	W    int64
}

// Registry 已登记约束集合。校验失败的 Add 不改变集合（失败不留痕）。
type Registry struct {
	n  int
	cs []Constraint
}

// New 固定变量数 n；n 非正时整体失败。
func New(n int) (*Registry, error) {
	if n <= 0 {
		return nil, ErrNonPositiveN
	}
	return &Registry{n: n}, nil
}

// Add 先校验后追加：越界变量、自环负权（u==v 且 w<0，等价 0<=w 恒假）被拒绝且不改变集合。
func (r *Registry) Add(u, v int, w int64) error {
	if u < 0 || u >= r.n || v < 0 || v >= r.n {
		return ErrVarOutOfRange
	}
	if u == v && w < 0 {
		return ErrNegativeSelfLoop
	}
	r.cs = append(r.cs, Constraint{U: u, V: v, W: w})
	return nil
}

// N 变量个数。
func (r *Registry) N() int { return r.n }

// Count 已登记约束条数。
func (r *Registry) Count() int { return len(r.cs) }

// Constraints 返回已登记约束的副本，调用方修改不影响注册表。
func (r *Registry) Constraints() []Constraint {
	return append([]Constraint(nil), r.cs...)
}
