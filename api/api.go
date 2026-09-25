// Package api 是物化 join 索引的对外入口，所有方法并发安全。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"

	"ontology/jidx"
	"ontology/rel"
)

// ErrNoSuchA：DelR 的 a 不在 R 中；与 rel.ErrMissingPair、rel.ErrDuplicatePair 互异。
var ErrNoSuchA = errors.New("api: a does not exist in R")

// System 组合 rel 存储与 jidx 索引；单把锁保证读者只见完整操作后的串行快照。
type System struct {
	mu sync.Mutex
	st *rel.Store
	ix *jidx.Index
}

// New 创建空系统。
func New() *System {
	st := rel.New()
	return &System{st: st, ix: jidx.New(st)}
}

func (s *System) setRLocked(a, b int) {
	if old, existed := s.st.SetR(a, b); existed {
		s.ix.RebindR(a, old, b) // 重赋：先删旧 b 全部对，再按新 b 建
	} else {
		s.ix.BindR(a, b)
	}
}
func (s *System) delRLocked(a int) error {
	old, ok := s.st.GetR(a)
	if !ok {
		return ErrNoSuchA // 触碰任何状态之前拒绝
	}
	s.st.DelR(a)
	s.ix.UnbindR(a, old)
	return nil
}
func (s *System) addSLocked(b, c int) error {
	if err := s.st.AddS(b, c); err != nil {
		return err // rel 层改动前判重，索引未触碰
	}
	s.ix.AddC(b, c)
	return nil
}
func (s *System) delSLocked(b, c int) error {
	if err := s.st.DelS(b, c); err != nil {
		return err // rel 层改动前判存在，索引未触碰
	}
	s.ix.DelC(b, c)
	return nil
}

// SetR 令 R[a]=b；重赋先清旧 b 的全部 join 对再按新 b 重建。
func (s *System) SetR(a, b int) { s.mu.Lock(); defer s.mu.Unlock(); s.setRLocked(a, b) }

// DelR 删除 a 并级联移除其全部 join 对；a 不存在返回 ErrNoSuchA。
func (s *System) DelR(a int) error { s.mu.Lock(); defer s.mu.Unlock(); return s.delRLocked(a) }

// AddS 把 c 加入 S[b] 并扇出建 join 对；重复返回 rel.ErrDuplicatePair。
func (s *System) AddS(b, c int) error { s.mu.Lock(); defer s.mu.Unlock(); return s.addSLocked(b, c) }

// DelS 从 S[b] 移除 c 并扇出删 join 对；不存在返回 rel.ErrMissingPair。
func (s *System) DelS(b, c int) error { s.mu.Lock(); defer s.mu.Unlock(); return s.delSLocked(b, c) }

// Join 返回与 a 可连接的 c（升序去重）。
func (s *System) Join(a int) []int { s.mu.Lock(); defer s.mu.Unlock(); return s.ix.Join(a) }

// JoinSize 返回 join 对总数。
func (s *System) JoinSize() int { s.mu.Lock(); defer s.mu.Unlock(); return s.ix.Size() }

// recomputeLocked 扫 R 与 S 重新枚举所有 (a,c)，返回 a→升序 c 集 与总对数。
func (s *System) recomputeLocked() (map[int][]int, int) {
	want := map[int][]int{}
	total := 0
	s.st.RangeR(func(a, b int) bool {
		cs := s.st.Members(b)
		want[a], total = append(want[a], cs...), total+len(cs)
		return true
	})
	for a := range want {
		sort.Ints(want[a])
	}
	return want, total
}

// 第三节八步序列：每步一个持锁操作，以及该步后 join 对总数的正确值。
var (
	stepSizes = []int{0, 0, 2, 4, 2, 1, 2, 1}
	eightStep = []func(*System){
		func(s *System) { s.setRLocked(1, 10) },
		func(s *System) { s.setRLocked(2, 10) },
		func(s *System) { s.addSLocked(10, 100) },
		func(s *System) { s.addSLocked(10, 200) },
		func(s *System) { s.delSLocked(10, 100) },
		func(s *System) { s.setRLocked(1, 20) },
		func(s *System) { s.addSLocked(20, 300) },
		func(s *System) { s.delRLocked(2) },
	}
)

// SelfCheck 对内置八步逐步做增量 vs 扫表重算对拍，再验三类拒绝互异且不留痕与复杂度。
func (s *System) SelfCheck() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, op := range eightStep {
		op(s) // 内置序列均为合法操作
		want, total := s.recomputeLocked()
		if total != stepSizes[i] || s.ix.Size() != total {
			return fmt.Errorf("step %d: ix=%d batch=%d table=%d", i+1, s.ix.Size(), total, stepSizes[i])
		}
		for _, a := range []int{1, 2} {
			if !reflect.DeepEqual(s.ix.Join(a), want[a]) {
				return fmt.Errorf("step %d: Join(%d)=%v want %v", i+1, a, s.ix.Join(a), want[a])
			}
		}
	}
	if !reflect.DeepEqual(s.ix.Join(1), []int{300}) {
		return fmt.Errorf("Join(1)=%v want [300]", s.ix.Join(1))
	}
	before, beforeN := s.recomputeLocked()
	must := func(err, want error) error {
		if !errors.Is(err, want) {
			return fmt.Errorf("got %v, want sentinel %v", err, want)
		}
		return nil
	}
	if err := must(s.delRLocked(999), ErrNoSuchA); err != nil {
		return err
	}
	if err := must(s.delSLocked(10, 999), rel.ErrMissingPair); err != nil {
		return err
	}
	if err := must(s.addSLocked(20, 300), rel.ErrDuplicatePair); err != nil {
		return err
	}
	after, n := s.recomputeLocked()
	if n != beforeN || !reflect.DeepEqual(after, before) || s.ix.Size() != beforeN {
		return errors.New("rejected op left a trace")
	}
	return jidx.SelfTest() // 用独立数据验证删除检查数不随 N 增长，不暴露数值
}
