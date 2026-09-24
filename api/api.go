// Package api 是在线 schema 迁移的对外接口。
package api

import (
	"errors"
	"fmt"

	"ontology/ddl"
)

// 对外暴露的四类可判定哨兵错误，互不相同。
var (
	ErrBadPhase       = ddl.ErrBadPhase
	ErrEmptyKey       = ddl.ErrEmptyKey
	ErrBadValue       = ddl.ErrBadValue
	ErrDualWriteFault = ddl.ErrDualWriteFault
)

// Store 是对外句柄，并发安全。
type Store struct{ s *ddl.Store }

// New 建存储，maxValue 为 2*v 的上限。
func New(maxValue int) *Store              { return &Store{s: ddl.New(maxValue)} }
func (s *Store) Put(k string, v int) error { return s.s.Put(k, v) }
func (s *Store) Get(k string) int          { return s.s.Get(k) }
func (s *Store) BeginMigration() error     { return s.s.BeginMigration() }
func (s *Store) Backfill() error           { return s.s.Backfill() }
func (s *Store) Switch() error             { return s.s.Switch() }
func (s *Store) InjectDualWriteFault()     { s.s.InjectDualWriteFault() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (s *Store) SelfCheck() error {
	for i, chk := range []func() error{checkBatchModel, checkDualWrite, checkIdempotent, checkNoTrace} {
		if err := chk(); err != nil {
			return fmt.Errorf("selfcheck invariant %d: %w", i+1, err)
		}
	}
	return nil
}

// 不变量1：任意序列后 Get 等于「按最终阶段从对应表取值」的朴素批量结果。
// 序列终点恒为 Switched，故每键期望 2*最后写入值。
func checkBatchModel() error {
	s := New(1 << 30)
	model := map[string]int{}
	for i := 0; i < 200; i++ {
		k, v := string(rune('a'+i%26)), i%9+1
		switch i {
		case 60:
			_ = s.BeginMigration()
			_ = s.Backfill()
		case 120:
			_ = s.Switch()
		default:
			_ = s.Put(k, v)
			model[k] = v
		}
	}
	for k, v := range model {
		if got := s.Get(k); got != 2*v {
			return fmt.Errorf("Get(%q)=%d, want %d", k, got, 2*v)
		}
	}
	return nil
}

// 不变量2：DualWrite 成功 Put 后 v2[k]==2*v1[k]；故障下 v1、v2 同时不变。
func checkDualWrite() error {
	s := New(100)
	_ = s.Put("x", 3)
	_ = s.BeginMigration()
	_ = s.Put("y", 4)
	_ = s.Backfill()
	_ = s.Switch()
	if s.Get("y") != 8 || s.Get("x") != 6 {
		return errors.New("v2 != 2*v1 after switch")
	}
	f := New(100)
	_ = f.BeginMigration()
	f.InjectDualWriteFault()
	if err := f.Put("z", 1); !errors.Is(err, ErrDualWriteFault) {
		return errors.New("fault put not rejected")
	}
	_ = f.Switch()
	if f.Get("z") != 0 {
		return errors.New("fault put leaked into tables")
	}
	return nil
}

// 不变量3：Backfill 任意次结果不变。
func checkIdempotent() error {
	s := New(100)
	_ = s.Put("a", 3)
	_ = s.Put("b", 5)
	_ = s.BeginMigration()
	for i := 0; i < 5; i++ {
		_ = s.Backfill()
	}
	_ = s.Switch()
	if s.Get("a") != 6 || s.Get("b") != 10 {
		return errors.New("backfill not idempotent")
	}
	return nil
}

// 不变量4：被拒操作（阶段/键/值/故障）不改变任何状态，且之后仍可正常使用。
func checkNoTrace() error {
	s := New(10)
	_ = s.Put("k", 2)
	rejects := []struct {
		op   func() error
		want error
	}{
		{s.Switch, ErrBadPhase},
		{func() error { return s.Put("", 1) }, ErrEmptyKey},
		{func() error { return s.Put("k", -1) }, ErrBadValue},
		{func() error { return s.Put("k", 6) }, ErrBadValue},
		{s.Backfill, ErrBadPhase},
	}
	for _, r := range rejects {
		if err := r.op(); !errors.Is(err, r.want) {
			return errors.New("rejection not detected")
		}
	}
	if s.Get("k") != 2 {
		return errors.New("rejected op changed state")
	}
	if err := s.Put("k", 5); err != nil || s.Get("k") != 5 {
		return errors.New("store unusable after rejections")
	}
	return nil
}
