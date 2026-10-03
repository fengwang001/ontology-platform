package cut

import (
	"reflect"
	"testing"
)

func newState(K int, m int64) *State {
	st := &State{
		M:           m,
		Next:        make([]int64, K),
		Active:      make([]bool, K),
		ActiveCount: 1,
	}
	for i := range st.Next {
		st.Next[i] = 1
	}
	st.Active[0] = true
	return st
}

// 规格例一的成员变更部分：K=3、m=4，逐步核对 next、Active 与 J。
func TestJoinLeaveSpecExample(t *testing.T) {
	st := newState(3, 4)
	st.Next[0] = 4 // Issue(1) 三次之后

	if err := st.Join(RoleAdmin, 2); err != nil {
		t.Fatalf("Join(2): %v", err)
	}
	if want := []int64{4, 5, 1}; !reflect.DeepEqual(st.Next, want) {
		t.Fatalf("Join(2) 后 Next = %v, want %v", st.Next, want)
	}
	if st.J != 4 {
		t.Fatalf("Join(2) 后 J = %d, want 4", st.J)
	}

	st.Next[0], st.Next[1] = 12, 13 // 四轮交错签发之后
	if err := st.Join(RoleAdmin, 3); err != nil {
		t.Fatalf("Join(3): %v", err)
	}
	if want := []int64{12, 13, 14}; !reflect.DeepEqual(st.Next, want) {
		t.Fatalf("Join(3) 后 Next = %v, want %v", st.Next, want)
	}
	if st.J != 17 {
		t.Fatalf("Join(3) 后 J = %d, want 17", st.J)
	}

	st.Next[2] = 18 // Issue(3)=14 之后
	if err := st.Leave(RoleAdmin, 3); err != nil {
		t.Fatalf("Leave(3): %v", err)
	}
	if want := []int64{12, 13, 18}; !reflect.DeepEqual(st.Next, want) {
		t.Fatalf("Leave(3) 后 Next = %v, want %v（离开者 next 原样保留）", st.Next, want)
	}
	if st.J != 17 {
		t.Fatalf("Leave(3) 后 J = %d, want 17（剩 2 个活跃系统不接续）", st.J)
	}

	if err := st.Leave(RoleAdmin, 1); err != nil {
		t.Fatalf("Leave(1): %v", err)
	}
	if want := []int64{12, 18, 18}; !reflect.DeepEqual(st.Next, want) {
		t.Fatalf("Leave(1) 后 Next = %v, want %v（含离开者的高水位接续）", st.Next, want)
	}
	if st.J != 22 {
		t.Fatalf("Leave(1) 后 J = %d, want 22", st.J)
	}
	if st.ActiveCount != 1 || !st.Active[1] {
		t.Fatalf("Leave(1) 后 Active = %v, want 仅系统 2 活跃", st.Active)
	}
}

// 校验次序：参数越界 → 权限不足 → 状态类；被拒不改变任何状态。
func TestRejectionOrder(t *testing.T) {
	st := newState(2, 4)
	snap := func() (int64, []int64, []bool) {
		next := append([]int64(nil), st.Next...)
		active := append([]bool(nil), st.Active...)
		return st.J, next, active
	}
	check := func(name string, err, want error, j int64, next []int64, active []bool) {
		t.Helper()
		if err != want {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
		if st.J != j || !reflect.DeepEqual(st.Next, next) || !reflect.DeepEqual(st.Active, active) {
			t.Errorf("%s: 被拒后状态被改变", name)
		}
	}

	j, next, active := snap()
	check("Join s 越界", st.Join(RoleAdmin, 3), ErrParam, j, next, active)
	check("Join role 越界", st.Join(3, 2), ErrParam, j, next, active)
	check("Join role 越界优先于权限", st.Join(0, 2), ErrParam, j, next, active)
	check("Join 权限不足", st.Join(1, 2), ErrPermission, j, next, active)
	check("Join 权限优先于状态", st.Join(1, 1), ErrPermission, j, next, active)
	check("Join 已活跃", st.Join(RoleAdmin, 1), ErrState, j, next, active)
	check("Leave s 越界", st.Leave(RoleAdmin, 0), ErrParam, j, next, active)
	check("Leave 权限不足", st.Leave(1, 1), ErrPermission, j, next, active)
	check("Leave 不活跃", st.Leave(RoleAdmin, 2), ErrState, j, next, active)
	check("Leave 最后活跃者", st.Leave(RoleAdmin, 1), ErrLast, j, next, active)
	check("Leave 权限优先于状态", st.Leave(1, 2), ErrPermission, j, next, active)
}

func TestHighWaterIncludesInactive(t *testing.T) {
	st := newState(3, 4)
	st.Next[2] = 100 // 不活跃系统 3 的计数器
	if got := st.HighWater(); got != 100 {
		t.Fatalf("HighWater = %d, want 100（含不活跃系统）", got)
	}
}
