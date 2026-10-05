package quarantine

import (
	"errors"
	"reflect"
	"testing"
)

func mustZone(t *testing.T, c, k, dmax int) *Zone {
	t.Helper()
	z, err := New(c, k, dmax)
	if err != nil {
		t.Fatalf("New(%d,%d,%d): %v", c, k, dmax, err)
	}
	return z
}

func rec(seq uint64, key string) *Record {
	return &Record{Seq: seq, Key: key, Fields: map[string]int64{"amt": 1}, Status: Quarantined}
}

func TestNewValidation(t *testing.T) {
	for _, p := range [][3]int{{0, 1, 1}, {100001, 1, 1}, {3, 0, 1}, {3, 4, 1}, {3, 1, 0}, {3, 1, 1001}} {
		if _, err := New(p[0], p[1], p[2]); err == nil {
			t.Fatalf("New%v: want error", p)
		}
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Fatalf("New(1,1,1): %v", err)
	}
}

func TestCapacityAndOrder(t *testing.T) {
	z := mustZone(t, 3, 2, 5)
	// ErrKeyFull 先于 ErrFull：K=2 的键满时即使总量未满也报 ErrKeyFull。
	z.Push(rec(1, "a"))
	z.Push(rec(2, "a"))
	if err := z.CheckAdd("a"); !errors.Is(err, ErrKeyFull) {
		t.Fatalf("CheckAdd(a) = %v, want ErrKeyFull", err)
	}
	z.Push(rec(3, "b"))
	if z.Total() != 3 {
		t.Fatalf("Total = %d, want 3", z.Total())
	}
	// 总量恰满：新键报 ErrFull；键 a 已满，ErrKeyFull 仍优先。
	if err := z.CheckAdd("c"); !errors.Is(err, ErrFull) {
		t.Fatalf("CheckAdd(c) = %v, want ErrFull", err)
	}
	if err := z.CheckAdd("a"); !errors.Is(err, ErrKeyFull) {
		t.Fatalf("CheckAdd(a) full zone = %v, want ErrKeyFull", err)
	}
	// 保序弹出。
	if got := z.Pop("a"); got.Seq != 1 {
		t.Fatalf("Pop(a).Seq = %d, want 1", got.Seq)
	}
	if got := z.Front("a"); got.Seq != 2 {
		t.Fatalf("Front(a).Seq = %d, want 2", got.Seq)
	}
	z.Pop("a")
	if z.HasQueue("a") {
		t.Fatal("queue a should be gone after drain")
	}
	if got := z.Keys(); !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("Keys = %v, want [b]", got)
	}
}

func TestKeysByteOrder(t *testing.T) {
	z := mustZone(t, 4, 4, 5)
	for _, k := range []string{"b", "a", "\x01", "\xff"} {
		z.Push(rec(1, k))
	}
	want := []string{"\x01", "a", "b", "\xff"}
	if got := z.Keys(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Keys = %q, want %q", got, want)
	}
}

func TestDiscardBan(t *testing.T) {
	z := mustZone(t, 10, 10, 2)
	z.Push(rec(1, "a"))
	z.Push(rec(2, "a"))
	e1 := z.Discard("a")
	if e1.Seq != 1 || z.DiscardCount("a") != 1 || z.Banned("a") {
		t.Fatalf("after 1st discard: %+v count=%d banned=%v", e1, z.DiscardCount("a"), z.Banned("a"))
	}
	z.Discard("a")
	// 恰达 Dmax 即封禁。
	if !z.Banned("a") {
		t.Fatal("key a should be banned at Dmax")
	}
	if got := len(z.Dropped()); got != 2 {
		t.Fatalf("len(Dropped) = %d, want 2", got)
	}
	if z.Total() != 0 {
		t.Fatalf("Total = %d, want 0", z.Total())
	}
}
