package fence

import (
	"sync"
	"testing"
)

func TestIssueStrictlyMonotonic(t *testing.T) {
	a := NewAllocator()
	var prev Token
	for i := 0; i < 100; i++ {
		tk := a.Issue()
		if tk <= prev {
			t.Fatalf("token not strictly increasing: %d <= %d", tk, prev)
		}
		prev = tk
	}
	if a.Last() != prev {
		t.Fatalf("Last() = %d, want %d", a.Last(), prev)
	}
}

func TestObserveWatermark(t *testing.T) {
	a := NewAllocator()
	if !a.Observe(1) {
		t.Fatal("first observe should pass")
	}
	if !a.Observe(3) {
		t.Fatal("larger token should pass")
	}
	if a.Mark() != 3 {
		t.Fatalf("mark = %d, want 3", a.Mark())
	}
	if a.Observe(2) {
		t.Fatal("token below watermark must be rejected")
	}
	if !a.Observe(3) {
		t.Fatal("token equal to watermark should still pass")
	}
	if a.Mark() != 3 {
		t.Fatalf("mark = %d, want 3", a.Mark())
	}
}

func TestConcurrentObserveNoRegress(t *testing.T) {
	a := NewAllocator()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(v uint64) {
			defer wg.Done()
			a.Observe(Token(v))
		}(uint64(i))
	}
	wg.Wait()
	if a.Mark() != 49 {
		t.Fatalf("mark = %d, want 49", a.Mark())
	}
}
