// Package api 热冷两层状态存储的对外门面。依赖 store。
package api

import (
	"errors"
	"fmt"

	"ontology/store"
)

// 可判定的哨兵错误，四者互不相同。
var (
	ErrBadCap   = store.ErrBadCap
	ErrEmptyKey = store.ErrEmptyKey
	ErrNotFound = store.ErrNotFound
	ErrColdFull = store.ErrColdFull
)

// Store 对上层表现为一个普通的 Key→Value 映射。
type Store struct{ s *store.Store }

func New(hotCap, maxCold int) (*Store, error) {
	s, err := store.New(hotCap, maxCold)
	if err != nil {
		return nil, err
	}
	return &Store{s: s}, nil
}

func (s *Store) Put(key string, v int64) error  { return s.s.Put(key, v) }
func (s *Store) Get(key string) (int64, error)  { return s.s.Get(key) }
func (s *Store) Value(key string) (int64, bool) { return s.s.Value(key) }
func (s *Store) HotKeys() []string              { return s.s.HotKeys() }
func (s *Store) ColdKeys() []string             { return s.s.ColdKeys() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 序列一（不变量 2、3）：第三节八步表，逐步核对热层顺序、冷层集合、Get 值。
	s, err := New(2, 100)
	if err != nil {
		return err
	}
	type step struct {
		put       bool
		key       string
		val, get  int64
		hot, cold string
	}
	seq := []step{
		{true, "A", 1, 0, "[A]", "[]"},
		{true, "B", 2, 0, "[B A]", "[]"},
		{false, "A", 0, 1, "[A B]", "[]"},
		{true, "C", 3, 0, "[C A]", "[B]"},
		{false, "B", 0, 2, "[B C]", "[A]"},
		{true, "D", 4, 0, "[D B]", "[A C]"},
		{false, "A", 0, 1, "[A D]", "[B C]"},
		{true, "E", 5, 0, "[E A]", "[B C D]"},
	}
	for i, st := range seq {
		var got int64
		var e error
		if st.put {
			e = s.Put(st.key, st.val)
		} else {
			got, e = s.Get(st.key)
		}
		bad := e != nil || got != st.get
		bad = bad || fmt.Sprint(s.HotKeys()) != st.hot || fmt.Sprint(s.ColdKeys()) != st.cold
		if bad {
			return fmt.Errorf("序列一第%d步: hot=%v cold=%v get=%d err=%v", i+1, s.HotKeys(), s.ColdKeys(), got, e)
		}
	}
	// 序列二（不变量 1、2）：确定性交错序列，值与朴素 map 逐键一致、分层容量成立。
	s2, _ := New(3, 4)
	m := map[string]int64{}
	keys := []string{"a", "b", "c", "d", "e", "f", "g"}
	seed := uint32(20260925)
	next := func() uint32 { seed = seed*1664525 + 1013904223; return seed >> 16 }
	for i := 0; i < 400; i++ {
		k := keys[next()%uint32(len(keys))]
		if next()%2 == 0 {
			v := int64(next() % 1000)
			if e := s2.Put(k, v); e != nil {
				return fmt.Errorf("序列二第%d步 Put: %v", i, e)
			}
			m[k] = v
			continue
		}
		got, e := s2.Get(k)
		if want, ok := m[k]; !ok {
			if !errors.Is(e, ErrNotFound) {
				return fmt.Errorf("序列二第%d步: 应 ErrNotFound, got %v", i, e)
			}
		} else if e != nil || got != want {
			return fmt.Errorf("序列二第%d步: got (%d,%v) want (%d,nil)", i, got, e, want)
		}
	}
	hot, cold := s2.HotKeys(), s2.ColdKeys()
	seen := map[string]bool{}
	for _, ks := range [][]string{hot, cold} {
		for _, k := range ks {
			if seen[k] {
				return fmt.Errorf("热冷交集非空: %s", k)
			}
			seen[k] = true
		}
	}
	if len(seen) != len(m) || len(hot) > 3 || len(cold) > 4 {
		return fmt.Errorf("分层不一致: hot=%d cold=%d all=%d", len(hot), len(cold), len(m))
	}
	for k, v := range m {
		if gv, ok := s2.Value(k); !ok || gv != v {
			return fmt.Errorf("值不一致 %s: got %d,%v want %d", k, gv, ok, v)
		}
	}
	// 序列三（不变量 4）：冷层满与各类拒绝不留痕。
	s3, _ := New(1, 1)
	_ = s3.Put("a", 1)
	_ = s3.Put("b", 2) // a 换出到冷层，冷层已满
	before := fmt.Sprint(s3.HotKeys(), s3.ColdKeys())
	if e := s3.Put("c", 3); !errors.Is(e, ErrColdFull) {
		return fmt.Errorf("冷层满应 ErrColdFull, got %v", e)
	}
	if _, e := s3.Get("zz"); !errors.Is(e, ErrNotFound) {
		return fmt.Errorf("不存在键应 ErrNotFound, got %v", e)
	}
	if _, e := s3.Get(""); !errors.Is(e, ErrEmptyKey) {
		return fmt.Errorf("空键应 ErrEmptyKey, got %v", e)
	}
	if fmt.Sprint(s3.HotKeys(), s3.ColdKeys()) != before {
		return errors.New("被拒操作改变了状态")
	}
	if v, _ := s3.Value("a"); v != 1 {
		return errors.New("换出未保留值")
	}
	if _, e := New(0, 1); !errors.Is(e, ErrBadCap) {
		return fmt.Errorf("非法容量应 ErrBadCap, got %v", e)
	}
	return nil
}
