package restore

import "testing"

func TestReapplyUnknownDoesNotMutateFrozenPending(t *testing.T) {
	m := NewManager(1)
	if _, err := m.Backup(3, 1, 10); err != nil {
		t.Fatal(err)
	}
	m.RecordAck(3, 1, 20)
	list, err := m.Restore(3, 1, 1, 30)
	if err != nil || len(list) != 1 || list[0] != 3 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	if err := m.ReapplyDone(3, 1, 1, 31); err != ErrState {
		t.Fatalf("unknown reapply err=%v", err)
	}
	if !m.restoring[1] {
		t.Fatal("unknown reapply cleared restoring")
	}
	if err := m.ReapplyDone(3, 1, 3, 32); err != nil {
		t.Fatalf("known reapply: %v", err)
	}
	if m.restoring[1] {
		t.Fatal("known reapply did not clear restoring")
	}
}
