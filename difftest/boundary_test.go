package difftest

import (
	"slices"
	"testing"

	"ontology/erase"
)

// ack 恰等 tb 与差 1：ack > tb 才进重放集，ack <= tb 不进。
func TestAckBackupTimeBoundary(t *testing.T) {
	l, _, rs := newStack(t, 1, 1000)
	mustRequest(t, l, 1, 0) // e1
	mustRequest(t, l, 2, 0) // e2
	mustRequest(t, l, 3, 0) // e3
	mustAck(t, l, 1, 1, 9)  // tb-1：备份仍含数据，不进 L
	b1 := mustBackup(t, rs, 1, 10)
	mustAck(t, l, 2, 1, 10) // tb：恰等，不进 L
	mustAck(t, l, 3, 1, 11) // tb+1：进 L
	L, err := rs.Restore(erase.RoleOps, 1, b1, 12)
	if err != nil || !slices.Equal(L, []int{3}) {
		t.Fatalf("L = %v, err = %v; want [3]", L, err)
	}
}

// Deferred 不重放：保留期间的擦除单无任何 ack，永不进重放集；
// 解除后补 ack，且 ack 晚于备份点才进 L。
func TestDeferredNotReplayed(t *testing.T) {
	l, hs, rs := newStack(t, 1, 100)
	if err := hs.Hold(erase.RoleLegal, 5, 0); err != nil {
		t.Fatal(err)
	}
	mustRequest(t, l, 5, 1) // e1 Deferred
	b1 := mustBackup(t, rs, 1, 2)
	if L, err := rs.Restore(erase.RoleOps, 1, b1, 3); err != nil || len(L) != 0 {
		t.Fatalf("L = %v, err = %v; want empty（Deferred 不重放）", L, err)
	}
	if err := hs.Release(erase.RoleLegal, 5, 4); err != nil {
		t.Fatal(err)
	}
	b2 := mustBackup(t, rs, 1, 4)
	mustAck(t, l, 1, 1, 5)
	L, err := rs.Restore(erase.RoleOps, 1, b2, 6)
	if err != nil || !slices.Equal(L, []int{1}) {
		t.Fatalf("L = %v, err = %v; want [1]", L, err)
	}
}

// 保留期间请求的擦除单，SLA 自解除起算；恰等 deadline 即逾期。
func TestSLAStartsAtRelease(t *testing.T) {
	l, hs, _ := newStack(t, 1, 100)
	if err := hs.Hold(erase.RoleLegal, 7, 0); err != nil {
		t.Fatal(err)
	}
	mustRequest(t, l, 7, 10) // e1 Deferred，无时限
	if got := l.Overdue(1_000_000); len(got) != 0 {
		t.Fatalf("Deferred 不应逾期: %v", got)
	}
	if err := hs.Release(erase.RoleLegal, 7, 200); err != nil {
		t.Fatal(err)
	}
	if st, dl := erasureInfo(t, l, 1); st != erase.Active || dl != 300 {
		t.Fatalf("e1 = %v/%d, want Active/300", st, dl)
	}
	if got := l.Overdue(299); len(got) != 0 {
		t.Fatalf("Overdue(299) = %v, want empty", got)
	}
	got := l.Overdue(300)
	if len(got) != 1 || got[0].ID != 1 || !slices.Equal(got[0].Pending, []int{1}) {
		t.Fatalf("Overdue(300) = %+v, want [{1 [1]}]", got)
	}
}

// 法律保留不撤回已 Active 的擦除：保留后仍可确认至 Done。
func TestHoldDoesNotCancelActive(t *testing.T) {
	l, hs, _ := newStack(t, 2, 100)
	mustRequest(t, l, 7, 10)
	mustAck(t, l, 1, 1, 20)
	if err := hs.Hold(erase.RoleLegal, 7, 30); err != nil {
		t.Fatal(err)
	}
	if st, _ := erasureInfo(t, l, 1); st != erase.Active {
		t.Fatalf("e1 = %v, want Active（保留不撤回）", st)
	}
	mustAck(t, l, 1, 2, 40)
	if st, _ := erasureInfo(t, l, 1); st != erase.Done {
		t.Fatalf("e1 = %v, want Done", st)
	}
	// 保留期间该主体的新请求进入 Deferred。
	if id := mustRequest(t, l, 7, 50); id != 2 {
		t.Fatalf("e2 id = %d", id)
	}
	if st, _ := erasureInfo(t, l, 2); st != erase.Deferred {
		t.Fatalf("e2 = %v, want Deferred", st)
	}
}

