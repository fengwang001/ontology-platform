package limit

import "testing"

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(1, []byte("A"), []byte("G")); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(2, []byte("A"), []byte("G2")); err != ErrDuplicate {
		t.Fatalf("duplicate register err=%v want ErrDuplicate", err)
	}
	if g, ok := r.Group([]byte("A")); !ok || string(g) != "G" {
		t.Fatalf("group=%q ok=%v", g, ok)
	}
	if _, ok := r.Group([]byte("X")); ok {
		t.Fatalf("unknown acct must report missing")
	}
	r.SetLimit(3, []byte("S"), 100, 150, 1000)
	l, ok := r.Limits([]byte("S"))
	if !ok || l != (SymLimit{Acct: 100, Group: 150, DayOpen: 1000}) {
		t.Fatalf("limit=%+v ok=%v", l, ok)
	}
	// 可重复调用（含调低）。
	r.SetLimit(4, []byte("S"), 30, 30, 100)
	if l, _ := r.Limits([]byte("S")); l.Acct != 30 || l.Group != 30 || l.DayOpen != 100 {
		t.Fatalf("limit not updated: %+v", l)
	}
}
