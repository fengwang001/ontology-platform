// Package api 提供列级 LWW 存储的对外接口：Put/Del/View/Conflicted/SelfCheck。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/row"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrEmptyKey   = errors.New("api: empty key")
	ErrEmptyCol   = errors.New("api: empty col")
	ErrNegativeTS = errors.New("api: negative timestamp")
	ErrEmptyVal   = errors.New("api: empty value (use Del)")
)

// Col 标识一对 (Key, Col)。
type Col struct{ Key, Name string }

// Store 是进程内存中的列级 LWW 存储，并发安全。
type Store struct {
	mu        sync.RWMutex
	rows      map[string]*row.Row
	conflicts map[Col]struct{}
}

func New() *Store {
	return &Store{rows: make(map[string]*row.Row), conflicts: make(map[Col]struct{})}
}

func checkArgs(key, col string, ts int64) error {
	switch {
	case key == "":
		return ErrEmptyKey
	case col == "":
		return ErrEmptyCol
	case ts < 0:
		return ErrNegativeTS
	}
	return nil
}

// Put 写入一列。Val 为空串被拒绝；任何拒绝都整体失败、不留痕。
func (s *Store) Put(key, col string, ts int64, val string) error {
	if val == "" {
		return ErrEmptyVal
	}
	return s.apply(key, col, ts, val, false)
}

// Del 给一列写墓碑。任何拒绝都整体失败、不留痕。
func (s *Store) Del(key, col string, ts int64) error {
	return s.apply(key, col, ts, "", true)
}

// apply 先校验后改状态：校验失败时不触碰任何数据。
func (s *Store) apply(key, col string, ts int64, val string, del bool) error {
	if err := checkArgs(key, col, ts); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.rows[key]
	if r == nil {
		r = row.New()
		s.rows[key] = r
	}
	conflict := false
	if del {
		conflict = r.Del(col, ts)
	} else {
		conflict = r.Put(col, ts, val)
	}
	if conflict {
		s.conflicts[Col{key, col}] = struct{}{}
	}
	return nil
}

// View 返回每个 Key 当前有值的列；被删或从未写入的列缺席。
func (s *Store) View() map[string]map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]map[string]string, len(s.rows))
	for k, r := range s.rows {
		if v := r.View(); len(v) > 0 {
			out[k] = v
		}
	}
	return out
}

// Conflicted 返回发生过冲突的 (Key,Col) 集合。
func (s *Store) Conflicted() map[Col]bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[Col]bool, len(s.conflicts))
	for c := range s.conflicts {
		out[c] = true
	}
	return out
}

// SelfCheck 用内置写入序列核验四条不变量（在独立的内部 Store 上，不影响接收者）。
func SelfCheck() error {
	s := New()
	steps := []struct {
		op   func() error
		want map[string]string
	}{
		{func() error { return s.Put("R", "c1", 5, "a") }, map[string]string{"c1": "a"}},
		{func() error { return s.Put("R", "c2", 7, "x") }, map[string]string{"c1": "a", "c2": "x"}},
		{func() error { return s.Put("R", "c1", 5, "b") }, map[string]string{"c1": "b", "c2": "x"}},
		{func() error { return s.Put("R", "c1", 9, "c") }, map[string]string{"c1": "c", "c2": "x"}},
		{func() error { return s.Del("R", "c2", 8) }, map[string]string{"c1": "c"}},
		{func() error { return s.Put("R", "c1", 4, "old") }, map[string]string{"c1": "c"}},
		{func() error { return s.Put("R", "c3", 6, "y") }, map[string]string{"c1": "c", "c3": "y"}},
		{func() error { return s.Del("R", "c3", 2) }, map[string]string{"c1": "c", "c3": "y"}},
	}
	for i, st := range steps {
		if err := st.op(); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if got := s.View()["R"]; !reflect.DeepEqual(got, st.want) {
			return fmt.Errorf("selfcheck step %d: view %v, want %v", i+1, got, st.want)
		}
	}
	if !s.Conflicted()[Col{"R", "c1"}] || len(s.Conflicted()) != 1 {
		return errors.New("selfcheck: conflict set wrong")
	}
	// 不变量 4：四类拒绝不留痕
	before := s.View()
	for _, err := range []error{
		s.Put("", "c", 1, "v"), s.Put("R", "", 1, "v"),
		s.Put("R", "c", -1, "v"), s.Put("R", "c", 1, ""),
		s.Del("", "c", 1), s.Del("R", "", 1), s.Del("R", "c", -1),
	} {
		if err == nil {
			return errors.New("selfcheck: invalid op accepted")
		}
	}
	if !reflect.DeepEqual(s.View(), before) || len(s.Conflicted()) != 1 {
		return errors.New("selfcheck: rejected op left trace")
	}
	return nil
}
