package cut_test

import (
	"errors"
	"testing"

	"ontology/cut"
	"ontology/seq"
)

func mustNew(t *testing.T, k int, m int64) *seq.Store {
	t.Helper()
	st, err := seq.New(k, m)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", k, m, err)
	}
	return st
}

func mustIssue(t *testing.T, st *seq.Store, sys int, want int64) {
	t.Helper()
	id, err := st.Issue(sys)
	if err != nil || id != want {
		t.Fatalf("Issue(%d)=%d,%v, want %d", sys, id, err, want)
	}
}

func mustJoin(t *testing.T, st *seq.Store, sys int) {
	t.Helper()
	if err := cut.Join(st, 2, sys); err != nil {
		t.Fatalf("Join(%d): %v", sys, err)
	}
}

func mustLeave(t *testing.T, st *seq.Store, sys int) {
	t.Helper()
	if err := cut.Leave(st, 2, sys); err != nil {
		t.Fatalf("Leave(%d): %v", sys, err)
	}
}

// 例一：K=3、m=4 的完整走查。
func TestExample1Walkthrough(t *testing.T) {
	st := mustNew(t, 3, 4)
	mustIssue(t, st, 1, 1)
	mustIssue(t, st, 1, 2)
	mustIssue(t, st, 1, 3)
	if got := st.NextOf(1); got != 4 {
		t.Fatalf("next_1=%d, want 4", got)
	}

	mustJoin(t, st, 2)
	if got := st.NextOf(1); got != 4 {
		t.Fatalf("Join 后 next_1=%d, want 4（已对齐，增量 0）", got)
	}
	if got := st.NextOf(2); got != 5 {
		t.Fatalf("Join 后 next_2=%d, want 5", got)
	}
	if got := st.J(); got != 4 {
		t.Fatalf("J=%d, want 4", got)
	}

	mustIssue(t, st, 1, 4)
	mustIssue(t, st, 2, 5)
	mustIssue(t, st, 1, 8)
	mustIssue(t, st, 2, 9)
	if st.NextOf(1) != 12 || st.NextOf(2) != 13 {
		t.Fatalf("next_1=%d next_2=%d, want 12,13", st.NextOf(1), st.NextOf(2))
	}

	mustJoin(t, st, 3)
	if got := st.NextOf(3); got != 14 {
		t.Fatalf("Join 后 next_3=%d, want 14", got)
	}
	if got := st.J(); got != 17 {
		t.Fatalf("J=%d, want 17", got)
	}
	mustIssue(t, st, 3, 14)
	if got := st.NextOf(3); got != 18 {
		t.Fatalf("next_3=%d, want 18", got)
	}

	mustLeave(t, st, 3)
	if got := st.Stride(); got != 4 {
		t.Fatalf("Leave(3) 后步长=%d, want 4", got)
	}

	mustLeave(t, st, 1)
	if got := st.NextOf(2); got != 18 {
		t.Fatalf("Leave(1) 后 next_2=%d, want 18（含离开者的高水位）", got)
	}
	if got := st.J(); got != 22 {
		t.Fatalf("J=%d, want 22", got)
	}
	mustIssue(t, st, 2, 18)
	mustIssue(t, st, 2, 19)
	if got := st.Stride(); got != 1 {
		t.Fatalf("步长=%d, want 1", got)
	}
}

// Join 计算 next_s 用的是对齐 t 之前取定的 H：
// 若 H 在对齐后取，next_2 会被推到更大的值。
func TestJoinUsesPreAlignHighWater(t *testing.T) {
	st := mustNew(t, 2, 4)
	mustIssue(t, st, 1, 1)
	mustIssue(t, st, 1, 2) // next_1=3
	mustJoin(t, st, 2)
	// 对齐前 H=3；next_2=alignUp(max(1,3),1)=5。
	// 若误用对齐后的 H=4，next_2 仍为 5；改用更能区分的场景：
	if got := st.NextOf(2); got != 5 {
		t.Fatalf("next_2=%d, want 5", got)
	}

	// 更严格的区分：next_1=5（对齐后变 8），H 应取 5 而非 8。
	st2 := mustNew(t, 2, 4)
	for i := 0; i < 4; i++ {
		if _, err := st2.Issue(1); err != nil {
			t.Fatal(err)
		}
	}
	// next_1=5，Join 时 t 对齐到 8；next_2 应用对齐前的 H=5 → alignUp(5,1)=5。
	mustJoin(t, st2, 2)
	if got := st2.NextOf(1); got != 8 {
		t.Fatalf("next_1=%d, want 8", got)
	}
	if got := st2.NextOf(2); got != 5 {
		t.Fatalf("next_2=%d, want 5（H 取对齐前的 5 而非 8）", got)
	}
}