// 重复请求拒绝：Active 与 Deferred 都算未完成；Done 后可再请求。
func TestDuplicateRejected(t *testing.T) {
	l, hs, _ := newStack(t, 1, 100)
	mustRequest(t, l, 7, 10)
	if _, err := l.Request(erase.RolePrivacy, 7, 20); err != erase.ErrDuplicate {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
	if err := hs.Hold(erase.RoleLegal, 8, 20); err != nil {
		t.Fatal(err)
	}
	mustRequest(t, l, 8, 25) // Deferred
	if _, err := l.Request(erase.RolePrivacy, 8, 26); err != erase.ErrDuplicate {
		t.Fatalf("err = %v, want ErrDuplicate（Deferred 也算未完成）", err)
	}
	mustAck(t, l, 1, 1, 30) // e1 Done
	if id := mustRequest(t, l, 7, 31); id != 3 {
		t.Fatalf("e3 id = %d（Done 后可再请求）", id)
	}
}

// 拒绝次序：参数越界 → 权限 → 时钟回退 → 状态类；Restore 内先
// ErrNoBackup 再 ErrState。
func TestRejectionOrder(t *testing.T) {
	l, _, rs := newStack(t, 2, 100)
	mustRequest(t, l, 7, 10)
	mustAck(t, l, 1, 1, 20)
	// 参数越界优先于权限与时钟。
	if err := l.Ack(erase.RolePrivacy, 99, 1, 0); err != erase.ErrParam {
		t.Fatalf("Ack(e=99,role=1,回退) = %v, want ErrParam", err)
	}
	if err := l.Ack(erase.RolePrivacy, 1, 9, 0); err != erase.ErrParam {
		t.Fatalf("Ack(s=9) = %v, want ErrParam", err)
	}
	// 权限优先于时钟回退。
	if err := l.Ack(erase.RolePrivacy, 1, 1, 0); err != erase.ErrPerm {
		t.Fatalf("Ack(role=1,回退) = %v, want ErrPerm", err)
	}
	// 时钟回退优先于状态类（s=1 已确认，本应是 ErrState）。
	if err := l.Ack(erase.RoleOps, 1, 1, 0); err != erase.ErrClock {
		t.Fatalf("Ack(回退) = %v, want ErrClock", err)
	}
	// 状态类：重复确认。
	if err := l.Ack(erase.RoleOps, 1, 1, 20); err != erase.ErrState {
		t.Fatalf("Ack(重复) = %v, want ErrState", err)
	}
	// Restore：b 不属于 s 报 ErrNoBackup，且优先于 ErrState。
	b1 := mustBackup(t, rs, 1, 30)
	mustAck(t, l, 1, 2, 40) // e1 Done，ack(1,2)=40 > 30
	if _, err := rs.Restore(erase.RoleOps, 2, b1, 50); err != erase.ErrNoBackup {
		t.Fatalf("Restore(s=2,b1) = %v, want ErrNoBackup", err)
	}
	mustRequest(t, l, 8, 55) // e2
	mustAck(t, l, 2, 1, 60)  // ack(2,1)=60 > tb=30
	if L, err := rs.Restore(erase.RoleOps, 1, b1, 70); err != nil || !slices.Equal(L, []int{2}) {
		t.Fatalf("Restore(s=1,b1) L = %v, err = %v; want [2]", L, err)
	}
	// s=1 已 Restoring：用 s=2 的备份点仍先报 ErrNoBackup。
	b2 := mustBackup(t, rs, 2, 80)
	if _, err := rs.Restore(erase.RoleOps, 1, b2, 90); err != erase.ErrNoBackup {
		t.Fatalf("Restore(s=1,b2) = %v, want ErrNoBackup", err)
	}
	if _, err := rs.Restore(erase.RoleOps, 1, b1, 90); err != erase.ErrState {
		t.Fatalf("Restore(s=1,b1) = %v, want ErrState", err)
	}
	// Read 参数越界。
	if err := rs.Read(0); err != erase.ErrParam {
		t.Fatalf("Read(0) = %v, want ErrParam", err)
	}
	if err := rs.Read(3); err != erase.ErrParam {
		t.Fatalf("Read(3) = %v, want ErrParam", err)
	}
}

// 被拒操作不改变擦除单、保留标志、备份点、系统状态、编号计数与最大 now。
func TestRejectedNoSideEffect(t *testing.T) {
	l, hs, rs := newStack(t, 1, 100)
	mustRequest(t, l, 7, 10) // e1
	mustRequest(t, l, 8, 10) // e2（保持未完成，用于重复拒绝）
	b1 := mustBackup(t, rs, 1, 20)
	mustAck(t, l, 1, 1, 30) // e1 Done
	if _, err := rs.Restore(erase.RoleOps, 1, b1, 40); err != nil {
		t.Fatal(err)
	}
	// 一系列被拒操作。
	if _, err := l.Request(erase.RolePrivacy, 8, 50); err != erase.ErrDuplicate {
		t.Fatalf("want ErrDuplicate, got %v", err)
	}
	if err := hs.Hold(erase.RoleOps, 7, 50); err != erase.ErrPerm {
		t.Fatalf("want ErrPerm, got %v", err)
	}
	if err := hs.Release(erase.RoleLegal, 7, 50); err != erase.ErrNotHeld {
		t.Fatalf("want ErrNotHeld, got %v", err)
	}
	if _, err := rs.Backup(erase.RoleOps, 1, 10); err != erase.ErrClock {
		t.Fatalf("want ErrClock, got %v", err)
	}
	if _, err := rs.Restore(erase.RoleOps, 1, 99, 50); err != erase.ErrParam {
		t.Fatalf("want ErrParam, got %v", err)
	}
	if err := rs.ReapplyDone(erase.RoleOps, 1, 99, 50); err != erase.ErrParam {
		t.Fatalf("want ErrParam, got %v", err)
	}
	if err := rs.ReapplyDone(erase.RoleOps, 1, 1, 10); err != erase.ErrClock {
		t.Fatalf("want ErrClock, got %v", err)
	}
	// 编号计数未动：新请求得 e3，新备份得 b2。
	if id := mustRequest(t, l, 9, 50); id != 3 {
		t.Fatalf("e3 id = %d（被拒操作不得消耗编号）", id)
	}
	if b := mustBackup(t, rs, 1, 50); b != 2 {
		t.Fatalf("b2 id = %d（被拒操作不得消耗编号）", b)
	}
	// 最大 now 未被拒操作污染：now=50 仍被接受（上面已用），now=49 被拒。
	if err := l.Ack(erase.RoleOps, 2, 1, 49); err != erase.ErrClock {
		t.Fatalf("want ErrClock, got %v", err)
	}
	mustAck(t, l, 2, 1, 50)
	// 系统状态未被破坏：s=1 仍 Restoring，待办仍为 {e1}。
	if err := rs.Read(1); err != erase.ErrRestoring {
		t.Fatalf("Read(1) = %v, want ErrRestoring", err)
	}
	if err := rs.ReapplyDone(erase.RoleOps, 1, 1, 60); err != nil {
		t.Fatal(err)
	}
	if err := rs.Read(1); err != nil {
		t.Fatalf("Read(1) = %v, want Ready", err)
	}
	// 保留标志未被误置：Release 应报 ErrNotHeld。
	if err := hs.Release(erase.RoleLegal, 7, 70); err != erase.ErrNotHeld {
		t.Fatalf("want ErrNotHeld, got %v", err)
	}
}

// Hold/Release 自身的拒绝：ErrAlready 与 ErrNotHeld。
func TestHoldReleaseStateErrors(t *testing.T) {
	_, hs, _ := newStack(t, 1, 100)
	if err := hs.Release(erase.RoleLegal, 7, 0); err != erase.ErrNotHeld {
		t.Fatalf("Release = %v, want ErrNotHeld", err)
	}
	if err := hs.Hold(erase.RoleLegal, 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := hs.Hold(erase.RoleLegal, 7, 2); err != erase.ErrAlready {
		t.Fatalf("Hold = %v, want ErrAlready", err)
	}
	if err := hs.Release(erase.RoleLegal, 7, 3); err != nil {
		t.Fatal(err)
	}
	if err := hs.Release(erase.RoleLegal, 7, 4); err != erase.ErrNotHeld {
		t.Fatalf("Release = %v, want ErrNotHeld", err)
	}
}
