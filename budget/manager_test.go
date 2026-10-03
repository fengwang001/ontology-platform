package budget

import (
	"errors"
	"sync"
	"testing"
)

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	var re *RejectError
	if errors.As(err, &re) {
		return re.Reason
	}
	return "unknown"
}

func TestSpecExample(t *testing.T) {
	m := NewManager()
	must(t, m.Declare("X", 4))
	must(t, m.AddTask("X", Task{ID: "x1", C: 2, T: 10, D: 6}))
	if b, _ := m.Budget("X"); b != 2 {
		t.Fatalf("X theta=%d want 2", b)
	}
	if tot := m.Total(); tot != (Fraction{1, 2}) {
		t.Fatalf("total=%s want 1/2", tot)
	}

	must(t, m.Declare("Y", 6))
	must(t, m.AddTask("Y", Task{ID: "y1", C: 1, T: 12, D: 12}))
	if b, _ := m.Budget("Y"); b != 1 {
		t.Fatalf("Y theta=%d want 1", b)
	}
	if tot := m.Total(); tot != (Fraction{2, 3}) {
		t.Fatalf("total=%s want 2/3 (1/2+1/6)", tot)
	}

	must(t, m.AddTask("Y", Task{ID: "y2", C: 1, T: 12, D: 9}))
	if b, _ := m.Budget("Y"); b != 2 {
		t.Fatalf("Y theta=%d want 2", b)
	}
	if tot := m.Total(); tot != (Fraction{5, 6}) {
		t.Fatalf("total=%s want 5/6", tot)
	}

	// y3: theta'=4 => 1/2 + 4/6 = 7/6 > 1，过载拒绝，状态不变。
	err := m.AddTask("Y", Task{ID: "y3", C: 1, T: 12, D: 6})
	if reasonOf(err) != ReasonOverloaded {
		t.Fatalf("want overloaded, got %v", err)
	}
	if b, _ := m.Budget("Y"); b != 2 {
		t.Fatalf("rejected add must not change theta: %d", b)
	}
	if tot := m.Total(); tot != (Fraction{5, 6}) {
		t.Fatalf("rejected add must not change total: %s", tot)
	}

	// RemoveTask 不改 theta。
	must(t, m.RemoveTask("Y", "y2"))
	if b, _ := m.Budget("Y"); b != 2 {
		t.Fatalf("theta stays 2 after remove, got %d", b)
	}
	// Compact 后回到当前任务集 MinBudget = 1。
	must(t, m.Compact("Y"))
	if b, _ := m.Budget("Y"); b != 1 {
		t.Fatalf("theta after compact=%d want 1", b)
	}
	if tot := m.Total(); tot != (Fraction{2, 3}) {
		t.Fatalf("total after compact=%s want 2/3 (1/2+1/6)", tot)
	}
}

func TestBudgetOnlyIncreases(t *testing.T) {
	m := NewManager()
	must(t, m.Declare("C", 4))
	must(t, m.AddTask("C", Task{ID: "a", C: 2, T: 10, D: 5})) // theta=3
	b, _ := m.Budget("C")
	if b != 3 {
		t.Fatalf("theta=%d want 3", b)
	}
	// 加入“更便宜”的任务：MinBudget 可能仍为 3，theta 不下降。
	must(t, m.AddTask("C", Task{ID: "b", C: 1, T: 12, D: 12}))
	if b, _ = m.Budget("C"); b != 3 {
		t.Fatalf("theta must not decrease, got %d", b)
	}
	// 先删后加：删除 a 后 theta 仍 3，重新加 a 也不会使 theta 降低。
	must(t, m.RemoveTask("C", "a"))
	if b, _ = m.Budget("C"); b != 3 {
		t.Fatalf("theta stays 3 after remove, got %d", b)
	}
	must(t, m.AddTask("C", Task{ID: "a2", C: 2, T: 10, D: 6}))
	if b, _ = m.Budget("C"); b != 3 {
		t.Fatalf("theta still 3, got %d", b)
	}
}

