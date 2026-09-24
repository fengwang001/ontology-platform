// Package rrep 维护 n 个副本的读写、回填执行与回填计数。依赖 rep。
package rrep

import (
	"sync"

	"ontology/rep"
)

// Store 是 n 副本的键值存储，状态在进程内存。
type Store struct {
	mu      sync.Mutex
	n       int
	keys    map[string]*keyState
	lastCmp int // 最近一次 Read 判定 winner 时逐副本比较过的条目数（非导出）
}

type keyState struct {
	entries []rep.Entry
	winner  int // 当前 winner 下标，-1 表示该 key 无任何条目
}

// NewStore 创建 n 副本存储；调用方保证 n >= 1。
func NewStore(n int) *Store {
	return &Store{n: n, keys: map[string]*keyState{}}
}

// Put 把 (value, ver) 写到 reps 里的每个副本，其余副本不动。调用方保证参数合法。
func (s *Store) Put(key, value string, ver int64, reps []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil {
		ks = &keyState{entries: make([]rep.Entry, s.n), winner: -1}
		for i := range ks.entries {
			ks.entries[i].Empty = true
		}
		s.keys[key] = ks
	}
	e := rep.Entry{Value: value, Ver: ver}
	downgraded := false
	for _, r := range reps {
		if r == ks.winner && ks.entries[r] != e {
			downgraded = true // 覆盖了当前 winner，索引需重建
		}
		ks.entries[r] = e
	}
	switch {
	case ks.winner == -1:
		ks.winner = reps[0]
		for _, r := range reps[1:] {
			if rep.Beats(ks.entries[r], ks.entries[ks.winner]) {
				ks.winner = r
			}
		}
	case downgraded:
		_, ks.winner, _ = rep.Winner(ks.entries) // 罕见路径：全量重建索引
	default:
		for _, r := range reps {
			if rep.Beats(ks.entries[r], ks.entries[ks.winner]) {
				ks.winner = r
			}
		}
	}
}

// Read 判定 winner 并回填落后副本，返回 (value, repaired, found)。
func (s *Store) Read(key string) (string, int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil || ks.winner == -1 {
		s.lastCmp = 0
		return "", 0, false
	}
	// 按版本维护的 winner 索引 O(1) 定位，只检查索引指向的那 1 个条目。
	s.lastCmp = 1
	w := ks.entries[ks.winner]
	repaired := 0
	for i, e := range ks.entries {
		if rep.NeedsRepair(e, w) {
			ks.entries[i] = w // 只升不降：w 恒为最大版本
			repaired++
		}
	}
	return w.Value, repaired, true
}

// Snapshot 返回 key 各副本当前条目的副本。
func (s *Store) Snapshot(key string) []rep.Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	ks := s.keys[key]
	if ks == nil {
		return nil
	}
	out := make([]rep.Entry, s.n)
	copy(out, ks.entries)
	return out
}
