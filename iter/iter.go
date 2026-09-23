// Package iter 提供正序迭代器，采用 fail-fast 失效约定。
package iter

import (
	"errors"

	"ontology/key"
	"ontology/list"
	"ontology/node"
)

// ErrInvalidated 表示迭代期间发生了写操作，迭代器已失效。
var ErrInvalidated = errors.New("iter: 迭代期间发生写入，迭代器已失效")

// Iter 从创建位置正序遍历。任何写操作都会使版本号变化，
// 下一次 Next 立即返回 ErrInvalidated，绝不返回半新半旧的序列。
type Iter struct {
	l    *list.List
	vers uint64
	next *node.Node
	done bool
}

// New 创建从首元素开始的迭代器。
func New(l *list.List) *Iter {
	return &Iter{l: l, vers: l.Version(), next: l.Header().Next[0]}
}

// Next 返回当前元素并前进。ok 为 false 表示遍历结束；
// 迭代期间发生写入时返回 ErrInvalidated。
func (it *Iter) Next() (k key.Key, ok bool, err error) {
	if it.done {
		return 0, false, nil
	}
	if it.l.Version() != it.vers {
		return 0, false, ErrInvalidated
	}
	if it.next == nil {
		it.done = true
		return 0, false, nil
	}
	k = it.next.Key
	it.next = it.next.Next[0]
	return k, true, nil
}
