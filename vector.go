// Package ontology 提供稀疏向量的点积与余弦相似度计算。
//
// 稀疏向量以按下标严格升序排列的 (index, value) 序列表示，
// 所有计算均通过一次双指针归并完成，绝不展开为稠密数组。
package ontology

// Element 是稀疏向量的一个显式元素。
// Value 允许为 0（稀疏表示允许显式存零），但不允许为 NaN。
type Element struct {
	Index uint32
	Value float64
}

// Vector 是稀疏向量，元素必须按 Index 严格升序排列。
type Vector []Element

// Stats 记录一次计算的可观测信息，每次调用独立返回，并发安全。
type Stats struct {
	// Steps 是双指针归并推进的步数（循环迭代次数）。
	Steps int
	// ZerosA / ZerosB 是两个输入向量中显式零元素的个数。
	ZerosA int
	ZerosB int
}

// ExplicitZeros 返回两个输入向量显式零元素的总数。
func (s Stats) ExplicitZeros() int { return s.ZerosA + s.ZerosB }
