package retry

import (
	"errors"
	"testing"
)

func TestBackoffSequenceAndCap(t *testing.T) {
	p := Policy{MaxAttempts: 3, Initial: 5, Cap: 20}
	want := []int64{5, 10, 20}
	for k, w := range want {
		if got := Backoff(p, k+1); got != w {
			t.Fatalf("Backoff(k=%d)=%d want %d", k+1, got, w)
		}
	}
	// cap 生效后不再增长，k=20 也不溢出。
	p20 := Policy{MaxAttempts: 20, Initial: 1_000_000, Cap: 1_000_000_000}
	if got := Backoff(p20, 20); got != 1_000_000_000 {
		t.Fatalf("Backoff(20)=%d want cap 1e9", got)
	}
	if got := Backoff(p20, 19); got != 1_000_000_000 {
		t.Fatalf("Backoff(19)=%d want cap 1e9", got)
	}
}

func TestCanRetry(t *testing.T) {
	p := Policy{MaxAttempts: 3, Initial: 1, Cap: 1}
	for k, want := range map[int]bool{1: true, 2: true, 3: false} {
		if got := CanRetry(p, k); got != want {
			t.Fatalf("CanRetry(%d)=%v want %v", k, got, want)
		}
	}
}

func TestWithinGlobal(t *testing.T) {
	// t0=0, sc=26：g'=26 恰等不算在内。
	if WithinGlobal(26, 0, 26, true) {
		t.Fatal("g'=26 must not be within sc=26")
	}
	if !WithinGlobal(25, 0, 26, true) {
		t.Fatal("g'=25 must be within sc=26")
	}
	if !WithinGlobal(1<<60, 0, 0, false) {
		t.Fatal("without sc everything is within")
	}
}

func TestValidate(t *testing.T) {
	good := Policy{MaxAttempts: 20, Initial: 1, Cap: 1_000_000_000}
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Policy{
		{MaxAttempts: 0, Initial: 1, Cap: 1},
		{MaxAttempts: 21, Initial: 1, Cap: 1},
		{MaxAttempts: 1, Initial: 0, Cap: 1},
		{MaxAttempts: 1, Initial: 10, Cap: 9}, // cap < d0
		{MaxAttempts: 1, Initial: 1, Cap: 1_000_000_001},
	}
	for i, p := range bad {
		if !errors.Is(p.Validate(), ErrConfig) {
			t.Fatalf("case %d expected ErrConfig", i)
		}
	}
}
