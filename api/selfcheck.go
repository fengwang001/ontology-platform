package api

import (
	"errors"
	"math"
	"math/rand"

	"ontology/gram"
)

// symmetric 判定行主序 n×n 矩阵是否逐位对称。
func symmetric(g []float64, n int) bool {
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if g[i*n+j] != g[j*n+i] {
				return false
			}
		}
	}
	return true
}

// onesRHS 返回 c[j]=Σ_i G[i][j]。对称 G 满足 G·(全1)=c，
// 用它做右端可核验求解确实按完整矩阵进行（解应为全 1）。
func onesRHS(g []float64, n int) []float64 {
	c := make([]float64, n)
	for j := range c {
		for i := 0; i < n; i++ {
			c[j] += g[i*n+j]
		}
	}
	return c
}

// randVec 返回长度 m、取值在 [-scale,scale) 的确定性伪随机向量。
func randVec(m int, seed int64, scale float64) []float64 {
	r := rand.New(rand.NewSource(seed))
	v := make([]float64, m)
	for i := range v {
		v[i] = r.Float64()*2*scale - scale
	}
	return v
}

// vander 构造列满秩 m×n 矩阵：行 k 为 [1,t,t²,…]，t∈[-.5,.5) 含负、零、正。
// 供 SelfCheck 的扩展核验与同包白盒测试共用。
func vander(m, n int) []float64 {
	a := make([]float64, m*n)
	for k := 0; k < m; k++ {
		tv, v := float64(k)/float64(m)-0.5, 1.0
		for j := 0; j < n; j++ {
			a[k*n+j], v = v, v*tv
		}
	}
	return a
}

// distinctErrs 判定若干错误两两不同，用于钉住哨兵错误互不相同。
func distinctErrs(errs ...error) bool {
	for i := 0; i < len(errs); i++ {
		for j := i + 1; j < len(errs); j++ {
			if errs[i] == errs[j] {
				return false
			}
		}
	}
	return true
}

// SelfCheck 用第三节内置矩阵与右端逐条核验四条不变量，全过返回 nil。
// Gram 计数只在包内读取，不经任何导出接口暴露。
func (s *Solver) SelfCheck() error {
	// 内置 A=[[1,1],[1,2],[1,3]]，b=[2,3,5]，正确解 [1/3,3/2]。
	a := []float64{1, 1, 1, 2, 1, 3}
	b := []float64{2, 3, 5}
	t := New()
	if err := t.Factor(a, 3, 2); err != nil {
		return err
	}
	x, err := t.Solve(b, 3)
	if err != nil {
		return err
	}
	// 不变量 1：Aᵀ(Ax−b) 每项 ≤ 1e-9（残差最小）。
	if normalResidual(a, x, b, 3, 2) > 1e-9 {
		return errors.New("self-check: normal-equation residual too large")
	}
	// 不变量 2：Gram 逐位对称。
	g := gram.Gram(a, 3, 2)
	if g[1] != g[2] {
		return errors.New("self-check: Gram not symmetric")
	}
	// 不变量 3：多个不同右端不重算 Gram。
	for k := 0; k < 5; k++ {
		if _, err := t.Solve([]float64{float64(k), float64(3 * k), float64(k + 1)}, 3); err != nil {
			return err
		}
	}
	if t.gramCount.Load() != 1 {
		return errors.New("self-check: Gram recomputed across Solve")
	}
	// 不变量 4：秩亏被整体拒绝，缓存与计数不变且仍可继续使用。
	if err := t.Factor([]float64{1, 1, 2, 2, 3, 3}, 3, 2); !errors.Is(err, ErrRankDeficient) {
		return errors.New("self-check: rank deficiency not rejected")
	}
	if _, err := t.Solve(b, 3); err != nil || t.gramCount.Load() != 1 {
		return errors.New("self-check: rejected Factor left a trace")
	}
	// 大 m 档：m=10000 个不同右端后计数仍恒为 1（不随右端数增长）。
	const M, N = 10000, 3
	big := New()
	if err := big.Factor(vander(M, N), M, N); err != nil {
		return err
	}
	rhs := make([]float64, M)
	for k := 0; k < M; k++ {
		rhs[k] = float64(k%97) * 0.5
		if _, err := big.Solve(rhs, M); err != nil {
			return err
		}
	}
	if big.gramCount.Load() != 1 {
		return errors.New("self-check: Gram count not 1 at large m")
	}
	return nil
}

// normalResidual 返回 max_i |Aᵀ(Ax−b)_i|。
func normalResidual(a, x, b []float64, m, n int) float64 {
	var worst float64
	for i := 0; i < n; i++ {
		var s float64
		for k := 0; k < m; k++ {
			var ax float64
			for j := 0; j < n; j++ {
				ax += a[k*n+j] * x[j]
			}
			s += a[k*n+i] * (ax - b[k])
		}
		if v := math.Abs(s); v > worst {
			worst = v
		}
	}
	return worst
}
