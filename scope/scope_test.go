package scope

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动拨动的注入时钟，测试中绝不触碰真实时间。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// assertEnded 确认作用域的 Done 已关闭。
func assertEnded(t *testing.T, s *Scope) {
	t.Helper()
	select {
	case <-s.Done():
	default:
		t.Fatalf("scope not ended: Done channel open")
	}
}

// assertOpen 确认作用域尚未结束。
func assertOpen(t *testing.T, s *Scope) {
	t.Helper()
	select {
	case <-s.Done():
		t.Fatalf("scope unexpectedly ended: err=%v", s.Err())
	default:
	}
}

func assertErrIs(t *testing.T, s *Scope, target error) {
	t.Helper()
	if !errors.Is(s.Err(), target) {
		t.Fatalf("Err()=%v, want errors.Is(..., %v)", s.Err(), target)
	}
}
