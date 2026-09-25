// Package snap 维护每 Key 版本历史、按快照取值、快照登记与释放。依赖 ver。
package snap

import (
	"sort"

	"ontology/ver"
)

type entry struct {
	v   ver.Version
	val int64
}

// Store 是内存版本化键值存储。本类型不做并发控制，由调用方（api 包）加锁。
type Store struct {
	cur     ver.Version         // 当前全局版本号
	hist    map[string][]entry  // 每 Key 的版本历史，按 ver 递增
	live    map[ver.Version]int // 存活快照登记（引用计数：同 ver 多次 Snapshot 各算一个）
	liveN   int                 // 存活快照总数
	checked int                 // 最近一次 Read 检查的历史条目个数（非导出，仅供包内测试观察）
}

func New() *Store {
	return &Store{hist: map[string][]entry{}, live: map[ver.Version]int{}}
}

// Write 使 ver 递增 1 并追加 (ver, v) 到 key 的历史，返回新 ver。
func (s *Store) Write(key string, v int64) ver.Version {
	s.cur++
	s.hist[key] = append(s.hist[key], entry{s.cur, v})
	return s.cur
}

// Snapshot 返回当前 ver 作为快照 id 并登记为存活（每次调用计一个）。
func (s *Store) Snapshot() ver.Version {
	s.live[s.cur]++
	s.liveN++
	return s.cur
}

// Read 返回 hist[key] 中 ver <= snap 的最大版本对应的值；无则 0。
// 只读：不改 cur、不改任何历史，只更新 checked 计数。
func (s *Store) Read(snap ver.Version, key string) int64 {
	h := s.hist[key]
	// 二分：第一个 ver > snap 的位置，其前一项即所求。
	i := sort.Search(len(h), func(i int) bool {
		s.checked++
		return !ver.LEQ(h[i].v, snap)
	})
	if i == 0 {
		return 0
	}
	return h[i-1].val
}

// ReadCurrent 返回最新值（历史末项，无则 0）。
func (s *Store) ReadCurrent(key string) int64 {
	h := s.hist[key]
	if len(h) == 0 {
		return 0
	}
	return h[len(h)-1].val
}

// Release 撤销一个快照的一次存活登记。
func (s *Store) Release(snap ver.Version) {
	s.live[snap]--
	if s.live[snap] == 0 {
		delete(s.live, snap)
	}
	s.liveN--
}

// Cur 返回当前全局版本号。
func (s *Store) Cur() ver.Version { return s.cur }

// Live 报告 snap 是否已登记为存活快照。
func (s *Store) Live(snap ver.Version) bool { return s.live[snap] > 0 }

// LiveCount 返回存活快照数。
func (s *Store) LiveCount() int { return s.liveN }
