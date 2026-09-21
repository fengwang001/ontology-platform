package fence_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/fence"
)

func TestGrantStrictlyIncreasing(t *testing.T) {
	f := fence.New()
	prev := fence.Token(0)
	for i := 0; i < 1000; i++ {
		tok := f.Grant()
		if tok <= prev {
			t.Fatalf("grant %d not greater than previous %d", tok, prev)
		}
		prev = tok
	}
	if got := f.Current(); got != prev {
		t.Fatalf("Current() = %d, want %d", got, prev)
	}
}

func TestAcceptRejectsBelowWatermark(t *testing.T) {
	f := fence.New()
	tok := f.Grant()
	if err := f.Accept(tok); err != nil {
		t.Fatalf("accept current token: %v", err)
	}
	if err := f.Accept(tok); err != nil {
		t.Fatalf("re-accept same token should be allowed: %v", err)
	}
	if err := f.Accept(tok - 1); !errors.Is(err, fence.ErrStale) {
		t.Fatalf("accept older token: got %v, want ErrStale", err)
	}
}

func TestWatermarkNeverRegresses(t *testing.T) {
	f := fence.New()
	first := f.Grant()
	if err := f.Accept(first); err != nil {
		t.Fatal(err)
	}
	// 接受一个更大的令牌把水位抬上去。
	bigger := first + 10
	if err := f.Accept(bigger); err != nil {
		t.Fatal(err)
	}
	// 等于旧水位的令牌也必须被拒绝。
	if err := f.Accept(first); !errors.Is(err, fence.ErrStale) {
		t.Fatalf("old watermark token: got %v, want ErrStale", err)
	}
	// 介于新旧水位之间的同样拒绝。
	if err := f.Accept(bigger - 1); !errors.Is(err, fence.ErrStale) {
		t.Fatalf("mid token: got %v, want ErrStale", err)
	}
	// 水位本身仍可重复接受。
	if err := f.Accept(bigger); err != nil {
		t.Fatalf("current watermark should still pass: %v", err)
	}
	if got := f.Watermark(); got != bigger {
		t.Fatalf("Watermark() = %d, want %d", got, bigger)
	}
}

func TestStaleErrorCarriesDetails(t *testing.T) {
	f := fence.New()
	f.Grant()
	f.Grant()
	err := f.Accept(1)
	var stale *fence.StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("errors.As failed for %v", err)
	}
	if stale.Token != 1 || stale.Watermark != 2 {
		t.Fatalf("StaleError = %+v, want token 1 watermark 2", stale)
	}
}

func TestConcurrentGrantUnique(t *testing.T) {
	f := fence.New()
	const n = 64
	var wg sync.WaitGroup
	seen := make(chan fence.Token, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			seen <- f.Grant()
		}()
	}
	wg.Wait()
	close(seen)
	uniq := make(map[fence.Token]bool)
	for tok := range seen {
		if uniq[tok] {
			t.Fatalf("duplicate token %d", tok)
		}
		uniq[tok] = true
	}
	if len(uniq) != n {
		t.Fatalf("got %d unique tokens, want %d", len(uniq), n)
	}
}

func TestConcurrentAcceptNoRegression(t *testing.T) {
	f := fence.New()
	const n = 64
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = f.Accept(fence.Token(i))
		}(i)
	}
	wg.Wait()
	if got := f.Watermark(); got != n-1 {
		t.Fatalf("Watermark() = %d, want %d", got, n-1)
	}
}
