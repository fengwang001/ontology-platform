// Package pindex 谓词判定与过滤索引（Key → 命中行 ID 升序）的增删查。
package pindex

import (
	"sort"
	"sync"
)

// Hit 是谓词 P 的唯一判定入口：Score >= 50（左闭，恰好 50 命中）。
func Hit(score int) bool { return score >= 50 }

// Index 是过滤索引：只覆盖命中谓词的行。
// 自带互斥锁：Lookup 会更新非导出计数器 examined，并发只读也安全。
type Index struct {
	mu       sync.Mutex
	byKey    map[int][]int // key -> 命中行 ID，升序
	examined int           // 最近一次 Lookup 为返回结果检查过的行数（非导出，不进公开接口）
}

// New 返回空索引。
func New() *Index { return &Index{byKey: make(map[int][]int)} }

// Add 把 id 挂到 key 下，二分插入保持升序；重复插入幂等。
func (ix *Index) Add(key, id int) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := ix.byKey[key]
	pos := sort.SearchInts(ids, id)
	if pos < len(ids) && ids[pos] == id {
		return
	}
	ids = append(ids, 0)
	copy(ids[pos+1:], ids[pos:])
	ids[pos] = id
	ix.byKey[key] = ids
}

// Remove 把 id 从 key 下摘除；不存在则不动。
func (ix *Index) Remove(key, id int) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := ix.byKey[key]
	pos := sort.SearchInts(ids, id)
	if pos >= len(ids) || ids[pos] != id {
		return
	}
	ids = append(ids[:pos], ids[pos+1:]...)
	if len(ids) == 0 {
		delete(ix.byKey, key)
	} else {
		ix.byKey[key] = ids
	}
}

// Lookup 按 Key 直接定位，返回命中行 ID 升序列表；无命中返回空列表。
// 为返回结果检查的行数 = 该 Key 下的列表长度，与全表行数无关。
func (ix *Index) Lookup(key int) []int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := ix.byKey[key]
	ix.examined = len(ids)
	out := make([]int, len(ids))
	copy(out, ids)
	return out
}

// Keys 返回当前有命中行的全部 Key，升序。
func (ix *Index) Keys() []int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	keys := make([]int, 0, len(ix.byKey))
	for k := range ix.byKey {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}
