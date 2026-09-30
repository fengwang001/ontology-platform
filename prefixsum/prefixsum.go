// Package prefixsum 提供有序键上的增量前缀和视图。
//
// 前缀和定义：对任意存在键 k，其前缀和为所有存在且不大于 k 的键的值之和。
// 前缀和只对存在键有定义；查询不存在键返回 ErrKeyNotFound。
package prefixsum

import (
	"errors"
	"fmt"
	"sync"
)

// 错误类别，互不相同、可区分。
var (
	// ErrInvalidArgument 非法参数（如 nil 接收者、构造参数不合法）。
	ErrInvalidArgument = errors.New("prefixsum: invalid argument")
	// ErrKeyOutOfRange 键越界（不在 [minKey, maxKey] 内）。
	ErrKeyOutOfRange = errors.New("prefixsum: key out of range")
	// ErrKeyNotFound 键不存在。
	ErrKeyNotFound = errors.New("prefixsum: key not found")
	// ErrTooManyKeys 键数超限。
	ErrTooManyKeys = errors.New("prefixsum: too many keys")
	// ErrOverflow 和值溢出。
	ErrOverflow = errors.New("prefixsum: sum overflow")
)

// Entry 是视图中一个存在键的快照项。
type Entry struct {
	Key       int64
	Value     int64
	PrefixSum int64
}

// node 是 treap 节点，按 key 有序、按 prio 堆序，sum 维护子树值和。
type node struct {
	key   int64
	value int64
	sum   int64
	prio  uint64
	left  *node
	right *node
}

// View 是有序键上的增量前缀和视图，可并发使用。
type View struct {
	mu      sync.RWMutex
	root    *node
	size    int
	minKey  int64
	maxKey  int64
	maxKeys int
}

// NewView 创建视图。minKey > maxKey 或 maxKeys <= 0 返回 ErrInvalidArgument。
func NewView(minKey, maxKey int64, maxKeys int) (*View, error) {
	if minKey > maxKey || maxKeys <= 0 {
		return nil, ErrInvalidArgument
	}
	return &View{minKey: minKey, maxKey: maxKey, maxKeys: maxKeys}, nil
}

