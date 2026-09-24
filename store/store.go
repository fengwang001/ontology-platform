// Package store 管理多 key：Write、AsOf、Compact 与全局 maxSeq。依赖 hist。
package store

import (
	"errors"
	"sync"

	"ontology/hist"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey        = errors.New("store: empty key")
	ErrNegativeRead    = errors.New("store: negative read seq")
	ErrCompacted       = errors.New("store: read seq compacted away")
	ErrNegativeCompact = errors.New("store: negative compact upto")
)

// Val 是写入的值类型。
type Val = hist.Val

// Store 是多 key 的版本化存储，并发安全。
type Store struct {
	mu          sync.RWMutex
	chains      map[string]*hist.Chain
	maxSeq      int64
	compactUpto int64 // 历次 Compact 的最大 upto；-1 表示从未 Compact
}

// New 返回空 Store。
func New() *Store {
	return &Store{chains: map[string]*hist.Chain{}, compactUpto: -1}
}

// Write 写入一个版本，成功才分配 Seq=maxSeq+1（首个为 1）。
// key 为空串拒绝，不分配 Seq、不改变任何状态。
func (s *Store) Write(key string, v hist.Val) (int64, error) {
	if key == "" {
		return 0, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxSeq++
	c := s.chains[key]
	if c == nil {
		c = &hist.Chain{}
		s.chains[key] = c
	}
	c.Append(s.maxSeq, v)
	return s.maxSeq, nil
}

// AsOf 返回「只应用到 Seq s 为止」的视图：每个 key 取 Seq<=s 的最新值。
// s<0 拒绝；s<=最近一次 Compact 的 upto 报 ErrCompacted；
// s 超过最新 Seq 时收敛到最新状态；s==0（未 Compact 过）返回空视图。
func (s *Store) AsOf(seq int64) (map[string]hist.Val, error) {
	if seq < 0 {
		return nil, ErrNegativeRead
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq <= s.compactUpto {
		return nil, ErrCompacted
	}
	out := make(map[string]hist.Val, len(s.chains))
	for k, c := range s.chains {
		if v, ok := c.At(seq); ok {
			out[k] = v
		}
	}
	return out, nil
}

// Compact 回收历史：每条链保留 Seq<=upto 的最新版本作基线，更老的丢弃。
// upto<0 拒绝。位点只前进不后退，保证已不可达的位点不会复活。
func (s *Store) Compact(upto int64) error {
	if upto < 0 {
		return ErrNegativeCompact
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.chains {
		c.Compact(upto)
	}
	if upto > s.compactUpto {
		s.compactUpto = upto
	}
	return nil
}

// MaxSeq 返回当前已分配的最大 Seq（未写入时为 0）。
func (s *Store) MaxSeq() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxSeq
}
