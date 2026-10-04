package budget

import (
	"math/big"
	"testing"
)

func mustReason(t *testing.T, err error, want RejectReason) {
	t.Helper()
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("err=%v, want RejectError reason=%v", err, want)
	}
	if re.Reason != want {
		t.Fatalf("reason=%v (%s), want %v", re.Reason, re.Detail, want)
	}
}

func mustBudget(t *testing.T, m *Manager, name string, want int64) {
	t.Helper()
	got, err := m.Budget(name)
	if err != nil {
		t.Fatalf("Budget(%q): %v", name, err)
	}
	if got != want {
		t.Fatalf("Budget(%q)=%d, want %d", name, got, want)
	}
}

func mustTotal(t *testing.T, m *Manager, num, den int64) {
	t.Helper()
	if got := m.Total(); got.Cmp(big.NewRat(num, den)) != 0 {
		t.Fatalf("Total()=%v, want %d/%d", got, num, den)
	}
}

// 规格中的完整示例序列。
func TestSpecExample(t *testing.T) {
	m := NewManager()
	if err := m.Declare("X", 4); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("X", Task{ID: "x1", C: 2, T: 10, D: 6}); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "X", 2)

	if err := m.Declare("Y", 6); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("Y", Task{ID: "y1", C: 1, T: 12, D: 12}); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "Y", 1)
	mustTotal(t, m, 2, 3) // 1/2 + 1/6

	if err := m.AddTask("Y", Task{ID: "y2", C: 1, T: 12, D: 9}); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "Y", 2)
	mustTotal(t, m, 5, 6) // 1/2 + 2/6

	// y3 使 MinBudget=4，Σ=1/2+4/6=7/6>1，过载拒绝。
	err := m.AddTask("Y", Task{ID: "y3", C: 1, T: 12, D: 6})
	mustReason(t, err, RejectOverload)
	if re := err.(*RejectError); re.Theta != 4 {
		t.Fatalf("rejected theta=%d, want 4", re.Theta)
	}
	// 拒绝后状态不变。
	mustBudget(t, m, "Y", 2)
	mustTotal(t, m, 5, 6)

	// RemoveTask 不改 θ。
	if err := m.RemoveTask("Y", "y2"); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "Y", 2)
	mustTotal(t, m, 5, 6)

	// Compact 后 θ 降为当前任务集的 MinBudget=1。
	if err := m.Compact("Y"); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "Y", 1)
	mustTotal(t, m, 2, 3)
}

func TestThetaMonotoneUntilCompact(t *testing.T) {
	m := NewManager()
	if err := m.Declare("C", 10); err != nil {
		t.Fatal(err)
	}
	// 先加需求高的任务把 θ 顶上去。
	if err := m.AddTask("C", Task{ID: "big", C: 3, T: 10, D: 3}); err != nil {
		t.Fatal(err)
	}
	hi, _ := m.Budget("C")
	if hi < 2 {
		t.Fatalf("theta=%d, expected >=2", hi)
	}
	// 删除后再加需求很低的任务：MinBudget 低于现有 θ，θ 保持不变。
	if err := m.RemoveTask("C", "big"); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("C", Task{ID: "small", C: 1, T: 100, D: 100}); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "C", hi)
	// Compact 后 θ 下降。
	if err := m.Compact("C"); err != nil {
		t.Fatal(err)
	}
	lo, _ := m.Budget("C")
	if lo >= hi {
		t.Fatalf("after Compact theta=%d, want < %d", lo, hi)
	}
	// 无任务时 Compact 归零。
	if err := m.RemoveTask("C", "small"); err != nil {
		t.Fatal(err)
	}
	if err := m.Compact("C"); err != nil {
		t.Fatal(err)
	}
	mustBudget(t, m, "C", 0)
}

func TestTotalExactlyOnePasses(t *testing.T) {
	m := NewManager()
	// 1/2 + 1/6 + 1/3 = 1，恰等于 1 必须通过。
	if err := m.Declare("X", 4); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("X", Task{ID: "x1", C: 2, T: 10, D: 6}); err != nil {
		t.Fatal(err)
	}
	if err := m.Declare("Y", 6); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("Y", Task{ID: "y1", C: 1, T: 12, D: 12}); err != nil {
		t.Fatal(err)
	}
	if err := m.Declare("Z", 3); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("Z", Task{ID: "z1", C: 1, T: 12, D: 12}); err != nil {
		t.Fatal(err)
	}
	mustTotal(t, m, 1, 1)
	// 再加任何正预算都会超过 1，必须拒绝且状态不变。
	if err := m.Declare("W", 2); err != nil {
		t.Fatal(err)
	}
	err := m.AddTask("W", Task{ID: "w1", C: 1, T: 12, D: 12})
	mustReason(t, err, RejectOverload)
	mustBudget(t, m, "W", 0)
	mustTotal(t, m, 1, 1)
}

func TestTooLargeRejectedBeforeFeasibility(t *testing.T) {
	m := NewManager()
	if err := m.Declare("B", 1000); err != nil {
		t.Fatal(err)
	}
	// lcm(1000,997,991) 远超 10^6：规模过大。
	// 同时 ΣC/T>1 也成立，但必须先报规模过大。
	err := m.AddTask("B", Task{ID: "b1", C: 997, T: 997, D: 997})
	if err != nil {
		t.Fatal(err)
	}
	err = m.AddTask("B", Task{ID: "b2", C: 991, T: 991, D: 991})
	mustReason(t, err, RejectTooLarge)
	mustBudget(t, m, "B", 1000) // b1 利用率为 1，θ=Π=1000 保留
}

