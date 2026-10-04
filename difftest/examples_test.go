package difftest

import (
	"slices"
	"testing"

	"ontology/erase"
)

// 规格例一：S=2、T=100 的完整流程。
func TestSpecExample1(t *testing.T) {
	l, hs, rs := newStack(t, 2, 100)
	if err := hs.Hold(erase.RoleLegal, 8, 5); err != nil {
		t.Fatal(err)
	}
	if id := mustRequest(t, l, 7, 10); id != 1 {
		t.Fatalf("e1 id = %d", id)
	}
	if st, dl := erasureInfo(t, l, 1); st != erase.Active || dl != 110 {
		t.Fatalf("e1 = %v/%d, want Active/110", st, dl)
	}
	if id := mustRequest(t, l, 8, 12); id != 2 {
		t.Fatalf("e2 id = %d", id)
	}
	if st, _ := erasureInfo(t, l, 2); st != erase.Deferred {
		t.Fatalf("e2 = %v, want Deferred", st)
	}
	if b := mustBackup(t, rs, 1, 20); b != 1 {
		t.Fatalf("b1 id = %d", b)
	}
	mustAck(t, l, 1, 1, 30)
	L, err := rs.Restore(erase.RoleOps, 1, 1, 40)
	if err != nil || !slices.Equal(L, []int{1}) {
		t.Fatalf("Restore L = %v, err = %v; want [1]", L, err)
	}
	if err := rs.Read(1); err != erase.ErrRestoring {
		t.Fatalf("Read(1) = %v, want ErrRestoring", err)
	}
	if err := rs.ReapplyDone(erase.RoleOps, 1, 1, 50); err != nil {
		t.Fatal(err)
	}
	if err := rs.Read(1); err != nil {
		t.Fatalf("Read(1) after reapply = %v", err)
	}
	if b := mustBackup(t, rs, 1, 60); b != 2 {
		t.Fatalf("b2 id = %d", b)
	}
	if L, err := rs.Restore(erase.RoleOps, 1, 2, 70); err != nil || len(L) != 0 {
		t.Fatalf("Restore(b2) L = %v, err = %v; want empty", L, err)
	}
	if err := rs.Read(1); err != nil {
		t.Fatalf("Read(1) = %v, want Ready", err)
	}
	ov := l.Overdue(110)
	if len(ov) != 1 || ov[0].ID != 1 || !slices.Equal(ov[0].Pending, []int{2}) {
		t.Fatalf("Overdue(110) = %+v, want [{1 [2]}]", ov)
	}
	if err := hs.Release(erase.RoleLegal, 8, 200); err != nil {
		t.Fatal(err)
	}
	if st, dl := erasureInfo(t, l, 2); st != erase.Active || dl != 300 {
		t.Fatalf("e2 after release = %v/%d, want Active/300", st, dl)
	}
}

// 规格例二：S=1、T=100，恢复重放期间新 Ack 照常但不进待办。
func TestSpecExample2(t *testing.T) {
	l, _, rs := newStack(t, 1, 100)
	if id := mustRequest(t, l, 5, 0); id != 1 {
		t.Fatalf("e1 id = %d", id)
	}
	if b := mustBackup(t, rs, 1, 10); b != 1 {
		t.Fatalf("b1 id = %d", b)
	}
	mustAck(t, l, 1, 1, 11)
	if st, _ := erasureInfo(t, l, 1); st != erase.Done {
		t.Fatalf("e1 = %v, want Done", st)
	}
	if id := mustRequest(t, l, 5, 20); id != 2 {
		t.Fatalf("e2 id = %d（旧单已 Done，不算重复）", id)
	}
	mustAck(t, l, 2, 1, 25)
	L, err := rs.Restore(erase.RoleOps, 1, 1, 26)
	if err != nil || !slices.Equal(L, []int{1, 2}) {
		t.Fatalf("Restore L = %v, err = %v; want [1 2]", L, err)
	}
	if id := mustRequest(t, l, 6, 30); id != 3 {
		t.Fatalf("e3 id = %d", id)
	}
	mustAck(t, l, 3, 1, 31) // Restoring 期间新 Ack 照常接受
	if err := rs.Read(1); err != erase.ErrRestoring {
		t.Fatalf("Read(1) = %v, want ErrRestoring", err)
	}
	// e3 不在当时算出的待办里。
	if err := rs.ReapplyDone(erase.RoleOps, 1, 3, 32); err != erase.ErrState {
		t.Fatalf("ReapplyDone(e3) = %v, want ErrState", err)
	}
	// 以任意次序清掉 e2、e1 后才回 Ready。
	if err := rs.ReapplyDone(erase.RoleOps, 1, 2, 33); err != nil {
		t.Fatal(err)
	}
	if err := rs.Read(1); err != erase.ErrRestoring {
		t.Fatalf("Read(1) = %v, want still ErrRestoring", err)
	}
	if err := rs.ReapplyDone(erase.RoleOps, 1, 1, 34); err != nil {
		t.Fatal(err)
	}
	if err := rs.Read(1); err != nil {
		t.Fatalf("Read(1) after all reapply = %v", err)
	}
}

// 对照：ack 恰等 tb 视为备份已不含该主体数据，不在重放集中。
func TestAckEqualBackupTimeExcluded(t *testing.T) {
	l, _, rs := newStack(t, 1, 100)
	mustRequest(t, l, 5, 0)
	b1 := mustBackup(t, rs, 1, 10)
	mustAck(t, l, 1, 1, 10) // ack == tb
	mustRequest(t, l, 5, 20)
	mustAck(t, l, 2, 1, 25)
	L, err := rs.Restore(erase.RoleOps, 1, b1, 26)
	if err != nil || !slices.Equal(L, []int{2}) {
		t.Fatalf("L = %v, err = %v; want [2]（e1 ack==tb 不在 L）", L, err)
	}
}