// Put 插入或改值，返回操作后前缀和发生变化的键个数（新插入键一律计一）。
// 任何校验失败都不改变状态。
func (v *View) Put(key, value int64) (int, error) {
	if v == nil {
		return 0, ErrInvalidArgument
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if key < v.minKey || key > v.maxKey {
		return 0, ErrKeyOutOfRange
	}
	cur := find(v.root, key)
	if cur == nil {
		if v.size >= v.maxKeys {
			return 0, ErrTooManyKeys
		}
		// 新键自身的前缀和也不可溢出。
		if addOverflows(prefixSumLT(v.root, key), value) {
			return 0, ErrOverflow
		}
		affected := 1 // 新插入键一律计一
		if value != 0 {
			// 键大于 key 的存在键前缀和均加 value，逐一校验不溢出。
			n, ok := v.scanFrom(key, false, func(p int64) bool {
				return !addOverflows(p, value)
			})
			if !ok {
				return 0, ErrOverflow
			}
			affected += n
		}
		v.root = insert(v.root, key, value, priority(key))
		v.size++
		return affected, nil
	}
	old := cur.value
	if old == value {
		return 0, nil // 值未变，无任何前缀和变化
	}
	// 键 >= key 的存在键前缀和由 p 变为 (p - old) + value，逐一校验不溢出。
	n, ok := v.scanFrom(key, true, func(p int64) bool {
		if subOverflows(p, old) {
			return false
		}
		return !addOverflows(p-old, value)
	})
	if !ok {
		return 0, ErrOverflow
	}
	v.setValue(key, value)
	return n, nil
}

// Delete 删除存在键，返回受影响键数（被删键不计）。
// 任何校验失败都不改变状态。
func (v *View) Delete(key int64) (int, error) {
	if v == nil {
		return 0, ErrInvalidArgument
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if key < v.minKey || key > v.maxKey {
		return 0, ErrKeyOutOfRange
	}
	cur := find(v.root, key)
	if cur == nil {
		return 0, ErrKeyNotFound
	}
	old := cur.value
	affected := 0
	if old != 0 {
		// 键大于 key 的存在键前缀和均减 old，逐一校验不溢出。
		n, ok := v.scanFrom(key, false, func(p int64) bool {
			return !subOverflows(p, old)
		})
		if !ok {
			return 0, ErrOverflow
		}
		affected = n
	}
	v.root = remove(v.root, key)
	v.size--
	return affected, nil
}

// PrefixSum 返回存在键 key 的前缀和；键不存在返回 ErrKeyNotFound。
func (v *View) PrefixSum(key int64) (int64, error) {
	if v == nil {
		return 0, ErrInvalidArgument
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if key < v.minKey || key > v.maxKey {
		return 0, ErrKeyOutOfRange
	}
	cur := find(v.root, key)
	if cur == nil {
		return 0, ErrKeyNotFound
	}
	return prefixSumLT(v.root, key) + cur.value, nil
}

// Entries 返回按键升序的视图快照。
func (v *View) Entries() []Entry {
	if v == nil {
		return nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	entries := make([]Entry, 0, v.size)
	var run int64
	inOrder(v.root, func(n *node) {
		run += n.value
		entries = append(entries, Entry{Key: n.key, Value: n.value, PrefixSum: run})
	})
	return entries
}

// Check 自检：校验树结构不变量（键序、堆序、子树和、键数），
// 并将增量维护的前缀和与朴素重算结果逐键比对。
func (v *View) Check() error {
	if v == nil {
		return ErrInvalidArgument
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	count := 0
	var run int64
	var prev int64
	var check func(n *node, min, max int64) error
	check = func(n *node, min, max int64) error {
		if n == nil {
			return nil
		}
		if n.key < min || n.key > max {
			return fmt.Errorf("prefixsum: check: key %d violates search order", n.key)
		}
		if n.left != nil && n.left.prio < n.prio {
			return fmt.Errorf("prefixsum: check: key %d violates heap order", n.key)
		}
		if n.right != nil && n.right.prio < n.prio {
			return fmt.Errorf("prefixsum: check: key %d violates heap order", n.key)
		}
		if n.sum != sumOf(n.left)+n.value+sumOf(n.right) {
			return fmt.Errorf("prefixsum: check: key %d has stale subtree sum", n.key)
		}
		if err := check(n.left, min, n.key-1); err != nil {
			return err
		}
		// 中序位置：朴素重算前缀和并与增量结果比对。
		run += n.value
		if inc := prefixSumLT(v.root, n.key) + n.value; inc != run {
			return fmt.Errorf("prefixsum: check: key %d prefix sum %d != naive %d", n.key, inc, run)
		}
		if count > 0 && n.key <= prev {
			return fmt.Errorf("prefixsum: check: key %d out of order after %d", n.key, prev)
		}
		prev = n.key
		count++
		return check(n.right, n.key+1, max)
	}
	if err := check(v.root, v.minKey, v.maxKey); err != nil {
		return err
	}
	if count != v.size {
		return fmt.Errorf("prefixsum: check: counted %d keys, recorded %d", count, v.size)
	}
	return nil
}

// Len 返回当前存在键个数。
func (v *View) Len() int {
	if v == nil {
		return 0
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.size
}

// setValue 就地改写存在键的值并修复路径上的子树和（调用方保证键存在且持写锁）。
func (v *View) setValue(key, value int64) {
	var path []*node
	cur := v.root
	for cur.key != key {
		path = append(path, cur)
		if key < cur.key {
			cur = cur.left
		} else {
			cur = cur.right
		}
	}
	cur.value = value
	cur.pull()
	for i := len(path) - 1; i >= 0; i-- {
		path[i].pull()
	}
}

// scanFrom 按键升序扫描键 >= threshold（inclusive 为 false 时严格大于）的存在键，
// 对每个键的当前前缀和调用 check；check 返回 false 时立即终止并报告失败。
// 返回扫过的键数与是否全部通过校验。调用方需持锁。
func (v *View) scanFrom(threshold int64, inclusive bool, check func(prefixSum int64) bool) (int, bool) {
	run := prefixSumLT(v.root, threshold)
	count := 0
	var stack []*node
	cur := v.root
	for cur != nil || len(stack) > 0 {
		for cur != nil {
			stack = append(stack, cur)
			cur = cur.left
		}
		cur = stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur.key > threshold || (inclusive && cur.key == threshold) {
			run += cur.value
			count++
			if !check(run) {
				return count, false
			}
		}
		cur = cur.right
	}
	return count, true
}

func inOrder(n *node, fn func(*node)) {
	if n == nil {
		return
	}
	inOrder(n.left, fn)
	fn(n)
	inOrder(n.right, fn)
}
