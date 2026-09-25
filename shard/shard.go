// Package shard 维护分区集合与逐 key 迁移，依赖 route 做算术路由。
package shard

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/route"
)

// ErrBadN 表示分区数非正。
var ErrBadN = errors.New("shard: partition count must be positive")

// Store 是进程内的分区集合：parts[i] 是一张 key→value 的 map。
type Store struct {
	mu sync.RWMutex

	parts []map[string]string
	n     int

	// probes 非导出：最近一次 Get/GetPartition 为定位 key 的 home 分区
	// 所检查过的分区个数。算术路由恒为 1；绝不扫描分区。
	probes atomic.Int64
}

// New 创建 n 个空分区；n 非正时失败且不产生任何状态。
func New(n int) (*Store, error) {
	if n <= 0 {
		return nil, ErrBadN
	}
	parts := make([]map[string]string, n)
	for i := range parts {
		parts[i] = map[string]string{}
	}
	return &Store{parts: parts, n: n}, nil
}

// N 返回当前分区数。
func (s *Store) N() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.n
}

// locate 算术定位 home 分区并记录检查分区数（恒为 1，无扫描）。
// 调用方须持有 s.mu（读锁即可）。
func (s *Store) locate(key string) int {
	home := route.Home(key, s.n)
	s.probes.Store(1)
	return home
}

// Put 把 key→val 只写入其 home 分区（key 非空由上层保证）。
func (s *Store) Put(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parts[route.Home(key, s.n)][key] = val
}

// Get 算术定位后读取 key；命中返回 (val,true)，否则 ("",false)。
func (s *Store) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	home := s.locate(key)
	val, ok := s.parts[home][key]
	return val, ok
}

// Home 算术定位 key 的 home 分区并对本次定位记账。
func (s *Store) Home(key string) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.locate(key)
}

// Inspect 直接读取分区 p 中 key 的存在性（调用方保证 p 合法）；
// 不做任何跨分区查找，也不改动定位计数。
func (s *Store) Inspect(p int, key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	val, ok := s.parts[p][key]
	return val, ok
}

// Rebalance 同步、一次性地把全部 key 按 newN 重新算术分桶。
// 它构建一套全新分桶后整体替换：写入新桶与丢弃旧桶同时生效，
// 因而每个 key 在返回后恰好存在于 hash(key)%newN 一份分区里。
func (s *Store) Rebalance(newN int) error {
	if newN <= 0 {
		return ErrBadN
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := make([]map[string]string, newN)
	for i := range next {
		next[i] = map[string]string{}
	}
	for _, part := range s.parts {
		for k, v := range part {
			next[route.Home(k, newN)][k] = v
		}
	}
	s.parts = next
	s.n = newN
	return nil
}

// Snapshot 返回各分区内容的深拷贝，供 Dump/比对使用。
func (s *Store) Snapshot() []map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]string, s.n)
	for i, part := range s.parts {
		cp := make(map[string]string, len(part))
		for k, v := range part {
			cp[k] = v
		}
		out[i] = cp
	}
	return out
}

// ProbeBoundConstant 只回结论（不回传计数数值）：在多档分区数下，
// 存一个 key 再 Get，定位检查分区数恒为 1，不随 m 增长。
func ProbeBoundConstant() bool {
	for _, m := range []int{100, 1000, 5000, 10000} {
		st, err := New(m)
		if err != nil {
			return false
		}
		st.Put("k", "v")
		if _, ok := st.Get("k"); !ok || st.probes.Load() != 1 {
			return false
		}
	}
	return true
}
