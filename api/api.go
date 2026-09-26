// Package api 是稀疏向量点积的对外入口。
package api

import (
	"errors"

	"ontology/sdot"
	"ontology/spv"
)

// 重新导出四类哨兵错误，调用方可用 errors.Is 判定。
var (
	ErrIndexOutOfRange = spv.ErrIndexOutOfRange
	ErrZeroValue       = spv.ErrZeroValue
	ErrLenMismatch     = spv.ErrLenMismatch
	ErrNotSorted       = spv.ErrNotSorted
)

// Build 按批量条目构造规范形稀疏向量。
// 全部校验先于任何状态构造：长度不一致 → 越界 → 显式零值 → 乱序/重复，
// 任一失败整体返回，不产生任何状态。
func Build(m int, idx []int, val []float64) (*spv.Vec, error) {
	if len(idx) != len(val) {
		return nil, spv.ErrLenMismatch
	}
	for k, index := range idx {
		if index < 0 || index >= m {
			return nil, spv.ErrIndexOutOfRange
		}
		if val[k] == 0 {
			return nil, spv.ErrZeroValue
		}
		if k > 0 && idx[k-1] >= index {
			return nil, spv.ErrNotSorted
		}
	}
	v := spv.New(m)
	for k := range idx {
		if err := v.Set(idx[k], val[k]); err != nil { // 已预校验，理论上不会失败
			return nil, err
		}
	}
	return v, nil
}

// Dot 返回 a·b。
func Dot(a, b *spv.Vec) float64 { return sdot.Dot(a, b) }

// SelfCheck 用内置向量逐条核验四条不变量，全部成立返回 nil。
//
//	1 与稠密参照逐位相等；2 规范形；3 只访问非零条目（sdot.SelfCheck）；4 失败不留痕。
func SelfCheck() error {
	// 不变量 1：与朴素稠密参照逐位相等；同时核验规范形（不变量 2）。
	m := 10
	a, err := Build(m, []int{1, 3, 5}, []float64{2, 3, 5})
	if err != nil {
		return err
	}
	b, err := Build(m, []int{0, 1, 5}, []float64{4, 7, 6})
	if err != nil {
		return err
	}
	if err := canonical(a); err != nil {
		return err
	}
	if err := canonical(b); err != nil {
		return err
	}
	if got := Dot(a, b); got != 44 || got != denseDot(a, b, m) {
		return errors.New("api: dot product disagrees with dense reference")
	}
	// 不变量 4：被拒操作不留痕；拒绝后向量仍可正常使用。
	before := a.Len()
	for _, c := range []struct {
		index int
		value float64
		want  error
	}{
		{-1, 1, ErrIndexOutOfRange},
		{10, 1, ErrIndexOutOfRange},
	} {
		if err := a.Set(c.index, c.value); !errors.Is(err, c.want) {
			return errors.New("api: expected index-out-of-range rejection")
		}
	}
	if a.Len() != before || Dot(a, b) != 44 {
		return errors.New("api: rejected operation left a trace")
	}
	if err := a.Set(1, 9); err != nil || a.Get(1) != 9 { // 拒绝后仍可正常写
		return errors.New("api: vector unusable after rejection")
	}
	if err := a.Set(1, 2); err != nil { // 还原
		return err
	}
	if _, err := Build(m, []int{1, 2}, []float64{1, 0}); !errors.Is(err, ErrZeroValue) {
		return errors.New("api: expected zero-value rejection")
	}
	if _, err := Build(m, []int{1}, []float64{1, 2}); !errors.Is(err, ErrLenMismatch) {
		return errors.New("api: expected length-mismatch rejection")
	}
	if _, err := Build(m, []int{1, 1}, []float64{1, 2}); !errors.Is(err, ErrNotSorted) {
		return errors.New("api: expected not-sorted rejection")
	}
	return sdot.SelfCheck() // 不变量 3：访问计数不随 m 增长
}

// canonical 核验规范形：idx 严格递增、val 非零、等长、下标在 [0,m)。
func canonical(v *spv.Vec) error {
	idx, val := v.Snapshot()
	if len(idx) != len(val) {
		return errors.New("api: idx/val length mismatch")
	}
	for k, index := range idx {
		if index < 0 || index >= v.M() || val[k] == 0 || (k > 0 && idx[k-1] >= index) {
			return errors.New("api: canonical form violated")
		}
	}
	return nil
}

// denseDot 是朴素参照：物化成稠密数组后逐项乘加。
func denseDot(a, b *spv.Vec, m int) float64 {
	da, db := make([]float64, m), make([]float64, m)
	ia, va := a.Snapshot()
	ib, vb := b.Snapshot()
	for k := range ia {
		da[ia[k]] = va[k]
	}
	for k := range ib {
		db[ib[k]] = vb[k]
	}
	var sum float64
	for k := 0; k < m; k++ {
		sum += da[k] * db[k]
	}
	return sum
}
