// Package seg 定义下标类型，并再导出 segtree 的哨兵错误。
package seg

import "ontology/segtree"

// Index 是线段树的下标类型。
type Index int

// Valid 报告 i 是否为长度 n 的树的合法下标。
func (i Index) Valid(n int) bool { return i >= 0 && int(i) < n }

// 哨兵错误（再导出，可用 errors.Is 区分）。
var (
	ErrBadRange    = segtree.ErrBadRange    // 区间非法总类
	ErrOutOfBounds = segtree.ErrOutOfBounds // 下标越界
	ErrReversed    = segtree.ErrReversed    // l > r
)
