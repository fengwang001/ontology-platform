package pathlock

import "hash/fnv"

// pathIndex 是按路径字节序排列锁的 treap（以路径散列为随机优先级，
// 期望 O(log n) 插入/删除/定位），用于前缀查询的排序与翻页。
// 不能用线段 trie 代替：trie 的深度优先序是「按段字典序」，
// 与完整路径的字节序不一致（如 "a-b" < "a/b"）。
type pathIndex struct {
	root     *indexNode
	compares int // 性能插桩：累计关键字比较次数，仅供测试验证复杂度
}

type indexNode struct {
	path        string
	lock        *Lock
	prio        uint64
	left, right *indexNode
}

func newPathIndex() *pathIndex { return &pathIndex{} }

func priorityOf(path string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(path))
	return h.Sum64()
}

func (ix *pathIndex) insert(path string, l *Lock) {
	ix.root = ix.insertRec(ix.root, path, l, priorityOf(path))
}

func (ix *pathIndex) insertRec(n *indexNode, path string, l *Lock, prio uint64) *indexNode {
	if n == nil {
		return &indexNode{path: path, lock: l, prio: prio}
	}
	ix.compares++
	if path < n.path {
		n.left = ix.insertRec(n.left, path, l, prio)
		if n.left.prio < n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = ix.insertRec(n.right, path, l, prio)
		if n.right.prio < n.prio {
			n = rotateLeft(n)
		}
	}
	return n
}

func (ix *pathIndex) remove(path string) {
	ix.root = ix.removeRec(ix.root, path)
}

func (ix *pathIndex) removeRec(n *indexNode, path string) *indexNode {
	if n == nil {
		return nil
	}
	ix.compares++
	switch {
	case path < n.path:
		n.left = ix.removeRec(n.left, path)
	case path > n.path:
		n.right = ix.removeRec(n.right, path)
	default:
		if n.left == nil {
			return n.right
		}
		if n.right == nil {
			return n.left
		}
		if n.left.prio < n.right.prio {
			n = rotateRight(n)
			n.right = ix.removeRec(n.right, path)
		} else {
			n = rotateLeft(n)
			n.left = ix.removeRec(n.left, path)
		}
	}
	return n
}

func rotateRight(n *indexNode) *indexNode {
	l := n.left
	n.left = l.right
	l.right = n
	return l
}

func rotateLeft(n *indexNode) *indexNode {
	r := n.right
	n.right = r.left
	r.left = n
	return r
}

// ascend 按字节序遍历所有 path > cursor 的条目，
// fn 返回 false 时提前停止。
func (ix *pathIndex) ascend(cursor string, fn func(*indexNode) bool) {
	ascendRec(ix.root, cursor, fn)
}

func ascendRec(n *indexNode, cursor string, fn func(*indexNode) bool) bool {
	if n == nil {
		return true
	}
	if n.path <= cursor {
		return ascendRec(n.right, cursor, fn)
	}
	if !ascendRec(n.left, cursor, fn) {
		return false
	}
	if !fn(n) {
		return false
	}
	return ascendRec(n.right, cursor, fn)
}
