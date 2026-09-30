package index

import (
	"sort"
	"sync"
)

// Stats 是索引的累计统计；被拒绝的查询不改变任何统计。
type Stats struct {
	Scans    int64 // 成功执行的扫描次数
	Examined int64 // 扫描考察的条目总数（恰为落在推导区间内的条目数）
	Returned int64 // 通过残余过滤后返回的条目总数
}

// Index 是按列序字典序排列的多列有序索引，空值排在每列最前。
// 插入、删除与扫描可并发调用；每次扫描看到其开始时的一致快照。
type Index struct {
	mu     sync.RWMutex
	cols   []Column
	colIdx map[string]int
	keys   [][]Value // 始终按 CompareKeys 升序
	stats  Stats
}

// New 创建索引；列序即索引序。
func New(cols ...Column) *Index {
	ix := &Index{cols: append([]Column(nil), cols...), colIdx: map[string]int{}}
	for i, c := range cols {
		ix.colIdx[c.Name] = i
	}
	return ix
}

// Columns 返回索引列定义。
func (ix *Index) Columns() []Column { return append([]Column(nil), ix.cols...) }

// Stats 返回当前统计快照。
func (ix *Index) Stats() Stats {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return ix.stats
}

// Len 返回当前条目数。
func (ix *Index) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.keys)
}

func (ix *Index) checkKey(key []Value) error {
	if len(key) != len(ix.cols) {
		return ErrKeyLength
	}
	for i, v := range key {
		if v.Kind != KindNull && v.Kind != ix.cols[i].Type {
			return ErrKeyType
		}
	}
	return nil
}

// Insert 插入一条键；重复键允许并存。
func (ix *Index) Insert(key ...Value) error {
	if err := ix.checkKey(key); err != nil {
		return err
	}
	k := append([]Value(nil), key...)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	pos := sort.Search(len(ix.keys), func(i int) bool {
		return CompareKeys(ix.keys[i], k) >= 0
	})
	ix.keys = append(ix.keys, nil)
	copy(ix.keys[pos+1:], ix.keys[pos:])
	ix.keys[pos] = k
	return nil
}

// Delete 删除一条与 key 完全相等的键；返回是否删除成功。
func (ix *Index) Delete(key ...Value) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	pos := sort.Search(len(ix.keys), func(i int) bool {
		return CompareKeys(ix.keys[i], key) >= 0
	})
	if pos < len(ix.keys) && CompareKeys(ix.keys[pos], key) == 0 {
		ix.keys = append(ix.keys[:pos], ix.keys[pos+1:]...)
		return true
	}
	return false
}

// snapshot 返回扫描开始时刻的一致快照。
func (ix *Index) snapshot() [][]Value {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return append([][]Value(nil), ix.keys...)
}

// recordScan 累计一次成功扫描的统计。
func (ix *Index) recordScan(examined, returned int) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.stats.Scans++
	ix.stats.Examined += int64(examined)
	ix.stats.Returned += int64(returned)
}
