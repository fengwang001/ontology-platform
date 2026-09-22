package txid

import "testing"

func TestAllocateMonotonicAndNonZero(t *testing.T) {
	src := NewSource()
	var prev ID
	for i := 0; i < 1000; i++ {
		id, err := src.Allocate()
		if err != nil || !id.Valid() {
			t.Fatalf("alloc %d: id=%d err=%v", i, id, err)
		}
		if i > 0 && !prev.Less(id) {
			t.Fatalf("not monotonic: %d then %d", prev, id)
		}
		prev = id
	}
}

func TestPeekDoesNotConsume(t *testing.T) {
	src := NewSourceAt(7)
	p, err := src.Peek()
	if err != nil || p != 7 {
		t.Fatalf("peek = %d, %v", p, err)
	}
	p2, _ := src.Peek()
	if p2 != 7 {
		t.Fatalf("second peek = %d, want 7", p2)
	}
	id, _ := src.Allocate()
	if id != 7 {
		t.Fatalf("allocate = %d, want 7", id)
	}
	if next, _ := src.Peek(); next != 8 {
		t.Fatalf("peek after alloc = %d, want 8", next)
	}
}

func TestInvalidZero(t *testing.T) {
	var id ID
	if id.Valid() {
		t.Fatal("zero ID must be invalid")
	}
	src := NewSourceAt(0)
	if id, _ := src.Allocate(); id != 1 {
		t.Fatalf("NewSourceAt(0) gave %d, want 1", id)
	}
}
