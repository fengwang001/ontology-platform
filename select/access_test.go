package sel

import "testing"

// TestSampleAccessCountConstant proves Sample touches exactly s population
// elements no matter how large N is, by reading the unexported counter
// directly. Only this package's tests can observe it; nothing exported
// reveals it.
func TestSampleAccessCountConstant(t *testing.T) {
	const s = 10
	for _, n := range []int{100, 250, 500, 1000, 2500, 5000, 10000} {
		sl := New(n, s, 0)
		if _, err := sl.Sample(make([]int64, n)); err != nil {
			t.Fatalf("N=%d: %v", n, err)
		}
		if sl.accessed != s {
			t.Fatalf("N=%d: accessed %d population elements, want %d", n, sl.accessed, s)
		}
	}
}

// TestRejectedSampleLeavesCounter verifies a length-mismatched Sample fails
// wholesale and does not move the counter (failure leaves no trace).
func TestRejectedSampleLeavesCounter(t *testing.T) {
	sl := New(10, 4, 1.0)
	if _, err := sl.Sample(make([]int64, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := sl.Sample(make([]int64, 9)); err != ErrPopulationLength {
		t.Fatalf("want ErrPopulationLength, got %v", err)
	}
	if sl.accessed != 4 {
		t.Fatalf("rejected Sample moved counter to %d, want 4", sl.accessed)
	}
}
