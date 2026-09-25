// Package bit 实现 Fenwick 树（树状数组）：内部 1 起算，对外 API 0 起算。
package bit

import (
	"errors"
	"sync"
)

var (
	ErrBadIndex = errors.New("bit: index out of range")
	ErrBadRange = errors.New("bit: bad range")
	ErrBadSize  = errors.New("bit: negative size")
)

// BIT 是并发安全的 Fenwick 树，所有操作经互斥锁串行化。
type BIT struct {
	mu      sync.Mutex
	tree    []int // 1 起算，tree[0] 闲置，使 lowbit 恒 ≥ 1
	visited int   // 非导出计数器：单次操作访问的节点数
}

// New 创建容量为 n 的树；New(0) 合法，n < 0 返回 ErrBadSize。
func New(n int) (*BIT, error) {
	if n < 0 {
		return nil, ErrBadSize
	}
	return &BIT{tree: make([]int, n+1)}, nil
}

func lowbit(i int) int { return i & -i }

// Len 返回对外可见的下标个数。
func (b *BIT) Len() int { return len(b.tree) - 1 }

// Add 给下标 i 加 delta；i 越界返回 ErrBadIndex。
func (b *BIT) Add(i, delta int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i < 0 || i >= b.Len() {
		return ErrBadIndex
	}
	b.visited = 0
	for j := i + 1; j < len(b.tree); j += lowbit(j) {
		b.tree[j] += delta
		b.visited++
	}
	return nil
}

// PrefixSum 返回下标 0..i 的和；i == -1 表示空前缀，返回 0。
func (b *BIT) PrefixSum(i int) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if i == -1 {
		b.visited = 0
		return 0, nil
	}
	if i < -1 || i >= b.Len() {
		return 0, ErrBadIndex
	}
	sum := 0
	b.visited = 0
	for j := i + 1; j > 0; j -= lowbit(j) {
		sum += b.tree[j]
		b.visited++
	}
	return sum, nil
}

// RangeSum 返回闭区间 [l, r] 的和；l > r 返回 ErrBadRange。
func (b *BIT) RangeSum(l, r int) (int, error) {
	if l > r {
		return 0, ErrBadRange
	}
	hi, err := b.PrefixSum(r)
	if err != nil {
		return 0, err
	}
	lo, err := b.PrefixSum(l - 1)
	if err != nil {
		return 0, err
	}
	return hi - lo, nil
}

// LastVisited 返回上一次 Add/PrefixSum 访问的节点数（复杂度断言用）。
func (b *BIT) LastVisited() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.visited
}
