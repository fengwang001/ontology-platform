// Package api 是增量检查点能力的对外入口。依赖方向：api -> snap -> state。
package api

import (
	"fmt"
	"maps"
	"math/rand"
	"sync"

	"ontology/snap"
	"ontology/state"
)

// 三类可判定哨兵错误（透传下层，errors.Is 可判定，互不相同）。
var (
	ErrEmptyKey    = state.ErrEmptyKey
	ErrTooManyKeys = state.ErrTooManyKeys
	ErrNoBase      = snap.ErrNoBase
)

// Store 是并发安全的检查点存储（状态仅在进程内存）。
type Store struct {
	mu sync.RWMutex
	st *state.State
	cp *snap.Checkpointer
}

// New 创建键数上限为 maxKeys 的存储。
func New(maxKeys int) *Store {
	st := state.New(maxKeys)
	return &Store{st: st, cp: snap.New(st)}
}

func (s *Store) Set(k string, v int64) error { s.mu.Lock(); defer s.mu.Unlock(); return s.st.Set(k, v) }

func (s *Store) Delete(k string) { s.mu.Lock(); defer s.mu.Unlock(); s.st.Delete(k) }

func (s *Store) Checkpoint() error { s.mu.Lock(); defer s.mu.Unlock(); return s.cp.Checkpoint() }

func (s *Store) Recover() (map[string]int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cp.Recover()
}

func (s *Store) View() map[string]int64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.st.Snapshot() }

func (s *Store) History() []snap.Snapshot { s.mu.RLock(); defer s.mu.RUnlock(); return s.cp.History() }

// SelfCheck 用内置随机操作序列核验第二节四条不变量；nil 表示全部通过。
func (s *Store) SelfCheck() error {
	rng := rand.New(rand.NewSource(407))
	for iter := 0; iter < 100; iter++ {
		store := New(40)
		naive := map[string]int64{}      // 朴素参照：当前活跃状态
		saved := []map[string]int64{nil} // saved[i+1] = 第 i 次检查点的朴素深拷贝
		for i := 0; i < 300; i++ {
			k := fmt.Sprintf("k%d", rng.Intn(30))
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4, 5: // Set（含 0 值与负值）
				v := int64(rng.Intn(7) - 3)
				if err := store.Set(k, v); err != nil {
					return err
				}
				naive[k] = v
			case 6, 7: // Delete
				store.Delete(k)
				delete(naive, k)
			default: // Checkpoint：朴素参照直接深拷贝整张活跃 map
				if err := store.Checkpoint(); err != nil {
					return err
				}
				saved = append(saved, maps.Clone(naive))
			}
		}
		if len(saved) == 1 {
			continue
		}
		rec, err := store.Recover() // 不变量 1：与朴素参照一致
		if err != nil || !maps.Equal(rec, saved[len(saved)-1]) {
			return fmt.Errorf("invariant 1: recover mismatch at iter %d", iter)
		}
		for i, sn := range store.History() { // 不变量 2+3：覆盖合并、最小 delta、tombstone 完整
			prev, cur, want := saved[i], saved[i+1], map[string]snap.Change{}
			for k, v := range cur { // 新增或值变化（i==0 时 prev 为 nil，全部为新增）
				if pv, ok := prev[k]; !ok || pv != v {
					want[k] = snap.Change{Value: v}
				}
			}
			for k := range prev { // 删除必须记 tombstone
				if _, ok := cur[k]; !ok {
					want[k] = snap.Change{Deleted: true}
				}
			}
			if len(sn.Data) != len(want) {
				return fmt.Errorf("invariant 2/3: delta not minimal at iter %d cp %d", iter, i)
			}
			for k, w := range want {
				if sn.Data[k] != w {
					return fmt.Errorf("invariant 2/3: change mismatch iter %d cp %d key %s", iter, i, k)
				}
			}
		}
		if !maps.Equal(store.View(), naive) { // 检查点之后的未提交变更仍在活跃状态
			return fmt.Errorf("active state mismatch at iter %d", iter)
		}
	}
	return checkRejectedOpsLeaveNoTrace() // 不变量 4
}

// checkRejectedOpsLeaveNoTrace 核验三类拒绝互不相同、被拒后状态不变且可继续使用。
func checkRejectedOpsLeaveNoTrace() error {
	s := New(1)
	if err := s.Set("", 1); err != ErrEmptyKey {
		return fmt.Errorf("invariant 4: empty-key error = %v", err)
	}
	if err := s.Set("a", 1); err != nil {
		return err
	}
	if err := s.Set("b", 2); err != ErrTooManyKeys {
		return fmt.Errorf("invariant 4: over-limit error = %v", err)
	}
	if _, err := s.Recover(); err != ErrNoBase {
		return fmt.Errorf("invariant 4: no-base error = %v", err)
	}
	if !maps.Equal(s.View(), map[string]int64{"a": 1}) {
		return fmt.Errorf("invariant 4: state changed after rejection")
	}
	if err := s.Checkpoint(); err != nil { // 被拒后仍可继续正常使用
		return err
	}
	rec, err := s.Recover()
	if err != nil || !maps.Equal(rec, map[string]int64{"a": 1}) {
		return fmt.Errorf("invariant 4: unusable after rejection")
	}
	return nil
}