func TestRejectReasonOrdering(t *testing.T) {
	m := NewManager()
	// 参数非法优先于不存在。
	mustReason(t, m.AddTask("ghost", Task{ID: "", C: 1, T: 1, D: 1}), RejectInvalidParam)
	mustReason(t, m.AddTask("", Task{ID: "t", C: 1, T: 1, D: 1}), RejectInvalidParam)
	mustReason(t, m.Declare("", 4), RejectInvalidParam)
	mustReason(t, m.Declare("ok", 0), RejectInvalidParam)
	mustReason(t, m.Declare("ok", 1001), RejectInvalidParam)
	// 不存在。
	mustReason(t, m.AddTask("ghost", Task{ID: "t", C: 1, T: 1, D: 1}), RejectNotFound)
	mustReason(t, m.RemoveTask("ghost", "t"), RejectNotFound)
	mustReason(t, m.Compact("ghost"), RejectNotFound)
	_, err := m.Budget("ghost")
	mustReason(t, err, RejectNotFound)
	// 重复。
	if err := m.Declare("A", 4); err != nil {
		t.Fatal(err)
	}
	mustReason(t, m.Declare("A", 4), RejectDuplicate)
	if err := m.AddTask("A", Task{ID: "a1", C: 1, T: 10, D: 10}); err != nil {
		t.Fatal(err)
	}
	mustReason(t, m.AddTask("A", Task{ID: "a1", C: 1, T: 5, D: 5}), RejectDuplicate)
	mustReason(t, m.RemoveTask("A", "nope"), RejectNotFound)
	// 任务编号只在组件内唯一：另一组件可用同编号。
	if err := m.Declare("B", 4); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("B", Task{ID: "a1", C: 1, T: 10, D: 10}); err != nil {
		t.Fatal(err)
	}
}

func TestCapacityLimits(t *testing.T) {
	m := NewManager()
	for i := 0; i < MaxComponents; i++ {
		if err := m.Declare(string(rune('A'+i)), 1000); err != nil {
			t.Fatal(err)
		}
	}
	mustReason(t, m.Declare("I", 1), RejectCapacity)

	small := NewManager()
	if err := small.Declare("A", 1000); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxTasksPerComponent; i++ {
		if err := small.AddTask("A", Task{ID: string(rune('a' + i)), C: 1, T: 1000, D: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	// 组件已满：新编号报容量已满；重复编号仍报重复（重复优先于容量）。
	mustReason(t, small.AddTask("A", Task{ID: "z", C: 1, T: 1000, D: 1000}), RejectCapacity)
	mustReason(t, small.AddTask("A", Task{ID: "a", C: 1, T: 1000, D: 1000}), RejectDuplicate)
}

func TestInfeasibleRejected(t *testing.T) {
	m := NewManager()
	if err := m.Declare("A", 4); err != nil {
		t.Fatal(err)
	}
	// 第一个任务可行（θ=4，Σ=1）。
	if err := m.AddTask("A", Task{ID: "a", C: 4, T: 5, D: 5}); err != nil {
		t.Fatal(err)
	}
	// 加入后 ΣC/T=7/5>1：不可行且 violationT=0（不可行优先于过载）。
	err := m.AddTask("A", Task{ID: "b", C: 3, T: 5, D: 5})
	mustReason(t, err, RejectInfeasible)
	if re := err.(*RejectError); re.ViolationT != 0 || re.Theta != 4 {
		t.Fatalf("violationT=%d theta=%d, want 0/4", re.ViolationT, re.Theta)
	}
	// 拒绝不留下任何任务或 θ 变化。
	mustBudget(t, m, "A", 4)
	mustTotal(t, m, 1, 1)
	if err := m.RemoveTask("A", "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Compact("A"); err != nil {
		t.Fatal(err)
	}
	// 利用率合法但 Θ=Π 下 dbf 违反：violationT 为最小违反点。
	// 两个 (C=1,T=3,D=2) 可行（θ=4）；第三个使 dbf(2)=3>sbf(2)=2。
	err = m.AddTask("A", Task{ID: "b", C: 1, T: 3, D: 2})
	if err != nil {
		t.Fatal(err) // 单任务可行
	}
	err = m.AddTask("A", Task{ID: "c", C: 1, T: 3, D: 2})
	if err != nil {
		t.Fatal(err)
	}
	err = m.AddTask("A", Task{ID: "d", C: 1, T: 3, D: 2})
	mustReason(t, err, RejectInfeasible)
	if re := err.(*RejectError); re.ViolationT != 2 {
		t.Fatalf("violationT=%d, want 2", re.ViolationT)
	}
	mustBudget(t, m, "A", 4) // 前两个任务的 θ 保留
}

func TestRejectedAddLeavesNoPartialState(t *testing.T) {
	m := NewManager()
	if err := m.Declare("A", 4); err != nil {
		t.Fatal(err)
	}
	if err := m.AddTask("A", Task{ID: "a", C: 2, T: 10, D: 6}); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Budget("A")
	totalBefore := m.Total()
	// 触发各种拒绝后状态不变。
	_ = m.AddTask("A", Task{ID: "a", C: 1, T: 1, D: 1})       // duplicate
	_ = m.AddTask("A", Task{ID: "x", C: 100, T: 100, D: 100}) // infeasible
	_ = m.AddTask("ZZ", Task{ID: "x", C: 1, T: 1, D: 1})      // not found
	after, _ := m.Budget("A")
	if before != after || m.Total().Cmp(totalBefore) != 0 {
		t.Fatalf("state changed after rejected ops: theta %d->%d", before, after)
	}
}
