package ontology

import "fmt"

// OrderError 表示向量元素下标未严格升序（出现相等或递减）。
type OrderError struct {
	// Which 指出是哪个向量：1 表示第一个参数，2 表示第二个参数。
	Which int
	// Position 是出错元素在向量中的位置（从 0 开始）。
	Position int
	// Prev / Cur 分别是前一个元素与当前元素的下标。
	Prev, Cur uint32
}

func (e *OrderError) Error() string {
	return fmt.Sprintf("ontology: 向量 %d 第 %d 个位置下标非严格升序: 前一个 %d, 当前 %d",
		e.Which, e.Position, e.Prev, e.Cur)
}

// NaNError 表示向量元素值为 NaN。
type NaNError struct {
	Which    int
	Position int
	Index    uint32
}

func (e *NaNError) Error() string {
	return fmt.Sprintf("ontology: 向量 %d 第 %d 个位置（下标 %d）的值为 NaN",
		e.Which, e.Position, e.Index)
}

// NonFiniteError 表示计算结果为非有限值（Inf 或 NaN），
// 通常由输入含正负无穷或中间结果溢出导致。
type NonFiniteError struct {
	Op    string  // "dot" 或 "cosine"
	Value float64 // 实际得到的非有限值
}

func (e *NonFiniteError) Error() string {
	return fmt.Sprintf("ontology: %s 结果为非有限值 %v", e.Op, e.Value)
}

// ZeroNormError 表示余弦相似度中某一向量的模为零，余弦无定义。
type ZeroNormError struct {
	Which int // 1: 第一个向量；2: 第二个向量；3: 两者皆为零模
}

func (e *ZeroNormError) Error() string {
	return fmt.Sprintf("ontology: 向量 %d 的模为零，余弦相似度无定义", e.Which)
}
