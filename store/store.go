// Package store 在多个 key 之上管理版本：成功写入才分配全局连续 Seq，
// 提供 AsOf 时间旅行读与按位点 Compact。它依赖 hist 管理单 key 版本链。
package store

import (
	"errors"
	"sort"
	"sync"

	"ontology/hist"
)

// Val 是写入值，按不可变数据使用。
type Val = hist.Val

var (
	// ErrEmptyKey：写入非法（key 为空串）。
	ErrEmptyKey = errors.New("store: key must not be empty")
	// ErrNegativeRead：读位点非法（s < 0）。
	ErrNegativeRead = errors.New("store: read sequence must not be negative")
	// ErrCompacted：读位点不可达（s <= 最近一次 Compact 的 upto）。
	ErrCompacted = errors.New("store: read sequence compacted and unreachable")
	// ErrNegativeCompact：Compact 参数非法（upto < 0）。
	ErrNegativeCompact = errors.New("store: compact upto must not be negative")
)

// Store 是进程内、并发安全的多 key 版本存储。
type Store struct {
	mu        sync.RWMutex
	chains    map[string]*hist.Chain
	maxSeq    int // 已成功分配的最大 Seq；从 1 开始，初始 0
	compacted int // 最近一次 Compact 的 upto；-1 表示从未 compact
}

// New 创建空存储。
func New() *Store {
	return &Store{chains: make(map[string]*hist.Chain), compacted: -1}
}

// Write 成功才分配 Seq=maxSeq+1；key 为空整体失败且不留痕（不分配 Seq）。
func (s *Store) Write(key string, v Val) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key == "" {
		return 0, ErrEmptyKey // 校验先于任何状态变更
	}
	c := s.chains[key]
	if c == nil {
		c = new(hist.Chain)
		s.chains[key] = c
	}
	s.maxSeq++
	c.Append(s.maxSeq, v)
	return s.maxSeq, nil
}

// AsOf 返回「只应用到 Seq s 为止」的视图：每 key 取 Seq<=s 的最新写入值。
// s<0 拒绝；s<=compacted 报不可达；s==0 为空视图；s 超过最新 Seq 收敛到最新。
func (s *Store) AsOf(seq int) (map[string]Val, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq < 0 {
		return nil, ErrNegativeRead
	}
	if seq <= s.compacted {
		return nil, ErrCompacted // 不可达即报错，绝不返回旧数据或空视图冒充
	}
	if seq > s.maxSeq {
		seq = s.maxSeq // 读位点超过最新 Seq：收敛到最新，不报错
	}
	view := make(map[string]Val, len(s.chains))
	for k, c := range s.chains {
		if v, ok := c.At(seq); ok {
			view[k] = v
		}
	}
	return view, nil
}

// MaxSeq 返回已成功分配的最大 Seq（未写入过时为 0）。
func (s *Store) MaxSeq() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxSeq
}

// CompactedTo 返回最近一次 Compact 的 upto（从未 compact 时为 -1）。
func (s *Store) CompactedTo() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.compacted
}

// Compact 回收历史：upto<0 拒绝且不留痕；之后 s<=upto 一律不可达，
// 而 s>upto 的读与 compact 前逐 key 相同（靠各链保留的基线版本保证）。
func (s *Store) Compact(upto int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if upto < 0 {
		return ErrNegativeCompact // 校验先于 compated 位点变更
	}
	if upto > s.compacted {
		s.compacted = upto // 位点只单调推进
	}
	for _, c := range s.chains {
		c.Compact(upto)
	}
	return nil
}

// SortedKeys 返回 key 的排序列表，供自检做稳定比对。
func (s *Store) SortedKeys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ks := make([]string, 0, len(s.chains))
	for k := range s.chains {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
