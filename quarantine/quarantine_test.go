package quarantine

import (
	"errors"
	"testing"
)

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		c, k, d int
		want    error
	}{
		{"ok edge", 1, 1, 1, nil},
		{"ok full", 100000, 100000, 1000, nil},
		{"c zero", 0, 0, 1, ErrInvalidParam},
		{"c too big", 100001, 1, 1, ErrInvalidParam},
		{"k zero", 1, 0, 1, ErrInvalidParam},
		{"k gt c", 2, 3, 1, ErrInvalidParam},
		{"dmax zero", 1, 1, 0, ErrInvalidParam},
		{"dmax too big", 1, 1, 1001, ErrInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.c, tc.k, tc.d)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestQueueAndCapacity(t *testing.T) {
	z, err := New(3, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string, seq int64, st State) error {
		return z.Append(key, Entry{Seq: seq, Key: key, State: st})
	}
	if err := put("a", 1, Quarantined); err != nil {
		t.Fatal(err)
	}
	if err := put("a", 2, Held); err != nil {
		t.Fatal(err)
	}
	if err := put("b", 3, Quarantined); err != nil {
		t.Fatal(err)
	}
	if z.Total() != 3 {
		t.Fatalf("total = %d, want 3", z.Total())
	}
	// ErrKeyFull 先于 ErrFull（总容量也恰好满）。
	if err := put("a", 4, Held); !errors.Is(err, ErrKeyFull) {
		t.Fatalf("err = %v, want ErrKeyFull", err)
	}
	if err := put("c", 5, Quarantined); !errors.Is(err, ErrFull) {
		t.Fatalf("err = %v, want ErrFull", err)
	}
	if got := z.Keys(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("keys = %v, want [a b]", got)
	}

	// 队首可修改，弹出保序；弹出后空间可复用。
	f := z.Front("a")
	if f.Seq != 1 || f.State != Quarantined {
		t.Fatalf("front = %+v, want seq1 Quarantined", f)
	}
	e, ok := z.PopFront("a")
	if !ok || e.Seq != 1 || z.Total() != 2 {
		t.Fatalf("pop = %d ok=%v total=%d", e.Seq, ok, z.Total())
	}
	if f2 := z.Front("a"); f2.Seq != 2 || f2.State != Held {
		t.Fatalf("new front = %+v, want seq2 Held", f2)
	}
	if err := put("c", 6, Quarantined); err != nil {
		t.Fatalf("reused slot: %v", err)
	}
	if _, ok := z.PopFront("missing"); ok {
		t.Fatal("PopFront on missing key should be ok=false")
	}
	if z.Front("missing") != nil {
		t.Fatal("Front on missing key should be nil")
	}
}

func TestDiscardAccounting(t *testing.T) {
	z, _ := New(2, 2, 2)
	if z.Banned("a") {
		t.Fatal("fresh key must not be banned")
	}
	if n := z.MarkDiscarded("a"); n != 1 || z.Banned("a") {
		t.Fatalf("after 1: n=%d banned=%v", n, z.Banned("a"))
	}
	if n := z.MarkDiscarded("a"); n != 2 || !z.Banned("a") {
		t.Fatalf("after 2: n=%d banned=%v", n, z.Banned("a"))
	}
	if z.DiscardCount("b") != 0 {
		t.Fatal("untracked key count must be 0")
	}
}
