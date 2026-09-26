// Package api 对外提供加权中位数服务：插入、查询、自检。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/median"
	"ontology/wmid"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrNonPositiveWeight = wmid.ErrNonPositiveWeight
	ErrDuplicateValue    = wmid.ErrDuplicateValue
	ErrEmpty             = median.ErrEmpty
)

// Store 保存已插入元素并回答加权中位数查询，并发安全。
type Store struct {
	mu   sync.RWMutex
	tree *wmid.Tree
	f    *median.Finder
}

// New 返回空 Store。
func New() *Store {
	t := wmid.New()
	return &Store{tree: t, f: median.New(t)}
}

// Insert 插入一个元素。失败（权重非正 / value 重复）时不改变任何状态。
func (s *Store) Insert(value, weight int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tree.Insert(value, weight)
}

// Median 返回当前加权中位数；空集合返回 ErrEmpty。
func (s *Store) Median() (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.f.Find()
}

// Total 返回总权重 W。
func (s *Store) Total() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if r := s.tree.Root(); r != nil {
		return r.Sum
	}
	return 0
}

// SelfCheck 对内置插入序列核验四条不变量，全部通过返回 nil。
// 只在内部新建的实例上运行，可被并发调用，不影响任何已有 Store。
func (s *Store) SelfCheck() error {
	seqs := [][][2]int64{
		{{30, 3}, {10, 3}, {40, 3}, {20, 3}},       // 第三节用例，期望中位数 20
		{{5, 1}, {1, 10}, {9, 1}},                  // 中位数压在最左
		{{1, 1}, {2, 1}, {3, 1}, {4, 1}, {5, 100}}, // 中位数压在最右
		{{7, 2}, {3, 5}, {11, 4}, {1, 1}, {9, 6}},  // 一般情形
	}
	for i, seq := range seqs {
		if err := checkSeq(seq); err != nil {
			return fmt.Errorf("selfcheck seq %d: %w", i, err)
		}
	}
	return checkRejectionNoTrace()
}

// checkSeq 核验不变量 1（两侧权重约束）、2（最小性）、3（与朴素参照一致）。
func checkSeq(seq [][2]int64) error {
	st := New()
	for _, p := range seq {
		if err := st.Insert(p[0], p[1]); err != nil {
			return err
		}
	}
	m, err := st.Median()
	if err != nil {
		return err
	}
	W := st.Total()
	var below, above, prefix int64
	naive := int64(0)
	found := false
	st.tree.InOrder(func(v, w int64) {
		switch {
		case v < m:
			below += w
		case v > m:
			above += w
		}
		prefix += w
		if !found && 2*prefix >= W {
			naive, found = v, true
		}
	})
	if 2*below > W || 2*above > W {
		return fmt.Errorf("invariant1 violated: below=%d above=%d W=%d", below, above, W)
	}
	if m != naive {
		return fmt.Errorf("invariant3 violated: got %d naive %d", m, naive)
	}
	// 最小性：m 之前所有元素的前缀和（即 below）必须严格 < W/2，
	// 否则存在更小的 value 已满足 P_k >= W/2。
	if 2*below >= W {
		return fmt.Errorf("invariant2 violated: smaller value already reaches W/2")
	}
	return nil
}

// checkRejectionNoTrace 核验不变量 4：被拒操作不改变任何状态。
func checkRejectionNoTrace() error {
	st := New()
	if _, err := st.Median(); !errors.Is(err, ErrEmpty) {
		return fmt.Errorf("empty median: want ErrEmpty, got %v", err)
	}
	for _, p := range [][2]int64{{10, 2}, {20, 3}, {30, 4}} {
		if err := st.Insert(p[0], p[1]); err != nil {
			return err
		}
	}
	beforeW, beforeM := st.Total(), mustMedian(st)
	rejects := []struct {
		v, w int64
		want error
	}{{10, 5, ErrDuplicateValue}, {99, 0, ErrNonPositiveWeight}, {99, -3, ErrNonPositiveWeight}}
	for _, r := range rejects {
		if err := st.Insert(r.v, r.w); !errors.Is(err, r.want) {
			return fmt.Errorf("insert(%d,%d): want %v, got %v", r.v, r.w, r.want, err)
		}
	}
	if st.Total() != beforeW || mustMedian(st) != beforeM {
		return errors.New("invariant4 violated: rejected op changed state")
	}
	return nil
}

func mustMedian(s *Store) int64 {
	m, err := s.Median()
	if err != nil {
		panic(err)
	}
	return m
}