func TestExactBandwidthBoundary(t *testing.T) {
	// 三个组件 Pi=6,4,12，预算 3,1,1：3/6+1/4+1/12 = 1/2+1/4+1/12 = 10/12 = 5/6
	// 再构造恰等于 1：组件 A Pi=2 theta=1；B Pi=3 theta=1；C Pi=6 theta=1
	//   1/2+1/3+1/6 = 1。
	m := NewManager()
	must(t, m.Declare("A", 2))
	must(t, m.Declare("B", 3))
	must(t, m.Declare("C", 6))
	// 直接放能产生 theta=1 的轻任务。
	must(t, m.AddTask("A", Task{ID: "a", C: 1, T: 1000, D: 1000}))
	must(t, m.AddTask("B", Task{ID: "b", C: 1, T: 1000, D: 1000}))
	must(t, m.AddTask("C", Task{ID: "c", C: 1, T: 1000, D: 1000}))
	if tot := m.Total(); tot != (Fraction{1, 1}) {
		t.Fatalf("total=%s want 1/1 (equality admitted)", tot)
	}
	// 再加任何需要抬预算的任务必须过载；用空 D 组件尝试抬升：
	must(t, m.Declare("D", 1000))
	err := m.AddTask("D", Task{ID: "d", C: 1, T: 1, D: 1}) // theta'=1 => +1/1000
	if reasonOf(err) != ReasonOverloaded {
		t.Fatalf("want overloaded beyond equality, got %v", err)
	}
}

func TestRejectionOrderAndInvariants(t *testing.T) {
	m := NewManager()
	// 参数非法优先于不存在。
	if reasonOf(m.Declare("", 4)) != ReasonInvalid {
		t.Fatal("empty name must be invalid")
	}
	if reasonOf(m.Declare("x", 0)) != ReasonInvalid {
		t.Fatal("pi=0 must be invalid")
	}
	if reasonOf(m.AddTask("nope", Task{ID: "", C: 1, T: 1, D: 1})) != ReasonInvalid {
		t.Fatal("invalid task precedes not-found")
	}
	if reasonOf(m.AddTask("nope", Task{ID: "z", C: 1, T: 1, D: 1})) != ReasonNotFound {
		t.Fatal("component missing must be not-found")
	}
	must(t, m.Declare("n", 2))
	if reasonOf(m.AddTask("n", Task{ID: "z", C: 2, T: 1, D: 1})) != ReasonInvalid {
		t.Fatal("C>D invalid")
	}
	// 容量：任务 8 个。
	for i := 0; i < 8; i++ {
		id := string(rune('0' + i))
		must(t, m.AddTask("n", Task{ID: id, C: 1, T: 1000, D: 1000}))
	}
	if reasonOf(m.AddTask("n", Task{ID: "n9", C: 1, T: 1000, D: 1000})) != ReasonCapacity {
		t.Fatal("task cap must be capacity_full")
	}
	// 重复编号先于容量。
	if reasonOf(m.AddTask("n", Task{ID: "0", C: 1, T: 1000, D: 1000})) != ReasonDuplicate {
		t.Fatal("duplicate id precedes capacity")
	}
	// 组件容量 8。
	for i := 0; i < 7; i++ {
		must(t, m.Declare(string(rune('A'+i)), 4))
	}
	if reasonOf(m.Declare("ninth", 4)) != ReasonCapacity {
		t.Fatal("component cap")
	}
	if reasonOf(m.Declare("n", 4)) != ReasonDuplicate {
		t.Fatal("duplicate component")
	}
	// 不存在系列。
	if reasonOf(m.RemoveTask("nope", "x")) != ReasonNotFound {
		t.Fatal("remove missing component")
	}
	if reasonOf(m.RemoveTask("n", "ghost")) != ReasonNotFound {
		t.Fatal("remove missing task")
	}
	if reasonOf(m.Compact("nope")) != ReasonNotFound {
		t.Fatal("compact missing component")
	}
	if _, err := m.Budget("nope"); reasonOf(err) != ReasonNotFound {
		t.Fatal("budget missing component")
	}
}

