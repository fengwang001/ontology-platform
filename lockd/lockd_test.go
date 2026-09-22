package lockd

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/fence"
)

// clock 是测试用的手动时钟。
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock {
	return &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func setup() (*clock, *Service) {
	c := newClock()
	return c, New(c.Now)
}

func TestWriteRejectsStaleToken(t *testing.T) {
	c, s := setup()
	old, err := s.Acquire("res", "alice", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(2 * time.Second) // alice 的租约过期
	current, err := s.Acquire("res", "bob", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// 旧持有者拿着过期令牌写入：必须拒绝，且可与「无人持有」区分。
	err = s.Write("res", old, []byte("stale"))
	if !errors.Is(err, ErrStaleToken) {
		t.Fatalf("expected ErrStaleToken, got %v", err)
	}
	if errors.Is(err, ErrNotHeld) {
		t.Fatal("stale-token error must be distinguishable from not-held")
	}
	if err := s.Write("res", current, []byte("fresh")); err != nil {
		t.Fatalf("current token should be accepted: %v", err)
	}
}

func TestWriteWithoutHolder(t *testing.T) {
	_, s := setup()
	err := s.Write("res", 1, []byte("data"))
	if !errors.Is(err, ErrNotHeld) {
		t.Fatalf("expected ErrNotHeld, got %v", err)
	}
	if errors.Is(err, ErrStaleToken) {
		t.Fatal("not-held error must be distinguishable from stale-token")
	}
}

func TestWriteWatermarkNeverRegresses(t *testing.T) {
	_, s := setup()
	tok, err := s.Acquire("res", "alice", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// 一个更大的已见令牌把水位抬上去。
	if err := s.Write("res", tok+10, []byte("high")); err != nil {
		t.Fatal(err)
	}
	// 此后等于旧水位（当前有效令牌）的写入也要拒绝。
	if err := s.Write("res", tok, []byte("old-level")); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("token equal to old watermark should be rejected, got %v", err)
	}
	// 等于新水位仍可接受。
	if err := s.Write("res", tok+10, []byte("same-level")); err != nil {
		t.Fatalf("token equal to current watermark should be accepted: %v", err)
	}
}

func TestTokensIndependentAcrossResources(t *testing.T) {
	_, s := setup()
	a1, err := s.Acquire("a", "alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	b1, err := s.Acquire("b", "bob", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if a1 != b1 {
		t.Fatalf("independent resources should each start at 1, got %d and %d", a1, b1)
	}
}

func TestInfoZeroValueWhenNotHeld(t *testing.T) {
	c, s := setup()
	if _, err := s.Acquire("res", "alice", time.Second); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Minute)
	info := s.Info("res")
	if info.Held || info.Holder != "" || info.Remaining != 0 || info.Token != 0 {
		t.Fatalf("expired info should be zero value, got %+v", info)
	}
	if info := s.Info("missing"); info.Held || info.Holder != "" || info.Remaining != 0 || info.Token != 0 {
		t.Fatalf("unknown resource info should be zero value, got %+v", info)
	}
}

func TestConcurrentAcquireExactlyOneWinner(t *testing.T) {
	_, s := setup()
	const n = 64
	var wg sync.WaitGroup
	tokens := make([]fence.Token, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tokens[i], errs[i] = s.Acquire("res", "client", time.Minute)
		}(i)
	}
	wg.Wait()
	wins := 0
	var winToken fence.Token
	for i := range errs {
		if errs[i] == nil {
			wins++
			winToken = tokens[i]
		} else if !errors.Is(errs[i], ErrHeld) {
			t.Fatalf("loser should get ErrHeld, got %v", errs[i])
		}
	}
	if wins != 1 {
		t.Fatalf("expected exactly 1 winner, got %d", wins)
	}
	if winToken != 1 {
		t.Fatalf("first winner token should be 1, got %d", winToken)
	}
}

func TestConcurrentWriteNoRegression(t *testing.T) {
	_, s := setup()
	if _, err := s.Acquire("res", "alice", time.Hour); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	for i := 1; i <= n; i++ {
		wg.Add(1)
		go func(tok fence.Token) {
			defer wg.Done()
			_ = s.Write("res", tok, []byte("x"))
		}(fence.Token(i))
	}
	wg.Wait()
	// 水位应停在本轮最大值：更小的写入全部被拒，最大值可重复接受。
	if err := s.Write("res", n-1, nil); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("token below watermark should be rejected, got %v", err)
	}
	if err := s.Write("res", n, nil); err != nil {
		t.Fatalf("token at watermark should be accepted: %v", err)
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	c, s := setup()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				c.Advance(time.Millisecond)
				tok, err := s.Acquire("res", "worker", 10*time.Millisecond)
				if err != nil {
					continue
				}
				_, _ = s.Renew("res", "worker", 10*time.Millisecond)
				_ = s.Write("res", tok, []byte("x"))
				_ = s.Release("res", "worker")
			}
		}()
	}
	wg.Wait()
}
