// Package api 是对外门面：行列式求值与内置自检。
package api

import (
	"errors"
	"fmt"
	"math"

	"ontology/elim"
)

// 对外暴露的三类可判定哨兵错误（与 elim 同一批）。
var (
	ErrEmpty     = elim.ErrEmpty
	ErrDimension = elim.ErrDimension
	ErrNonFinite = elim.ErrNonFinite
)

// API 是行列式服务的句柄。并发安全。
type API struct{ eng *elim.Engine }

func New() *API { return &API{eng: elim.New()} }

// Det 求 n×n 行主序矩阵 a 的行列式；输入只读。
func (x *API) Det(a []float64, n int) (float64, error) {
	return x.eng.Det(a, n)
}

// naiveDet 按第一行展开的递归定义，作为朴素参照。
func naiveDet(a []float64, n int) float64 {
	if n == 1 {
		return a[0]
	}
	var sum float64
	for j := 0; j < n; j++ {
		sub := make([]float64, 0, (n-1)*(n-1))
		for i := 1; i < n; i++ {
			for c := 0; c < n; c++ {
				if c != j {
					sub = append(sub, a[i*n+c])
				}
			}
		}
		sign := 1.0
		if j%2 == 1 {
			sign = -1
		}
		sum += sign * a[j] * naiveDet(sub, n-1)
	}
	return sum
}

// SelfCheck 对一组内置矩阵核验四条不变量，返回首个违例错误，全过返回 nil。
func (x *API) SelfCheck() error {
	cases := []struct {
		n   int
		mat []float64
	}{
		{1, []float64{-3.5}},
		{2, []float64{0, 1, 1, 0}},
		{3, []float64{0, 1, 1, 1, 0, 1, 1, 1, 0}}, // det = 2，含行交换
		{3, []float64{2, 0, 0, 0, 3, 0, 0, 0, 4}}, // 对角，无交换
		{4, []float64{1, 2, 3, 4, 0, 1, 0, 1, 5, 1, 0, 2, 1, 0, 3, 1}},
		{3, []float64{1, 2, 3, 2, 4, 6, 0, 1, 0}}, // 奇异，det = 0
	}
	for _, c := range cases {
		before := append([]float64(nil), c.mat...)
		got, err := x.Det(c.mat, c.n)
		if err != nil {
			return fmt.Errorf("selfcheck: det: %w", err)
		}
		// 不变量1：与朴素参照一致
		if want := naiveDet(before, c.n); math.Abs(got-want) > 1e-9 {
			return fmt.Errorf("selfcheck: naive mismatch: got %v want %v", got, want)
		}
		// 不变量3：输入不被修改（逐字节）
		for i := range before {
			if math.Float64bits(before[i]) != math.Float64bits(c.mat[i]) {
				return errors.New("selfcheck: input mutated")
			}
		}
	}
	// 不变量2（交换符号）由 {0,1;1,0} 用例隐式覆盖：det 必须为 -1。
	if d, _ := x.Det([]float64{0, 1, 1, 0}, 2); d != -1 {
		return fmt.Errorf("selfcheck: swap sign wrong: %v", d)
	}
	// 不变量4：三类故障可判定且互不相同，被拒后仍可正常使用。
	bads := []error{ErrEmpty, ErrDimension, ErrNonFinite}
	_, e1 := x.Det([]float64{1}, 0)
	_, e2 := x.Det([]float64{1, 2, 3}, 2)
	_, e3 := x.Det([]float64{math.NaN(), 0, 0, 1}, 2)
	for i, e := range []error{e1, e2, e3} {
		if !errors.Is(e, bads[i]) {
			return fmt.Errorf("selfcheck: fault %d not decidable: %v", i, e)
		}
	}
	if errors.Is(e1, e2) || errors.Is(e2, e3) || errors.Is(e1, e3) {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	if d, err := x.Det([]float64{0, 1, 1, 1, 0, 1, 1, 1, 0}, 3); err != nil || d != 2 {
		return fmt.Errorf("selfcheck: unusable after rejection: %v %v", d, err)
	}
	return nil
}
