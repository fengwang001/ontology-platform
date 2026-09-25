// Package shard 持有分区集合（[]map[string]string），负责 Put/Get 与
// Rebalance 的逐 key 迁移。分区定位一律走 route 包的算术 home，绝不扫描分区。
package shard

import (
	"maps"
	"sync"
	"sync/atomic"

	"ontology/internal/route"
)

// Shard 是一组按亲和键路由的内存分区。
type Shard struct {
	mu sync.RWMutex
	n  int
	p  []map[string]string

	// probes 记录最近一次 Get/GetPartition 为定位 key 所在分区而检查过的
	// 分区个数。非导出：包外没有任何途径读到它，仅同包白盒测试可直接检查。
	// 用 atomic 是因为并发读持有 RLock，仍会并发更新这个计数。
	probes atomic.Int64
}

// New 创建 n 个空分区。调用方（api 层）负责 n > 0 的校验。
func New(n int) *Shard {
	s := &Shard{n: n, p: make([]map[string]string, n)}
	for i := range s.p {
		s.p[i] = map[string]string{}
	}
	return s
}

// N 返回当前分区数。
func (s *Shard) N() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.n
}

// Put 把 key 写入算术 home 分区，绝不在别处留副本。
func (s *Shard) Put(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.p[route.Home(key, s.n)][key] = val
}

// Get 算术直达 home 分区检查一个 map：probes 恒为 1，与分区总数无关。
func (s *Shard) Get(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.probes.Store(1)
	v, ok := s.p[route.Home(key, s.n)][key]
	return v, ok
}

// At 在调用方已确认 p == home 后检查指定分区，同样只碰一个分区。
func (s *Shard) At(p int, key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.probes.Store(1)
	v, ok := s.p[p][key]
	return v, ok
}

// Routed 记录一次纯算术的二次路由提示：未检查任何分区内容（probes=0）。
func (s *Shard) Routed() {
	s.probes.Store(0)
}

// Rebalance 同步、一次性地把每个 key 迁到 hash(key)%newN：home 变了就
// 写到新分区并从旧分区删除，home 没变就留在原处。返回后每个 key 恰好一份。
func (s *Shard) Rebalance(newN int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	oldN := s.n
	if newN == oldN {
		return
	}
	if newN > oldN {
		for i := oldN; i < newN; i++ {
			s.p = append(s.p, map[string]string{})
		}
	}
	if newN < oldN {
		// 截断前先把尾部分区的 key 搬入前 newN 个分区。
		for oldP := newN; oldP < oldN; oldP++ {
			for k, v := range s.p[oldP] {
				h := route.Home(k, newN)
				s.p[h][k] = v
				delete(s.p[oldP], k)
			}
		}
		s.p = s.p[:newN]
	}
	// 原地逐 key 迁移；迁移期间持写锁，外部观察不到任何中间态。
	for oldP := 0; oldP < newN; oldP++ {
		m := s.p[oldP]
		for k, v := range m {
			h := route.Home(k, newN)
			if h != oldP {
				s.p[h][k] = v // 写到新分区
				delete(m, k)  // 立刻从旧分区删除，杜绝双份
			}
		}
	}
	s.n = newN
}

// Dump 返回各分区内容的深拷贝快照，调用方可任意修改。
func (s *Shard) Dump() []map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]string, s.n)
	for i := range s.p {
		out[i] = maps.Clone(s.p[i])
	}
	return out
}
