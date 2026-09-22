package txid

import (
	"errors"
	"testing"
)

func TestZeroInvalid(t *testing.T) {
	var z TxID
	if z.IsValid() {
		t.Fatal("zero TxID must be invalid")
	}
	if TxID(1).Less(TxID(1)) {
		t.Fatal("equal TxID must not be less")
	}
}

func TestMonotonicNoWrap(t *testing.T) {
	s := NewSource()
	if got := s.Last(); got.IsValid() {
		t.Fatalf("Last before allocation = %v, want invalid", got)
	}
	if s.Frontier() != 1 {
		t.Fatalf("frontier = %d, want 1", s.Frontier())
	}
	first, err := s.Next()
	if err != nil || first != 1 {
		t.Fatalf("first Next = %v,%v", first, err)
	}
	second, err := s.Next()
	if err != nil || second <= first {
		t.Fatalf("second Next = %v,%v not greater than %v", second, err, first)
	}
	if s.Last() != second {
		t.Fatalf("Last = %d, want %d", s.Last(), second)
	}
	if s.Frontier() != TxID(uint64(second)+1) {
		t.Fatalf("frontier = %d, want %d", s.Frontier(), uint64(second)+1)
	}

	exhausted := &Source{next: 0}
	if _, err := exhausted.Next(); !errors.Is(err, ErrExhausted) {
		t.Fatalf("exhausted Next err = %v, want ErrExhausted", err)
	}
}

func TestConcurrentAllocation(t *testing.T) {
	// 分配器本身不承担并发责任（由 store 串行化），这里仅确认连续分配不重复。
	s := NewSource()
	seen := make(map[TxID]bool)
	for i := 0; i < 1000; i++ {
		id, err := s.Next()
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %d", id)
		}
		seen[id] = true
	}
}
