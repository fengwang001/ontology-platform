package ontology

import (
	"sync"
	"sync/atomic"
)

// Index 是一个索引结构：按声明的键生成规则把属性值映射到实例集合。
// 查询开销只随命中结果数量增长（哈希桶直查），不随实例总数增长。
type Index struct {
	ID     string
	KeyFn  KeyFunc
	mu     sync.RWMutex
	bucket map[Key]map[string]struct{}
	// visits 统计查询过程中实际访问的桶内条目数，用于证明查询复杂度。
	visits atomic.Int64
}

func NewIndex(id string, keyFn KeyFunc) *Index {
	return &Index{ID: id, KeyFn: keyFn, bucket: make(map[Key]map[string]struct{})}
}

// add 把实例加入键对应的桶。
func (ix *Index) add(key Key, instanceID string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	set, ok := ix.bucket[key]
	if !ok {
		set = make(map[string]struct{})
		ix.bucket[key] = set
	}
	set[instanceID] = struct{}{}
}

// remove 把实例从键对应的桶移除。
func (ix *Index) remove(key Key, instanceID string) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if set, ok := ix.bucket[key]; ok {
		delete(set, instanceID)
		if len(set) == 0 {
			delete(ix.bucket, key)
		}
	}
}

// lookup 返回键对应的实例集合（拷贝），并累计访问计数。
func (ix *Index) lookup(key Key) []string {
	ix.mu.RLock()
	set := ix.bucket[key]
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	ix.mu.RUnlock()
	ix.visits.Add(int64(len(set)))
	return out
}

// Visits 返回累计访问的条目数（测试用于证明 O(命中数)）。
func (ix *Index) Visits() int64 {
	return ix.visits.Load()
}

// Size 返回索引条目总数（仅用于测试与演示）。
func (ix *Index) Size() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	n := 0
	for _, set := range ix.bucket {
		n += len(set)
	}
	return n
}
