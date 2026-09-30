package extent

import "sync"

// allocator 管理固定容量的物理空间，按字节记录引用计数，
// 并保存物理字节的实际内容。分配总是取能容纳整个区间的最低地址。
type allocator struct {
	mu       sync.Mutex
	data     []byte   // 物理字节内容
	refs     []uint32 // 每个物理字节的引用计数
	capacity int64
	used     int64 // 引用计数非零的物理字节数
}

func newAllocator(capacity int64) *allocator {
	return &allocator{
		data:     make([]byte, capacity),
		refs:     make([]uint32, capacity),
		capacity: capacity,
	}
}

// allocWrite 分配 len(data) 个连续物理字节并写入数据，返回起始地址。
// 取能容纳的最低地址；空间不足返回 -1，且不改变任何状态。
func (a *allocator) allocWrite(data []byte) int64 {
	n := int64(len(data))
	a.mu.Lock()
	defer a.mu.Unlock()
	var run int64
	for i := int64(0); i < a.capacity; i++ {
		if a.refs[i] == 0 {
			run++
			if run == n {
				start := i - n + 1
				for j := start; j <= i; j++ {
					a.refs[j] = 1
				}
				copy(a.data[start:start+n], data)
				a.used += n
				return start
			}
		} else {
			run = 0
		}
	}
	return -1
}

// share 将 [start, start+n) 的引用计数各加一（克隆共享）。
func (a *allocator) share(start, n int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := start; i < start+n; i++ {
		a.refs[i]++
	}
}

// release 将 [start, start+n) 的引用计数各减一，归零即回收。
func (a *allocator) release(start, n int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := start; i < start+n; i++ {
		a.refs[i]--
		if a.refs[i] == 0 {
			a.used--
			a.data[i] = 0
		}
	}
}

// usedLocked 返回引用计数非零的物理字节数。
func (a *allocator) usedLocked() int64 {
	return a.used
}