func TestTooLargeSizeGuard(t *testing.T) {
	m := NewManager()
	must(t, m.Declare("B", 1000))
	// T 两两互质：991, 997 与 Pi=1000 的 LCM 远超 1e6（在乘法中途即停止）。
	err := m.AddTask("B", Task{ID: "a", C: 1, T: 997, D: 997})
	if err != nil {
		t.Fatalf("first task fine: %v", err)
	}
	err = m.AddTask("B", Task{ID: "b", C: 1, T: 991, D: 991})
	if reasonOf(err) != ReasonTooLarge {
		t.Fatalf("want too_large, got %v", err)
	}
	// 状态不变（首个任务因 D=997 需 theta=502：sbf 在 t=997 处的要求）。
	if b, _ := m.Budget("B"); b != 502 {
		t.Fatalf("state unchanged after reject, theta=%d want 502", b)
	}
}

func TestInfeasibleReasonCarriesPoint(t *testing.T) {
	m := NewManager()
	must(t, m.Declare("P", 4))
	// 利用率 >1：两个 T=2 的任务 C=2 和 C=1 => ViolateT=0。
	must(t, m.AddTask("P", Task{ID: "a", C: 2, T: 2, D: 2}))
	err := m.AddTask("P", Task{ID: "b", C: 1, T: 2, D: 2})
	// 可能先触发过载；用独占组件隔离。
	_ = err
	m2 := NewManager()
	must(t, m2.Declare("Q", 2))
	must(t, m2.AddTask("Q", Task{ID: "a", C: 2, T: 2, D: 2}))
	err = m2.AddTask("Q", Task{ID: "b", C: 1, T: 2, D: 2})
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("want reject, got %v", err)
	}
	if re.Reason != ReasonInfeasible || re.ViolateT != 0 {
		t.Fatalf("want infeasible t=0, got %s t=%d", re.Reason, re.ViolateT)
	}
}

func TestDeterministicReplay(t *testing.T) {
	ops := func(m *Manager) []error {
		es := []error{
			m.Declare("X", 4),
			m.AddTask("X", Task{ID: "x1", C: 2, T: 10, D: 6}),
			m.Declare("Y", 6),
			m.AddTask("Y", Task{ID: "y1", C: 1, T: 12, D: 12}),
			m.AddTask("Y", Task{ID: "y2", C: 1, T: 12, D: 9}),
			m.AddTask("Y", Task{ID: "y3", C: 1, T: 12, D: 6}),
			m.RemoveTask("Y", "y2"),
			m.Compact("Y"),
		}
		return es
	}
	m1, m2 := NewManager(), NewManager()
	e1, e2 := ops(m1), ops(m2)
	for i := range e1 {
		if reasonOf(e1[i]) != reasonOf(e2[i]) {
			t.Fatalf("op %d reasons differ", i)
		}
	}
	if m1.Total() != m2.Total() {
		t.Fatalf("totals differ: %s vs %s", m1.Total(), m2.Total())
	}
	for _, name := range []string{"X", "Y"} {
		b1, _ := m1.Budget(name)
		b2, _ := m2.Budget(name)
		if b1 != b2 {
			t.Fatalf("budget %s differs: %d vs %d", name, b1, b2)
		}
	}
}

func TestConcurrentCalls(t *testing.T) {
	m := NewManager()
	must(t, m.Declare("K", 8))
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := string(rune('a' + w))
				_ = m.AddTask("K", Task{ID: id, C: 1, T: 1000, D: 1000})
				_ = m.Total()
				_, _ = m.Budget("K")
			}
		}(w)
	}
	wg.Wait()
	// 最多 8 个任务且总带宽 <= 1。
	if tot := m.Total(); tot.Num > tot.Den {
		t.Fatalf("bandwidth exceeded under concurrency: %s", tot)
	}
	snap, _ := m.Snapshot("K")
	if len(snap.Tasks) > 8 {
		t.Fatalf("task cap violated: %d", len(snap.Tasks))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
