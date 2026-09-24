// Package iter 提供快照迭代器：完整反映迭代开始那一刻的内容。
package iter

import (
	"ontology/key"
	"ontology/list"
)

// Iter 是快照迭代器。创建时物化键序列，之后的写入不影响迭代结果。
type Iter struct {
	keys []key.K
	pos  int
}

// New 从序号 from（0 起，含）开始正序遍历；from 越界时收敛到空或从头。
func New(l *list.List, from int) *Iter {
	n := l.Size()
	from = min(max(from, 0), n)
	it := &Iter{keys: make([]key.K, 0, n-from)}
	for i := from; i < n; i++ {
		k, _, _ := l.At(i)
		it.keys = append(it.keys, k)
	}
	return it
}

// Next 前进一格，报告是否还有元素。
func (it *Iter) Next() bool {
	if it.pos >= len(it.keys) {
		return false
	}
	it.pos++
	return true
}

// Key 返回当前元素；仅在 Next 返回 true 后有效。
func (it *Iter) Key() key.K { return it.keys[it.pos-1] }
