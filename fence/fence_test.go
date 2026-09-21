package fence

import (
	"errors"
	"sync"
	"testing"
)

func TestIssueStrictlyIncreasing(t *testing.T) {
	f := New()
	prev := Token(0)
	for i := 0; i < 100; i++ {
		tok := f.Issue()
		if tok <= prev {
			t.Fatalf("token %d not greater than previous %d", tok, prev)
		}
		prev = tok
	}
	if prev != 100 {
		t.Fatalf("expected last token 100, got %d", prev)
	}
}

func TestCheckRejectsStaleToken(t *testing.T) {
	f := New()
	issued := f.Issue() // 1，水位抬到 1
	if err := f.Check(issued); err != nil {
		t.Fatalf("current token should be accepted: %v", err)
	}
	if err := f.Check(issued); err != nil {
		t.Fatalf("equal to watermark should be accepted: %v", err)
	}
}

func TestCheckWatermarkNeverRegresses(t *testing.T) {
	f := New()
	f.Issue()         // 1
	high := f.Issue() // 2
	if err := f.Check(high); err != nil {
		t.Fatalf("token %d should be accepted: %v", high, err)
	}
	// 水位已被抬到 2，等于旧水位 1 的令牌也要拒绝。
	err := f.Check(1)
	if !errors.Is(err, ErrStaleToken) {
		t.Fatalf("expected ErrStaleToken, got %v", err)
	}
	var stale *StaleError
	if !errors.As(err, &stale) {
		t.Fatalf("expected *StaleError, got %T", err)
	}
	if stale.Token != 1 || stale.Watermark != 2 {
		t.Fatalf("unexpected stale detail: %+v", stale)
	}
	// 等于当前水位仍可接受。
	if err := f.Check(2); err != nil {
		t.Fatalf("equal to current watermark should be accepted: %v", err)
	}
}

func TestMaxTracksIssuedAndAccepted(t *testing.T) {
	f := New()
	if got := f.Max(); got != 0 {
		t.Fatalf("initial max should be 0, got %d", got)
	}
	f.Issue()
	f.Issue()
	if got := f.Max(); got != 2 {
		t.Fatalf("max should be 2, got %d", got)
	}
	if err := f.Check(5); err != nil {
		t.Fatalf("larger token should be accepted: %v", err)
	}
	if got := f.Max(); got != 5 {
		t.Fatalf("max should be raised to 5, got %d", got)
	}
}

func TestConcurrentIssueUnique(t *testing.T) {
	f := New()
	const n = 64
	var wg sync.WaitGroup
	tokens := make([]Token, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i] = f.Issue()
		}(i)
	}
	wg.Wait()
	seen := make(map[Token]bool, n)
	for _, tok := range tokens {
		if tok == 0 || seen[tok] {
			t.Fatalf("duplicate or zero token %d", tok)
		}
		seen[tok] = true
	}
}

func TestConcurrentCheckNoRegression(t *testing.T) {
	f := New()
	const n = 64
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(tok Token) {
			defer wg.Done()
			_ = f.Check(tok)
		}(Token(i))
	}
	wg.Wait()
	if got := f.Max(); got != n {
		t.Fatalf("watermark should be %d after concurrent checks, got %d", n, got)
	}
}
