// Package api 是稀疏向量点积的对外入口：批量构造、点积与自检。
package api

import (
	"errors"
	"fmt"

	"ontology/sdot"
	"ontology/spv"
)

// 四类可判定哨兵错误，彼此互不相同；越界错误复用 spv.ErrIndexOutOfRange。
var (
	// ErrZeroValue：试图存入显式零值（零值语义是删除，不是存储）。
	ErrZeroValue = errors.New("api: cannot store explicit zero value")
	// ErrLenMismatch：idx 与 val 长度不一致。
	ErrLenMismatch = errors.New("api: idx and val length mismatch")
	// ErrIndexNotSorted：下标非严格递增（乱序或重复）。
	ErrIndexNotSorted = errors.New("api: indices not strictly increasing")
)

// Build 用成对的 idx/val 构造规范形稀疏向量。
// 先做整体校验：任一条目不合法即整体失败（返回 nil 与对应哨兵错误），
// 在此之前不创建向量、不产生任何状态；通过后才逐条写入。
func Build(m int, idx []int, val []float64) (*spv.Vec, error) {
	if len(idx) != len(val) {
		return nil, ErrLenMismatch
	}
	prev := -1
	for k, index := range idx {
		if index < 0 || index >= m {
			return nil, spv.ErrIndexOutOfRange
		}
		if val[k] == 0 {
			return nil, ErrZeroValue
		}
		if index <= prev { // 乱序或重复
			return nil, ErrIndexNotSorted
		}
		prev = index
	}
	v := spv.New(m) // 全部校验通过后才构造，失败天然不留痕
	for k, index := range idx {
		if err := v.Set(index, val[k]); err != nil {
			return nil, err // 理论上不可达：输入已整体校验
		}
	}
	return v, nil
}

// Dot 返回 a·b（两指针归并，只访问非零条目）。
func Dot(a, b *spv.Vec) float64 { return sdot.Dot(a, b) }

// SelfCheck 用一组内置向量核验四条不变量，全部通过返回 nil，
// 否则返回聚合错误。第 3 条的访问计数由 sdot 白盒测试钉死，
// 这里核验其可观测面：非零条目数不随 m 增长且结果与稠密参照一致。
func SelfCheck() error {
	var errs []error
	a, errA := Build(10, []int{1, 3, 5}, []float64{2, 3, 5})
	b, errB := Build(10, []int{0, 1, 5}, []float64{4, 7, 6})
	if errA != nil || errB != nil {
		return fmt.Errorf("api: selfcheck setup failed: %w", errors.Join(errA, errB))
	}
	if Dot(a, b) != dense(10, a, b) || Dot(a, b) != 44 {
		errs = append(errs, errors.New("invariant 1: dot differs from dense reference"))
	}
	if !a.Canonical() || !b.Canonical() {
		errs = append(errs, errors.New("invariant 2: canonical form violated"))
	}
	for _, m := range []int{100, 1000, 10000} {
		x, y := makePair(m)
		xi, _ := x.Snapshot()
		yi, _ := y.Snapshot()
		if len(xi) != 10 || len(yi) != 10 || Dot(x, y) != dense(m, x, y) {
			errs = append(errs, fmt.Errorf("invariant 3: nnz/dot wrong at m=%d", m))
		}
	}
	sentinels := []error{spv.ErrIndexOutOfRange, ErrZeroValue, ErrLenMismatch, ErrIndexNotSorted}
	for p := 0; p < len(sentinels); p++ {
		for q := p + 1; q < len(sentinels); q++ {
			if sentinels[p] == sentinels[q] {
				errs = append(errs, errors.New("invariant 4: sentinel errors not distinct"))
			}
		}
	}
	cases := []struct {
		m    int
		idx  []int
		val  []float64
		want error
	}{
		{10, []int{10}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{-1}, []float64{1}, spv.ErrIndexOutOfRange},
		{10, []int{1}, []float64{0}, ErrZeroValue},
		{10, []int{1, 2}, []float64{1}, ErrLenMismatch},
		{10, []int{2, 1}, []float64{1, 2}, ErrIndexNotSorted},
		{10, []int{1, 1}, []float64{1, 2}, ErrIndexNotSorted},
	}
	for _, c := range cases {
		if v, err := Build(c.m, c.idx, c.val); err != c.want || v != nil {
			errs = append(errs, fmt.Errorf("invariant 4: case %v want %v", c.idx, c.want))
		}
	}
	return errors.Join(errs...)
}

// makePair 构造两个在 m 维下各含 10 个非零条目的确定性向量。
func makePair(m int) (x, y *spv.Vec) {
	x, y = spv.New(m), spv.New(m)
	for k := 0; k < 10; k++ {
		ix := (k*7 + 3) % m
		_ = x.Set(ix, float64(k+1))
		_ = y.Set(ix, float64(10-k))
	}
	return x, y
}

// dense 是朴素稠密参照：物化成 m 维后逐项乘加，仅供自检对比。
func dense(m int, a, b *spv.Vec) float64 {
	sum := 0.0
	for k := 0; k < m; k++ {
		sum += a.Get(k) * b.Get(k)
	}
	return sum
}
