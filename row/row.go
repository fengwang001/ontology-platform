// Package row 定义分页数据行及其复合排序键 (Score, ID) 上的严格全序。
package row

import (
	"errors"
	"math"
)

// ErrInvalid 表示行不合法：NaN 排序值或空 ID。
var ErrInvalid = errors.New("row: invalid row")

// Row 是数据集的一行：一个 float64 排序值加一个唯一字符串 ID。
type Row struct {
	Score float64
	ID    string
}

// New 校验并构造一行。NaN 不可作为排序值，ID 必须非空。
func New(score float64, id string) (Row, error) {
	if math.IsNaN(score) || id == "" {
		return Row{}, ErrInvalid
	}
	return Row{Score: score, ID: id}, nil
}

// Valid 报告行是否可写入数据集。
func (r Row) Valid() bool {
	return !math.IsNaN(r.Score) && r.ID != ""
}

// orderBits 把 float64 映射为保持数值全序的无符号整数：
// 正数翻转符号位，负数整体取反，使位序与 <-1<0<1< 一致。
func orderBits(f float64) uint64 {
	b := math.Float64bits(f)
	if b&(1<<63) != 0 {
		return ^b
	}
	return b | (1 << 63)
}

// Compare 按复合键 (Score, ID) 严格全序比较：先比 Score，相等再按字节序比 ID。
// 返回 -1 / 0 / 1。调用方须保证两行均 Valid。
func Compare(a, b Row) int {
	ka, kb := orderBits(a.Score), orderBits(b.Score)
	switch {
	case ka < kb:
		return -1
	case ka > kb:
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	}
	return 0
}

// Less 报告 a 的复合键是否严格小于 b。
func Less(a, b Row) bool { return Compare(a, b) < 0 }

// After 报告 r 的复合键是否严格晚于边界 bound（正向翻页用）。
func After(r, bound Row) bool { return Compare(r, bound) > 0 }

// Before 报告 r 的复合键是否严格早于边界 bound（反向翻页用）。
func Before(r, bound Row) bool { return Compare(r, bound) < 0 }
