// Package ring 是「就绪键」的 FIFO 轮转环：只有有任务且当前没在执行的键
// 才在环里。入环、出环均为 O(1)，空闲（无任务）的键绝不留在环里。
// 自身不加锁，并发安全由调用方（exec）用一把互斥锁统一保证。
package ring

type node[K comparable] struct {
	key  K
	next *node[K]
}

// Ring 是去重的键环：同一个键在环上至多出现一次。
type Ring[K comparable] struct {
	in       map[K]*node[K] // 键是否在环上，并持有其节点
	head     *node[K]
	tail     *node[K]
	lookups  int // 非导出计数器：自上次 PopChecks 以来取键检查过的节点数
}

func New[K comparable]() *Ring[K] {
	return &Ring[K]{in: make(map[K]*node[K])}
}

// Add 把键加入环尾；已在环上则不重复加入。返回是否真的新加入。
func (r *Ring[K]) Add(key K) bool {
	if _, ok := r.in[key]; ok {
		return false
	}
	nd := &node[K]{key: key}
	r.in[key] = nd
	if r.tail == nil {
		r.head, r.tail = nd, nd
	} else {
		r.tail.next = nd
		r.tail = nd
	}
	return true
}

// Pop 取出环首的键并将其移出环；环空时 ok 为 false。
func (r *Ring[K]) Pop() (key K, ok bool) {
	if r.head == nil {
		r.lookups++ // 即使空环，一次取键也只检查一个位置
		return key, false
	}
	r.lookups++
	nd := r.head
	r.head = nd.next
	if r.head == nil {
		r.tail = nil
	}
	delete(r.in, nd.key)
	return nd.key, true
}

// Contains 报告键是否在环上。
func (r *Ring[K]) Contains(key K) bool { _, ok := r.in[key]; return ok }

// Len 返回环上的键数。
func (r *Ring[K]) Len() int { return len(r.in) }

// PopChecks 返回并清零自上次调用以来取键检查过的节点数（用于复杂度实测）。
func (r *Ring[K]) PopChecks() int { n := r.lookups; r.lookups = 0; return n }
