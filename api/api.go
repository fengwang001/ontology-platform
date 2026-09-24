// Package api 对外提供大值溢出存储的并发安全接口。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/srec"
)

// 可判定哨兵错误，三者互不相同。
var (
	ErrInvalidParam  = srec.ErrInvalidParam
	ErrEmptyKey      = srec.ErrEmptyKey
	ErrOverflowLimit = srec.ErrOverflowLimit
	ErrDanglingRef   = srec.ErrDanglingRef
)

// Store 并发安全外壳：Get/OverflowBlocks/SelfCheck 可多 goroutine 并发。
type Store struct {
	mu sync.RWMutex
	tb *srec.Table
}

func New(T, maxOverflow int) (*Store, error) {
	tb, err := srec.New(T, maxOverflow)
	if err != nil {
		return nil, err
	}
	return &Store{tb: tb}, nil
}

func (s *Store) Put(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tb.Put(k, v)
}

func (s *Store) Get(k string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tb.Get(k)
}

func (s *Store) Del(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tb.Del(k)
}

func (s *Store) Recover() (int, []string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tb.Recover()
}

func (s *Store) OverflowBlocks() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tb.OverflowBlocks()
}

// SelfCheck 在独立内部实例上跑内置操作序列，核验四条不变量。
// 不触碰接收者状态，可并发调用。
func (s *Store) SelfCheck() error {
	tb, err := srec.New(4, 8)
	if err != nil {
		return err
	}
	// 不变量1/3：内联→溢出→更新→回收，逐步核对溢出块数与引用完整
	steps := []struct {
		del bool
		k   string
		v   []byte
		n   int
	}{
		{false, "a", []byte("1234"), 0},
		{false, "b", []byte("12345"), 1},
		{false, "b", []byte("xy"), 0},
		{false, "c", []byte("12345"), 1},
		{false, "c", []byte("1234567890"), 1},
		{true, "b", nil, 1},
		{false, "a", []byte("1234567"), 2},
	}
	for i, st := range steps {
		if st.del {
			err = tb.Del(st.k)
		} else {
			err = tb.Put(st.k, st.v)
		}
		if err != nil || tb.OverflowBlocks() != st.n || tb.Check() != nil {
			return fmt.Errorf("selfcheck 步骤%d: n=%d err=%v", i+1, tb.OverflowBlocks(), err)
		}
	}
	if v, ok, _ := tb.Get("c"); !ok || string(v) != "1234567890" {
		return errors.New("selfcheck: Get(c) 与朴素参照不符")
	}
	// 不变量2：Recover 回收孤儿、检测悬挂
	tb.Raw().Blocks[99] = []byte("orphan")
	if n, keys, err := tb.Recover(); n != 1 || keys != nil || err != nil {
		return fmt.Errorf("selfcheck: 孤儿回收 n=%d keys=%v err=%v", n, keys, err)
	}
	tb.Raw().Ref["ghost"] = 77
	if _, keys, err := tb.Recover(); !errors.Is(err, ErrDanglingRef) || len(keys) != 1 || keys[0] != "ghost" {
		return fmt.Errorf("selfcheck: 悬挂检测 keys=%v err=%v", keys, err)
	}
	delete(tb.Raw().Ref, "ghost")
	// 不变量4：被拒操作不留痕
	before := tb.OverflowBlocks()
	if err := tb.Put("", []byte("12345")); !errors.Is(err, ErrEmptyKey) || tb.OverflowBlocks() != before {
		return errors.New("selfcheck: 拒绝后状态被改变")
	}
	return nil
}