// H 来自不活跃系统：离开者保留了最大的 next，重新加入者以它为起点。
func TestHighWaterFromInactiveSystem(t *testing.T) {
	st := mustNew(t, 4, 4)
	mustJoin(t, st, 2)
	mustJoin(t, st, 3)
	mustJoin(t, st, 4)
	// 此时 next = (4,1,6,7)，J=3+5+6=14。
	if got := st.J(); got != 14 {
		t.Fatalf("J=%d, want 14", got)
	}
	mustIssue(t, st, 4, 7)
	mustIssue(t, st, 4, 11)
	mustIssue(t, st, 4, 15) // next_4=19
	mustLeave(t, st, 4)     // 不活跃的系统 4 持有全局最大 next=19
	mustLeave(t, st, 3)     // 活跃={1,2}，next_3=6 原样保留
	// 系统 3 重新加入：H=19 来自不活跃的系统 4。
	mustJoin(t, st, 3)
	if got := st.NextOf(3); got != 22 {
		t.Fatalf("next_3=%d, want alignUp(19,2)=22（H 来自不活跃系统 4）", got)
	}
	if got := st.J(); got != 30 {
		t.Fatalf("J=%d, want 30", got)
	}
}

// 离开再重新加入：next 从全局高水位接续，不回退。
func TestLeaveAndRejoin(t *testing.T) {
	st := mustNew(t, 2, 4)
	mustJoin(t, st, 2)
	mustIssue(t, st, 1, 4)
	mustIssue(t, st, 2, 1)
	mustIssue(t, st, 2, 5) // next_2=9
	mustLeave(t, st, 2)
	if got := st.NextOf(2); got != 9 {
		t.Fatalf("离开后 next_2=%d, want 9（原样保留）", got)
	}
	// H'=max(8,9)=9，系统 1 从 9 起接续。
	mustIssue(t, st, 1, 9)
	mustIssue(t, st, 1, 10) // next_1=11
	mustIssue(t, st, 1, 11) // next_1=12
	mustJoin(t, st, 2)
	// H=12（对齐前），next_1 对齐到 12（增量 0）；next_2=alignUp(max(9,12),1)=13。
	if got := st.NextOf(1); got != 12 {
		t.Fatalf("next_1=%d, want 12", got)
	}
	if got := st.NextOf(2); got != 13 {
		t.Fatalf("next_2=%d, want 13", got)
	}
	mustIssue(t, st, 2, 13)
}

// 活跃数 1→2→1 往返：步长与起点正确切换。
func TestActiveCountRoundTrip(t *testing.T) {
	st := mustNew(t, 2, 4)
	if got := st.Stride(); got != 1 {
		t.Fatalf("初始步长=%d, want 1", got)
	}
	mustJoin(t, st, 2)
	if got := st.Stride(); got != 4 {
		t.Fatalf("Join 后步长=%d, want 4", got)
	}
	mustIssue(t, st, 1, 4)
	mustIssue(t, st, 2, 1) // next_2=5
	mustLeave(t, st, 2)
	if got := st.Stride(); got != 1 {
		t.Fatalf("Leave 后步长=%d, want 1", got)
	}
	// 单系统起点含离开者：H'=max(8,5)=8。
	mustIssue(t, st, 1, 8)
	mustIssue(t, st, 1, 9)
}

func TestJoinLeaveRejects(t *testing.T) {
	st := mustNew(t, 3, 4)

	if err := cut.Join(st, 2, 0); !errors.Is(err, seq.ErrParam) {
		t.Errorf("Join sys=0: %v, want ErrParam", err)
	}
	if err := cut.Join(st, 2, 4); !errors.Is(err, seq.ErrParam) {
		t.Errorf("Join sys=4: %v, want ErrParam", err)
	}
	if err := cut.Join(st, 0, 2); !errors.Is(err, seq.ErrParam) {
		t.Errorf("Join role=0: %v, want ErrParam", err)
	}
	if err := cut.Join(st, 3, 2); !errors.Is(err, seq.ErrParam) {
		t.Errorf("Join role=3: %v, want ErrParam", err)
	}
	if err := cut.Join(st, 1, 2); !errors.Is(err, seq.ErrPermission) {
		t.Errorf("Join role=1: %v, want ErrPermission", err)
	}
	if err := cut.Join(st, 2, 1); !errors.Is(err, seq.ErrState) {
		t.Errorf("Join 已活跃系统: %v, want ErrState", err)
	}
	if err := cut.Leave(st, 2, 2); !errors.Is(err, seq.ErrState) {
		t.Errorf("Leave 不活跃系统: %v, want ErrState", err)
	}
	if err := cut.Leave(st, 2, 1); !errors.Is(err, seq.ErrLast) {
		t.Errorf("Leave 最后活跃系统: %v, want ErrLast", err)
	}
	if err := cut.Leave(st, 1, 1); !errors.Is(err, seq.ErrPermission) {
		t.Errorf("Leave role=1: %v, want ErrPermission", err)
	}

	// 拒绝次序：sys 越界优先于 role 非管理员。
	if err := cut.Join(st, 1, 99); !errors.Is(err, seq.ErrParam) {
		t.Errorf("Join(sys 越界, role=1): %v, want ErrParam 优先", err)
	}

	// 全部被拒后状态不变。
	if st.NextOf(1) != 1 || st.NextOf(2) != 1 || st.NextOf(3) != 1 {
		t.Fatalf("被拒操作改变了 next: %d,%d,%d",
			st.NextOf(1), st.NextOf(2), st.NextOf(3))
	}
	if !st.Active(1) || st.Active(2) || st.Active(3) {
		t.Fatalf("被拒操作改变了活跃标志")
	}
	if got := st.J(); got != 0 {
		t.Fatalf("被拒操作改变了 J=%d", got)
	}
}
