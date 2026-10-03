package keyindex

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// Version 描述某个键的一个版本。Deleted 为 true 时是删除标记。
type Version struct {
	Number  int
	Deleted bool
}

// Index 是有序键与版本索引，可被并发访问。
type Index struct {
	mu    sync.RWMutex
	keys  []string
	data  map[string]map[int]bool
	seeks int64
}

// New 创建空索引。
func New() *Index {
	return &Index{data: map[string]map[int]bool{}}
}

// Put 装载或写入一个版本。
func (ix *Index) Put(key string, version int, deleted bool) {
	if len(key) == 0 {
		panic("keyindex: empty key")
	}
	if version <= 0 {
		panic(fmt.Sprintf("keyindex: non-positive version %d", version))
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	vers, ok := ix.data[key]
	if !ok {
		vers = map[int]bool{}
		ix.data[key] = vers
		i := sort.SearchStrings(ix.keys, key)
		ix.keys = append(ix.keys, "")
		copy(ix.keys[i+1:], ix.keys[i:])
		ix.keys[i] = key
	}
	vers[version] = deleted
}

// DeleteVersion 删除单个版本（并发写入测试用）。
func (ix *Index) DeleteVersion(key string, version int) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	vers, ok := ix.data[key]
	if !ok {
		return
	}
	delete(vers, version)
	if len(vers) == 0 {
		delete(ix.data, key)
		i := sort.SearchStrings(ix.keys, key)
		if i < len(ix.keys) && ix.keys[i] == key {
			ix.keys = append(ix.keys[:i], ix.keys[i+1:]...)
		}
	}
}

// Snapshot 取得当前时刻的不可变快照。
func (ix *Index) Snapshot() *Snapshot {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	keys := append([]string(nil), ix.keys...)
	data := make(map[string]map[int]bool, len(ix.data))
	for k, vers := range ix.data {
		vm := make(map[int]bool, len(vers))
		for n, deleted := range vers {
			vm[n] = deleted
		}
		data[k] = vm
	}
	return &Snapshot{keys: keys, data: data, seeks: &ix.seeks}
}

// Seeks 返回累计 SeekGE 次数。
func (ix *Index) Seeks() int64 {
	return atomic.LoadInt64(&ix.seeks)
}

// ResetSeeks 清零寻址计数器。
func (ix *Index) ResetSeeks() {
	atomic.StoreInt64(&ix.seeks, 0)
}

// Snapshot 是某一时刻的不可变视图。
type Snapshot struct {
	keys  []string
	data  map[string]map[int]bool
	seeks *int64
	pos   int
}

// SeekGE 返回不小于 k 的第一个键；不存在时 ok 为 false。
func (s *Snapshot) SeekGE(k string) (key string, ok bool) {
	atomic.AddInt64(s.seeks, 1)
	i := sort.SearchStrings(s.keys, k)
	s.pos = i
	if i >= len(s.keys) {
		return "", false
	}
	return s.keys[i], true
}

// Current 返回当前位置的键；游标到末尾时 ok 为 false。
func (s *Snapshot) Current() (key string, ok bool) {
	if s.pos >= len(s.keys) {
		return "", false
	}
	return s.keys[s.pos], true
}

// Next 顺序前进到下一个键（不产生寻址计数）。
func (s *Snapshot) Next() {
	if s.pos < len(s.keys) {
		s.pos++
	}
}

// End 直接把游标置于末尾（如前缀已占满到键空间末尾）。
func (s *Snapshot) End() {
	s.pos = len(s.keys)
}

// Versions 返回 key 的全部版本，版本号降序。
func (s *Snapshot) Versions(key string) []Version {
	vers := s.data[key]
	nums := make([]int, 0, len(vers))
	for n := range vers {
		nums = append(nums, n)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(nums)))
	out := make([]Version, 0, len(nums))
	for _, n := range nums {
		out = append(out, Version{Number: n, Deleted: vers[n]})
	}
	return out
}

// Succ 计算 succ(P)；键空间末尾时 ok 为 false。
func Succ(p string) (string, bool) {
	i := len(p) - 1
	for i >= 0 && p[i] == 0xFF {
		i--
	}
	if i < 0 {
		return "", false
	}
	out := make([]byte, i+1)
	copy(out, p[:i+1])
	out[i]++
	return string(out), true
}
