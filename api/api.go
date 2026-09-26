// Package api 对外提供最小二乘：一次 Factor 缓存 AᵀA（Gram），
// 之后对任意多个右端 Solve 都复用同一份 Gram，只重算 Aᵀb 并回代。
package api

import (
	"errors"
	"sync/atomic"

	"ontology/gram"
	"ontology/lsq"
)

// 四类故障各有互不相同的哨兵错误；ErrNotFactored 是 Solve 先于 Factor 的附加错误。
var (
	ErrDimMismatch     = errors.New("dimension mismatch")
	ErrUnderdetermined = errors.New("underdetermined system: m < n")
	ErrEmpty           = errors.New("empty system: m < 1 or n < 1")
	ErrRankDeficient   = errors.New("rank deficient: Gram matrix is singular")
	ErrNotFactored     = errors.New("Factor must be called before Solve")
)

// Solver 缓存一次 Factor 的结果。gramCount 为非导出计数器，
// 只在 Factor 成功（真正计算了一次 O(m·n²) 的 Gram）时原子 +1，
// 不出现在任何公开接口里。
type Solver struct {
	a []float64 // A 的副本，供 Solve 计算 Aᵀb
	m int
	n int
	g []float64 // 缓存的 Gram

	gramCount atomic.Int64
}

// New 返回一个尚未 Factor 的空 Solver。
func New() *Solver { return &Solver{} }

// Factor 校验并缓存 A 的 Gram。所有校验在任何状态写入之前完成，
// 秩亏探测在局部副本上进行；只有最后一句成功提交才改变状态并 +1，
// 因此任何被拒绝的调用都不留痕迹。
func (s *Solver) Factor(a []float64, m, n int) error {
	if m < 1 || n < 1 {
		return ErrEmpty
	}
	if m < n {
		return ErrUnderdetermined
	}
	if len(a) != m*n {
		return ErrDimMismatch
	}
	g := gram.Gram(a, m, n) // 局部变量，尚未提交
	if lsq.IsSingular(g, n) {
		return ErrRankDeficient
	}
	ac := make([]float64, len(a))
	copy(ac, a) // 缓存副本，不持有调用方切片，也不修改输入
	s.a, s.m, s.n, s.g = ac, m, n, g
	s.gramCount.Add(1)
	return nil
}

// Solve 对右端 b 求最小二乘解：只算 Aᵀb，复用 Factor 缓存的 Gram。
// 不修改 b，不写任何状态，故 Factor 完成后可被多 goroutine 并发只读调用。
func (s *Solver) Solve(b []float64, m int) ([]float64, error) {
	if s.g == nil {
		return nil, ErrNotFactored
	}
	if m != s.m || len(b) != m {
		return nil, ErrDimMismatch
	}
	c := gram.AtB(s.a, b, m, s.n)
	return lsq.SolveG(s.g, c, s.n), nil // SolveG 在副本上消元，不动缓存
}

// gramCountForTest 返回当前 Gram 实际被计算的次数（同包白盒测试使用）。
func (s *Solver) gramCountForTest() int64 { return s.gramCount.Load() }
