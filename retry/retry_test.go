package retry

import (
	"errors"
	"testing"
)

func TestPolicyValidation(t *testing.T) {
	bad := []struct {
		m       int
		d0, cap int64
	}{
		{0, 5, 20}, {21, 5, 20}, {3, 0, 20}, {3, 1_000_001, 20},
		{3, 10, 9}, {3, 1, 1_000_000_001}, {1, -1, 5},
	}
	for _, c := range bad {
		if _, err := New(c.m, c.d0, c.cap); !errors.Is(err, ErrConfig) {
			t.Fatalf("New(%d,%d,%d) err=%v want ErrConfig", c.m, c.d0, c.cap, err)
		}
	}
	if _, err := New(3, 5, 20); err != nil {
		t.Fatal(err)
	}
}

func TestBackoff(t *testing.T) {
	p, _ := New(3, 5, 20)
	want := []int64{5, 10, 20}
	for k, w := range want {
		if got := p.Backoff(k + 1); got != w {
			t.Fatalf("Backoff(%d)=%d want %d", k+1, got, w)
		}
	}
	// M=20 时不溢出：d0=1e6, k=20 -> min(1e6*2^19, cap=1e9)=1e9。
	p2, _ := New(20, 1_000_000, 1_000_000_000)
	if got := p2.Backoff(20); got != 1_000_000_000 {
		t.Fatalf("Backoff(20)=%d want 1e9", got)
	}
	if !p.CanRetry(1) || p.CanRetry(3) {
		t.Fatalf("CanRetry bad: %v %v", p.CanRetry(1), p.CanRetry(3))
	}
}
