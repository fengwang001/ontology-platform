package ontology

import "errors"

// Direction 决定 Top-K 按分数的哪个方向截取。
type Direction int

const (
	// Desc 保留分数最大的 K 个元素。
	Desc Direction = iota
	// Asc 保留分数最小的 K 个元素。
	Asc
)

// ErrInvalidCapacity 在容量 K 小于等于 0 时由 New 返回。
var ErrInvalidCapacity = errors.New("ontology: topk capacity must be greater than zero")

// ErrInvalidDirection 在方向参数不是 Desc/Asc 时由 New 返回。
var ErrInvalidDirection = errors.New("ontology: topk direction must be Desc or Asc")

// Element 是参与流式排名的 (ID, Score) 二元组。
type Element struct {
	ID    string
	Score float64
}

// lessResult 表示两个元素之间的复合次序比较结果。
// 这里先做类型占位，具体语义在 compare.go 中定义。
