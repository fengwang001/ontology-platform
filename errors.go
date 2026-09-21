package ontology

import (
	"errors"
	"fmt"
)

// ErrNoStreams 表示在至少需要一条流的运算中传入了零条流。
var ErrNoStreams = errors.New("ontology: at least one stream is required")

// NaNError 表示某条流中出现了 NaN。NaN 与任何值都不相等
// （包括它自己），不能参与集合运算。
type NaNError struct {
	Stream int // 第几条流（从 0 开始）
	Index  int // 该流中的元素下标（从 0 开始）
}

func (e NaNError) Error() string {
	return fmt.Sprintf("ontology: stream %d element %d is NaN", e.Stream, e.Index)
}

// OrderError 表示某条流不是升序：位置 Index 的元素严格小于
// 前一个元素。相等的相邻元素是允许的，不算乱序。
type OrderError struct {
	Stream int     // 第几条流（从 0 开始）
	Index  int     // 违规元素在该流中的下标（从 0 开始）
	Prev   float64 // 前一个元素
	Cur    float64 // 违规元素
}

func (e OrderError) Error() string {
	return fmt.Sprintf(
		"ontology: stream %d is not sorted: element %d (%v) is less than element %d (%v)",
		e.Stream, e.Index, e.Cur, e.Index-1, e.Prev,
	)
}
