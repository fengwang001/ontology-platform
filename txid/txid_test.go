package txid

import "testing"

func TestSourceMonotonic(t *testing.T) {
	var s Source
	var prev TxID
	for i := 0; i < 1000; i++ {
		next, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if !next.Valid() {
			t.Fatal("Next returned zero")
		}
		if i > 0 && !prev.Before(next) {
			t.Fatalf("not monotonic: %d then %d", prev, next)
		}
		prev = next
	}
}

func TestSourceAdvanceNoDecrease(t *testing.T) {
	var s Source
	s.Next()
	s.Next()
	if err := s.Advance(1); err != nil {
		t.Fatal(err)
	}
	next, _ := s.Next()
	if next != 3 {
		t.Fatalf("got %d, want 3", next)
	}
	if err := s.Advance(0); err != ErrIllegal {
		t.Fatalf("got %v, want ErrIllegal", err)
	}
}

func TestZeroPanics(t *testing.T) {
	defer func() {
		if recover() != ErrIllegal {
			t.Fatal("want ErrIllegal panic")
		}
	}()
	var z TxID
	z.Before(1)
}
