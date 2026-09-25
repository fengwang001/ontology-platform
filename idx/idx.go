// Package idx 定义对外 0 起算的下标类型，并统一重导出哨兵错误。
package idx

import "ontology/bit"

// 哨兵错误重导出，调用方用 errors.Is 统一区分三类错误。
var (
	ErrBadIndex = bit.ErrBadIndex
	ErrBadRange = bit.ErrBadRange
	ErrBadSize  = bit.ErrBadSize
)

// Index 是对外 0 起算的下标。
type Index int

// Check 校验 i 是否为 b 的合法下标，非法返回 ErrBadIndex。
func (i Index) Check(b *bit.BIT) error {
	if i < 0 || int(i) >= b.Len() {
		return ErrBadIndex
	}
	return nil
}

// Last 返回 b 的最后一个合法下标；空树返回 -1。
func Last(b *bit.BIT) Index { return Index(b.Len() - 1) }
