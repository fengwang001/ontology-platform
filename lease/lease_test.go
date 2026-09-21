package lease

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

func setup() (*clock, *Lease) {
	c := newClock()
	return c, New(c.Now, fence.New())
}

func TestAcquireMutualExclusion(t *testing.T) {
	_, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	_, err := l.Acquire("bob", time.Minute)
	if !errors.Is(err, ErrHeld) {
		t.Fatalf("expected ErrHeld, got %v", err)
	}
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected *ConflictError, got %T", err)
	}
	if conflict.Holder != "alice" {
		t.Fatalf("conflict should name alice, got %q", conflict.Holder)
	}
	if conflict.Remaining != time.Minute {
		t.Fatalf("conflict remaining should be 1m, got %s", conflict.Remaining)
	}
}

func TestRenewOnlyByHolder(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(10 * time.Second)
	if _, err := l.Renew("bob", time.Minute); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("non-holder renew should fail with ErrNotHolder, got %v", err)
	}
	// 到期时间不得被非持有者改变。
	if got := l.Info().Remaining; got != 50*time.Second {
		t.Fatalf("expiry changed by rejected renew, remaining %s", got)
	}
}

func TestRenewResetsToNowPlusTTL(t *testing.T) {
	c, l := setup()
	if _, err := l.Acquire("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Advance(50 * time.Second)
	if _, err := l.Renew("alice", time.Minute); err != nil {
		t.Fatal(err)
	}
	// 续期是 now+ttl：50s 时续期 60s，剩余应为 60s；累加则只剩 70s。
	if got := l.Info().Remaining; got != time.Minute {
		t.Fatalf("renew should reset expiry to now+ttl, remaining %s", got)
	}
}

func TestRenewKeepsToken(t *testing.T) {
	c, l := setup()
	tok, err := l.Acquire("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	renewed, err := l.Renew("alice", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if renewed != tok {
		t.Fatalf("renew must not change token: got %d, want %d", renewed, tok)
	}
}
