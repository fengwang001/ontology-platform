// Package api 是在线 schema 迁移的对外接口。
package api

import (
	"fmt"
	"reflect"

	"ontology/ddl"
)

// 四类可判定哨兵错误（来自 ddl，互不相同）。
var (
	ErrPhase     = ddl.ErrPhase
	ErrKey       = ddl.ErrKey
	ErrValue     = ddl.ErrValue
	ErrDualWrite = ddl.ErrDualWrite
)

type Store struct{ s *ddl.Store }

func New(maxValue int) *Store              { return &Store{s: ddl.New(maxValue)} }
func (s *Store) Put(k string, v int) error { return s.s.Put(k, v) }
func (s *Store) Get(k string) int          { return s.s.Get(k) }
func (s *Store) BeginMigration() error     { return s.s.BeginMigration() }
func (s *Store) Backfill() error           { return s.s.Backfill() }
func (s *Store) Switch() error             { return s.s.Switch() }
func (s *Store) InjectDualWriteFault()     { s.s.InjectDualWriteFault() }

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
// 只使用内部新建实例，可并发调用。
func (s *Store) SelfCheck() error {
	if err := checkBatchEquivalent(); err != nil {
		return fmt.Errorf("selfcheck inv1: %w", err)
	}
	if err := checkDualWrite(); err != nil {
		return fmt.Errorf("selfcheck inv2: %w", err)
	}
	if err := checkBackfillIdempotent(); err != nil {
		return fmt.Errorf("selfcheck inv3: %w", err)
	}
	if err := checkFailureNoTrace(); err != nil {
		return fmt.Errorf("selfcheck inv4: %w", err)
	}
	return nil
}

// 不变量 1：九步序列后，Get 与按最终阶段从对应表取值的批量结果逐键相同。
func checkBatchEquivalent() error {
	st := ddl.New(1000)
	ops := []func() error{
		func() error { return st.Put("a", 3) }, func() error { return st.Put("b", 5) },
		st.BeginMigration, func() error { return st.Put("c", 4) },
		st.Backfill, st.Backfill, func() error { return st.Put("a", 7) },
		st.Switch, func() error { return st.Put("d", 2) },
	}
	for _, op := range ops {
		if err := op(); err != nil {
			return err
		}
	}
	_, _, v2 := st.Snapshot() // 终态 Switched：批量结果即 v2 表
	for k, want := range v2 {
		if got := st.Get(k); got != want {
			return fmt.Errorf("Get(%q)=%d, batch=%d", k, got, want)
		}
	}
	return nil
}

// 不变量 2：DualWrite 成功 Put 后 v2[k]==2*v1[k]；故障下两表同时不变。
func checkDualWrite() error {
	st := ddl.New(1000)
	st.Put("a", 3)
	st.BeginMigration()
	st.Put("b", 4)
	_, v1, v2 := st.Snapshot()
	if v2["b"] != 2*v1["b"] {
		return fmt.Errorf("v2[b]=%d != 2*v1[b]=%d", v2["b"], v1["b"])
	}
	st.InjectDualWriteFault()
	_, b1, b2 := st.Snapshot()
	if err := st.Put("c", 1); err != ErrDualWrite {
		return fmt.Errorf("fault Put err=%v", err)
	}
	_, a1, a2 := st.Snapshot()
	if !reflect.DeepEqual(b1, a1) || !reflect.DeepEqual(b2, a2) {
		return fmt.Errorf("fault changed state: v1 %v->%v v2 %v->%v", b1, a1, b2, a2)
	}
	return nil
}

// 不变量 3：Backfill 任意次，v2 恒等于 2*v1。
func checkBackfillIdempotent() error {
	st := ddl.New(1000)
	for i := 0; i < 5; i++ {
		st.Put(fmt.Sprintf("k%d", i), i+1)
	}
	st.BeginMigration()
	var prev map[string]int
	for round := 0; round < 3; round++ {
		st.Backfill()
		_, v1, v2 := st.Snapshot()
		for k, v := range v1 {
			if v2[k] != 2*v {
				return fmt.Errorf("round %d: v2[%q]=%d != 2*%d", round, k, v2[k], v)
			}
		}
		if prev != nil && !reflect.DeepEqual(prev, v2) {
			return fmt.Errorf("round %d changed v2", round)
		}
		prev = v2
	}
	return nil
}

// 不变量 4：被拒操作不改变任何状态。
func checkFailureNoTrace() error {
	st := ddl.New(1000)
	st.Put("a", 3)
	bad := []struct {
		op   func() error
		want error
	}{
		{st.Switch, ErrPhase},   // Switch 非 DualWrite
		{st.Backfill, ErrPhase}, // Backfill 非 DualWrite
		{func() error { return st.Put("", 1) }, ErrKey},
		{func() error { return st.Put("x", -1) }, ErrValue},
		{func() error { return st.Put("x", 600) }, ErrValue}, // 2*600>1000
	}
	for _, b := range bad {
		p0, v10, v20 := st.Snapshot()
		if err := b.op(); err != b.want {
			return fmt.Errorf("err=%v, want %v", err, b.want)
		}
		p1, v11, v21 := st.Snapshot()
		if p0 != p1 || !reflect.DeepEqual(v10, v11) || !reflect.DeepEqual(v20, v21) {
			return fmt.Errorf("rejected op changed state")
		}
	}
	st.BeginMigration() // 被拒后仍可正常使用
	return st.Put("b", 2)
}
