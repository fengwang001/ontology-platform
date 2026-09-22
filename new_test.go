package hll

import (
	"errors"
	"testing"
)

func TestNewInvalidPrecision(t *testing.T) {
	for _, p := range []int{-1, 0, 3, 17, 64, 1 << 30} {
		e, err := New(p)
		if !errors.Is(err, ErrPrecision) {
			t.Fatalf("New(%d) err = %v, want ErrPrecision", p, err)
		}
		if e != nil {
			t.Fatalf("New(%d) returned non-nil estimator", p)
		}
	}
}

func TestNewValidPrecision(t *testing.T) {
	for p := MinPrecision; p <= MaxPrecision; p++ {
		e, err := New(p)
		if err != nil {
			t.Fatalf("New(%d): %v", p, err)
		}
		if e.Precision() != p {
			t.Fatalf("Precision() = %d, want %d", e.Precision(), p)
		}
		if got := len(e.InspectRegisters()); got != 1<<uint(p) {
			t.Fatalf("p=%d registers = %d, want %d", p, got, 1<<uint(p))
		}
		if got := e.Estimate(); got != 0 {
			t.Fatalf("empty estimator p=%d Estimate = %d, want 0", p, got)
		}
	}
}
