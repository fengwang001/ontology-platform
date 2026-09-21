package fence

import (
	"errors"
	"sync"
	"testing"
)

func TestAllocateStrictlyIncreasing(t *testing.T) {
	f := New()
	prev := Token(0)
	for i := 0; i < 1000; i++ {
		tok := f.Allocate()
		if tok <= prev {
			t.Fatalf("token %d not greater than previous %d", tok, prev)
		}
		prev = tok
	}
}

func TestValidateRejectsBelowWatermark(t *testing.T) {
	f := New()
	tok := f.Allocate() // watermark = 1
	if err := f.Validate(tok); err != nil {
		t.Fatalf("validate current token: %v", err)
	}
	if err := f.Validate(tok + 10); err != nil { // raises watermark to 11
		t.Fatalf("validate newer token: %v", err)
	}
	// Now even the previously accepted token is below the watermark.
	if err := f.Validate(tok); !errors.Is(err, ErrStale) {
		t.Fatalf("want ErrStale for old watermark token, got %v", err)
	}
	var stale *StaleError
	if err := f.Validate(tok); !errors.As(err, &stale) {
		t.Fatalf("want *StaleError, got %v", err)
	} else if stale.Token != tok || stale.Watermark != tok+10 {
		t.Fatalf("stale detail = %+v", stale)
	}
}

func TestValidateAcceptsEqualToWatermark(t *testing.T) {
	f := New()
	tok := f.Allocate()
	if err := f.Validate(tok); err != nil {
		t.Fatalf("equal-to-watermark must be accepted: %v", err)
	}
}

func TestConcurrentAllocateUnique(t *testing.T) {
	f := New()
	const n = 64
	tokens := make([]Token, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i] = f.Allocate()
		}(i)
	}
	wg.Wait()
	seen := make(map[Token]bool, n)
	for _, tok := range tokens {
		if seen[tok] {
			t.Fatalf("duplicate token %d", tok)
		}
		seen[tok] = true
	}
}

func TestConcurrentValidateMonotonicWatermark(t *testing.T) {
	f := New()
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = f.Validate(Token(i + 1))
		}(i)
	}
	wg.Wait()
	if got := f.Watermark(); got != n {
		t.Fatalf("watermark = %d, want %d (must never regress)", got, n)
	}
}
