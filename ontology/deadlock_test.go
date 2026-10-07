package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 建立通过链接类型关联的实例集合。
func newLinkedStore(t *testing.T, ids ...InstanceID) *Store {
	t.Helper()
	s := newTestStore(t)
	for _, id := range ids {
		mustCreate(t, s, id, map[string]PropertyValue{"title": string(id)})
	}
	for i := 0; i+1 < len(ids); i++ {
		s.AddLink(Link{Type: "depends", From: ids[i], To: ids[i+1]})
	}
	return s
}

// 两个动作按相反顺序申请两个关联实例：违反升序规则的一方必然被确定性拒绝，
// 不会出现双方各持一部分、互相等待的局面。
func TestCrossInstanceOppositeOrderDeterministic(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		s := newLinkedStore(t, "I1", "I2")
		// 显式编排交错：A 先持 I1，B 再持 I2，随后双方各申请对方持有的实例。
		occA, err := s.Acquire("A", "I1")
		if err != nil {
			t.Fatalf("trial %d: A acquire I1: %v", trial, err)
		}
		occB, err := s.Acquire("B", "I2")
		if err != nil {
			t.Fatalf("trial %d: B acquire I2: %v", trial, err)
		}
		// A 申请 I2：被 B 占用，立即拒绝（不阻塞）。
		if _, err := s.Acquire("A", "I2"); rejectCode(t, err) != RejectOccupiedByAction {
			t.Fatalf("trial %d: A acquire I2 should be occupied, got %v", trial, err)
		}
		// B 申请 I1：违反升序规则，确定性拒绝——谁被拒绝可预先推导。
		if _, err := s.Acquire("B", "I1"); rejectCode(t, err) != RejectOrderConflict {
			t.Fatalf("trial %d: B acquire I1 should be order conflict, got %v", trial, err)
		}
		// 双方都能立即继续：A 释放后 B 仍受顺序规则约束，只能先释放 I2 再按序申请。
		occA.Abort()
		occB.Abort()
		if _, err := s.Acquire("B", "I1"); err != nil {
			t.Fatalf("trial %d: B re-acquire I1 after release: %v", trial, err)
		}
	}
}

// 穷举 3 个实例的全部申请顺序排列，验证每种排列的结果都是确定的：
// 同一排列重复执行多次，拒绝分布完全一致。
func TestCrossInstanceAllPermutationsDeterministic(t *testing.T) {
	ids := []InstanceID{"I1", "I2", "I3"}
	perms := [][]InstanceID{}
	var gen func(prefix []InstanceID, rest []InstanceID)
	gen = func(prefix, rest []InstanceID) {
		if len(rest) == 0 {
			perms = append(perms, append([]InstanceID(nil), prefix...))
			return
		}
		for i := range rest {
			next := append([]InstanceID(nil), rest[:i]...)
			next = append(next, rest[i+1:]...)
			gen(append(prefix, rest[i]), next)
		}
	}
	gen(nil, ids)

	signature := func(perm []InstanceID) string {
		s := newLinkedStore(t, ids...)
		action := ActionID("ACT")
		sig := ""
		occs := map[InstanceID]*Occupancy{}
		for _, id := range perm {
			occ, err := s.Acquire(action, id)
			if err != nil {
				sig += "R:" + rejectCode(t, err).String() + " "
				continue
			}
			occs[id] = occ
			sig += "G "
		}
		for _, occ := range occs {
			occ.Abort()
		}
		return sig
	}

	for _, perm := range perms {
		want := signature(perm)
		for repeat := 0; repeat < 5; repeat++ {
			if got := signature(perm); got != want {
				t.Fatalf("perm %v nondeterministic: %q vs %q", perm, want, got)
			}
		}
		t.Logf("perm %v -> %s", perm, want)
	}
}

// 多个动作并发申请同一批关联实例：所有申请都立即返回（不阻塞），
// 且不存在循环持有导致的死锁（否则竞态测试将超时失败）。
func TestConcurrentAcquireNeverBlocks(t *testing.T) {
	s := newLinkedStore(t, "I1", "I2", "I3", "I4")
	ids := []InstanceID{"I1", "I2", "I3", "I4"}

	var wg sync.WaitGroup
	for a := 0; a < 8; a++ {
		wg.Add(1)
		go func(a int) {
			defer wg.Done()
			action := ActionID(fmt.Sprintf("A%d", a))
			// 每个动作按各自（可能乱序）的顺序尝试申请全部实例。
			order := append([]InstanceID(nil), ids[a%len(ids):]...)
			order = append(order, ids[:a%len(ids)]...)
			held := []*Occupancy{}
			for _, id := range order {
				occ, err := s.Acquire(action, id)
				if err == nil {
					held = append(held, occ)
				}
			}
			for _, occ := range held {
				occ.Abort()
			}
		}(a)
	}
	wg.Wait()
}
