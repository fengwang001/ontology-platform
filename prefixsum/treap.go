package prefixsum

import "math"

// priority 由键确定性生成（splitmix64），
// 使树形只取决于键集合，与插入顺序无关，保证视图可复现：
// 写入改值再改回、插入后再删除，视图逐键恢复原样。
func priority(key int64) uint64 {
	z := uint64(key) + 0x9e3779b97f4a7c15
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func sumOf(n *node) int64 {
	if n == nil {
		return 0
	}
	return n.sum
}

func (n *node) pull() {
	n.sum = sumOf(n.left) + n.value + sumOf(n.right)
}

func rotateRight(n *node) *node {
	l := n.left
	n.left = l.right
	l.right = n
	n.pull()
	l.pull()
	return l
}

func rotateLeft(n *node) *node {
	r := n.right
	n.right = r.left
	r.left = n
	n.pull()
	r.pull()
	return r
}

// insert 插入新键（调用方保证键不存在）。
func insert(n *node, key, value int64, prio uint64) *node {
	if n == nil {
		return &node{key: key, value: value, sum: value, prio: prio}
	}
	if key < n.key {
		n.left = insert(n.left, key, value, prio)
		n.pull()
		if n.left.prio < n.prio {
			n = rotateRight(n)
		}
	} else {
		n.right = insert(n.right, key, value, prio)
		n.pull()
		if n.right.prio < n.prio {
			n = rotateLeft(n)
		}
	}
	return n
}

// remove 删除存在键（调用方保证键存在）。
func remove(n *node, key int64) *node {
	if key < n.key {
		n.left = remove(n.left, key)
		n.pull()
		return n
	}
	if key > n.key {
		n.right = remove(n.right, key)
		n.pull()
		return n
	}
	if n.left == nil {
		return n.right
	}
	if n.right == nil {
		return n.left
	}
	if n.left.prio < n.right.prio {
		n = rotateRight(n)
		n.right = remove(n.right, key)
	} else {
		n = rotateLeft(n)
		n.left = remove(n.left, key)
	}
	n.pull()
	return n
}

func find(n *node, key int64) *node {
	for n != nil {
		switch {
		case key < n.key:
			n = n.left
		case key > n.key:
			n = n.right
		default:
			return n
		}
	}
	return nil
}

// prefixSumLT 返回键严格小于 key 的所有值之和。
// 由不变量保证结果可表示，不会溢出。
func prefixSumLT(n *node, key int64) int64 {
	var s int64
	for n != nil {
		if key <= n.key {
			n = n.left
		} else {
			s += sumOf(n.left) + n.value
			n = n.right
		}
	}
	return s
}

// addOverflows 报告有符号加法 a+b 是否溢出。
func addOverflows(a, b int64) bool {
	if b > 0 {
		return a > math.MaxInt64-b
	}
	if b < 0 {
		return a < math.MinInt64-b
	}
	return false
}

// subOverflows 报告有符号减法 a-b 是否溢出。
func subOverflows(a, b int64) bool {
	if b > 0 {
		return a < math.MinInt64+b
	}
	if b < 0 {
		return a > math.MaxInt64+b
	}
	return false
}
