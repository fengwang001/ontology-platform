// Package sparse 计算稀疏向量的点积与余弦相似度。
//
// 稀疏向量表示为按下标严格升序排列的 (index, value) 序列，
// 计算过程只做一次双指针归并，永不展开为稠密数组。
package sparse

import (
	"errors"
	"strconv"
)

// Elem 是稀疏向量的一个元素：维度下标与该维度上的值。
type Elem struct {
	Index uint32
	Value float64
}

// Vector 是按下标严格升序排列的稀疏元素切片。
// 值为 0 的显式元素合法（允许稀疏表示存零）。
// 元素的 Value 不得为 NaN 或 Inf（作为可判定错误返回）。
type Vector []Elem

// DotResult 是一次点积计算的可读统计与结果。
type DotResult struct {
	// Dot 是点积结果（Kahan 补偿求和）。
	Dot float64
	// Steps 是本次双指针归并实际推进的循环步数（个位数级，
	// 与下标跨度无关；十亿下标也不会变成十亿步）。
	Steps int
	// ExplicitZeros 是两个输入向量中显式零元素的总数。
	ExplicitZeros int
}

// 输入向量编号（1 或 2），用于错误定位。
const (
	VectorA = 1
	VectorB = 2
)

var (
	// ErrNotSorted 表示下标未严格升序（相等或递减）。
	ErrNotSorted = errors.New("sparse: indices not strictly ascending")
	// ErrNaN 表示某个元素的值为 NaN。
	ErrNaN = errors.New("sparse: NaN element value")
	// ErrInf 表示某个元素的值为正/负无穷。
	ErrInf = errors.New("sparse: infinite element value")
	// ErrZeroNorm 表示余弦相似度遇到模为零的向量（空或全零）。
	ErrZeroNorm = errors.New("sparse: cosine undefined for zero-norm vector")
	// ErrNonFiniteResult 表示点积出现 Inf 或 NaN（如 Inf 量相乘）。
	ErrNonFiniteResult = errors.New("sparse: dot product is not finite")
)

// ValidationError 携带可判定的错误类别与精确定位（哪个向量、第几个位置）。
// 位置从 0 开始计数，即 Vector 切片中的下标。
type ValidationError struct {
	Err    error // ErrNotSorted / ErrNaN / ErrInf
	Vector int   // 1 或 2
	Pos    int   // 出错元素在该向量中的位置（0 起）
	Index  uint32
}

func (e *ValidationError) Error() string {
	return e.Err.Error() +
		": vector " + strconv.Itoa(e.Vector) +
		" position " + strconv.Itoa(e.Pos) +
		" (index " + strconv.FormatUint(uint64(e.Index), 10) + ")"
}

func (e *ValidationError) Unwrap() error { return e.Err }
