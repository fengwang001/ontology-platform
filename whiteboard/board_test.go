package whiteboard

import (
	"errors"
	"reflect"
	"testing"
)

// mustKind 断言 err 为指定类别的 *Error。
func mustKind(t *testing.T, err error, kind Kind) *Error {
	t.Helper()
	var werr *Error
	if !errors.As(err, &werr) {
		t.Fatalf("expected *Error kind %v, got %v", kind, err)
	}
	if werr.Kind != kind {
		t.Fatalf("expected kind %v, got %v (%v)", kind, werr.Kind, werr)
	}
	return werr
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustOrder(t *testing.T, b *Board, want ...string) {
	t.Helper()
	got := b.Order()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func setup(t *testing.T, b *Board, ids ...string) {
	t.Helper()
	for i, id := range ids {
		mustOK(t, b.Add("setup", id, int64(i)))
	}
}

func TestAddGroupUngroupBasic(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	mustOrder(t, b, "a", "b", "c", "d")

	mustOK(t, b.Group("u", "g1", []string{"b", "c"}, 10))
	mustOK(t, b.Ungroup("u", "g1", 11))
	mustOrder(t, b, "a", "b", "c", "d") // 解散不改变次序

	if got := b.Revision(); got != 6 { // 4 Add + Group + Ungroup
		t.Fatalf("revision = %d, want 6", got)
	}
	mustOK(t, b.Add("u", "e", 12))
	if got := b.Revision(); got != 7 {
		t.Fatalf("revision = %d, want 7", got)
	}
}

func TestClockRollback(t *testing.T) {
	b := New()
	mustOK(t, b.Add("u", "a", 100))
	mustKind(t, b.Add("u", "b", 99), ErrClock)
	mustOK(t, b.Add("u", "b", 100)) // 等于上一次 now 允许
	mustKind(t, b.Lock("u", "a", 10, 50), ErrClock)
	mustKind(t, b.Unlock("u", "a", 50), ErrClock)
	mustOrder(t, b, "a", "b")
}

func TestRankBetween(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	for i, id := range []string{"a", "b", "c", "d"} {
		r, ok := b.Rank(id)
		if !ok || r != i {
			t.Fatalf("Rank(%s) = %d,%v want %d,true", id, r, ok, i)
		}
	}
	if _, ok := b.Rank("zzz"); ok {
		t.Fatal("Rank of missing element should be !ok")
	}
	got, err := b.Between(1, 2)
	if err != nil || !reflect.DeepEqual(got, []string{"b", "c"}) {
		t.Fatalf("Between(1,2) = %v,%v", got, err)
	}
	if _, err := b.Between(2, 1); err == nil {
		t.Fatal("Between(2,1) should fail")
	}
	if _, err := b.Between(0, 4); err == nil {
		t.Fatal("Between(0,4) should fail")
	}
}
