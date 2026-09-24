// Package rstore 保存右流记录集：按 K 的保存、查询、删除。
// 不依赖其他包；右流记录的生命周期独立于左流（DelL 不删右记录）。
package rstore

// Store 是右流记录集，K 唯一，重复 Put 同 K 即更新。
type Store struct {
	m map[string]int64
}

// New 返回空的右流记录集。
func New() *Store {
	return &Store{m: make(map[string]int64)}
}

// Get 查询 K 的右值；ok 为 false 表示右记录缺席。
func (s *Store) Get(k string) (v int64, ok bool) {
	v, ok = s.m[k]
	return v, ok
}

// Put 保存或更新 K 的右值。
func (s *Store) Put(k string, v int64) {
	s.m[k] = v
}

// Del 删除 K 的右记录；返回删除前是否存在。
func (s *Store) Del(k string) bool {
	if _, ok := s.m[k]; !ok {
		return false
	}
	delete(s.m, k)
	return true
}

// All 返回右记录集的副本（K -> RV）。
func (s *Store) All() map[string]int64 {
	out := make(map[string]int64, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out
}
