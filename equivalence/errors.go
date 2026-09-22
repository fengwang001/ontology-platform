// Package equivalence 提供字符串元素的等价类合并器（并查集）。
//
// 每个等价类的代表元恒为该类中字典序最小的 ID，且与合并顺序无关。
// 实现包含按秩合并与全路径压缩，可被多协程并发使用。
package equivalence

import "errors"

// ErrUnknownElement 表示操作引用了一个从未被创建的元素。
//
// Find 与 Connected 对未知元素返回该错误；Union 则会隐式创建未知元素。
var ErrUnknownElement = errors.New("equivalence: unknown element")

// IsUnknown reports whether err 表示「未知元素」错误。
func IsUnknown(err error) bool { return errors.Is(err, ErrUnknownElement) }
