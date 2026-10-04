package corpact

import (
	"errors"
	"testing"
)

func TestRegistryLifecycle(t *testing.T) {
	r := NewRegistry()
	if err := r.Announce([]byte("a1"), []byte("S"), 30, 3, 5, 7); err != nil {
		t.Fatal(err)
	}
	if err := r.Announce([]byte("a2"), []byte("S"), 0, 1, 5, 8); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if err := r.Announce([]byte("a1"), []byte("T"), 1, 0, 1, 2); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("want duplicate, got %v", err)
	}
	if err := r.Cancel([]byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if err := r.Cancel([]byte("a1")); err != nil {
		t.Fatal(err)
	}
	// 取消后不再冲突，可以再次登记同标的行动。
	if err := r.Announce([]byte("a2"), []byte("S"), 0, 1, 5, 8); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryDueAndState(t *testing.T) {
	r := NewRegistry()
	if err := r.Announce([]byte("a1"), []byte("S"), 30, 3, 5, 7); err != nil {
		t.Fatal(err)
	}
	if got := r.DueSnapshot(5); len(got) != 0 {
		t.Fatalf("snap due at day5: %v", got)
	}
	if got := r.DueSnapshot(6); len(got) != 1 || string(got[0]) != "a1" {
		t.Fatalf("snap due = %v", got)
	}
	if err := r.AttachSnap([]byte("a1"), []SnapEntry{{Acct: []byte("A"), Q: 1, F: 0}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Cancel([]byte("a1")); !errors.Is(err, ErrBadState) {
		t.Fatalf("want bad state, got %v", err)
	}
	if got := r.DueExecute(7); len(got) != 1 {
		t.Fatalf("exec due = %v", got)
	}
	if err := r.AttachResult([]byte("a1"), &Result{Pex: 767}); err != nil {
		t.Fatal(err)
	}
	if got := r.DueExecute(8); len(got) != 0 {
		t.Fatalf("exec due after done: %v", got)
	}
	a, _ := r.Get([]byte("a1"))
	if a.State != Executed || a.Result.Pex != 767 {
		t.Fatalf("state = %v", a.State)
	}
}
