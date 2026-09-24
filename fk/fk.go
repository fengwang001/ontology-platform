// Package fk 四种操作的校验顺序、被拒判定与引用计数维护。依赖 sch。
// 所有拒绝分支都在任何写之前 return，保证失败不留痕。
package fk

import (
	"errors"
	"fmt"

	"ontology/sch"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrOrphan       = errors.New("fk: child insert rejected, parent does not exist")
	ErrParentInUse  = errors.New("fk: parent delete rejected, still referenced")
	ErrNoParent     = errors.New("fk: parent delete rejected, parent does not exist")
	ErrNoChild      = errors.New("fk: child delete rejected, child does not exist")
	ErrInconsistent = errors.New("fk: internal state inconsistent")
)

// PIns 父行不存在则插入；已存在则幂等成功。
func PIns(s *sch.State, pk string) error {
	if !s.HasParent(pk) {
		s.AddParent(pk)
	}
	return nil
}

// PDel 先查存在性（ErrNoParent），再查引用计数（ErrParentInUse），
// 两个拒绝分支都在写之前；通过才删除。
func PDel(s *sch.State, pk string) error {
	exists, inUse := s.ParentDeletable(pk)
	if !exists {
		return ErrNoParent
	}
	if inUse {
		return ErrParentInUse
	}
	s.DelParent(pk)
	return nil
}

// CIns 父行当前存在才插入，否则 ErrOrphan；被拒的子行不记忆。
// ck 已存在且指向同一父行时幂等成功。
func CIns(s *sch.State, ck, pk string) error {
	if !s.HasParent(pk) {
		return ErrOrphan
	}
	if !s.HasChild(ck) {
		s.AddChild(ck, pk)
	}
	return nil
}

// CDel 子行存在则删除，否则 ErrNoChild。
func CDel(s *sch.State, ck string) error {
	if !s.HasChild(ck) {
		return ErrNoChild
	}
	s.DelChild(ck)
	return nil
}

// Check 核对内部一致性：无悬空子行；每个父行的引用计数恰好等于
// 子表中指向它的行数；计数项不指向不存在的父行。
func Check(s *sch.State) error {
	want := map[string]int{}
	for ck, pk := range s.ChildMap() {
		if !s.HasParent(pk) {
			return fmt.Errorf("%w: child %s dangles to %s", ErrInconsistent, ck, pk)
		}
		want[pk]++
	}
	for pk, n := range s.RefMap() {
		if !s.HasParent(pk) {
			return fmt.Errorf("%w: refcount for missing parent %s", ErrInconsistent, pk)
		}
		if n != want[pk] {
			return fmt.Errorf("%w: refcount %s=%d want %d", ErrInconsistent, pk, n, want[pk])
		}
	}
	for pk := range s.ParentSet() {
		if s.RefCount(pk) != want[pk] {
			return fmt.Errorf("%w: refcount %s off", ErrInconsistent, pk)
		}
	}
	return nil
}
