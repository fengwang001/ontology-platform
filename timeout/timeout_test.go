package timeout_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	tmo "ontology/timeout"
)

type fakeTimer struct {
	c       chan time.Time
	fire    time.Time
	stopped bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }
func (t *fakeTimer) Stop() bool {
	t.stopped = true
	return true
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1000, 0)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) NewTimer(d time.Duration) tmo.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	ft := &fakeTimer{c: make(chan time.Time, 1), fire: c.now.Add(d)}
	c.timers = append(c.timers, ft)
	return ft
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
		fired := append([]*fakeTimer(nil), c.timers...)
		c.mu.Unlock()
		for _, ft := range fired {
			c.mu.Lock()
			due := !ft.fire.After(c.now)
			c.mu.Unlock()
		if due && !ft.stopped {
			select {
			case ft.c <- c.Now():
			default:
			}
		}
	}
}

func TestTimeout(t *testing.T) {
	cases := []struct {
		name    string
		clock   func() *fakeClock
		d       time.Duration
		fn      func(ctx context.Context) error
		advance time.Duration
		wantIs  error
		wantNil bool
	}{
		{"success", newFakeClock, time.Second,
			func(ctx context.Context) error { return nil }, 0, nil, true},
		{"error-passthrough", newFakeClock, time.Second,
			func(ctx context.Context) error { return errors.New("boom") }, 0, nil, false},
		{"timeout", newFakeClock, time.Second,
			func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}, 2 * time.Second, tmo.ErrTimedOut, false},
		{"panic-captured", newFakeClock, time.Second,
			func(ctx context.Context) error { panic("kaboom") }, 0, tmo.ErrPanic, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := tc.clock()
			started := make(chan struct{})
			fn := tc.fn
			if tc.advance > 0 {
				fn = func(ctx context.Context) error {
					close(started)
					return tc.fn(ctx)
				}
				go func() {
					<-started
					fc.advance(tc.advance)
				}()
			}
			err := tmo.Run(context.Background(), fc, tc.d, fn)
			if tc.wantNil {
				if err != nil {
					t.Fatalf("err=%v want nil", err)
				}
				return
			}
			if err == nil || !errors.Is(err, tc.wantIs) {
				t.Fatalf("err=%v want *%v", err, tc.wantIs)
			}
		})
	}
}

func TestRealClock(t *testing.T) {
	cases := []struct {
		name   string
		d      time.Duration
		fn     func(ctx context.Context) error
		wantIs error
	}{
		{"fast-ok", 100 * time.Millisecond, func(ctx context.Context) error { return nil }, nil},
		{"slow-timeout", 5 * time.Millisecond,
			func(ctx context.Context) error {
				select {
				case <-time.After(50 * time.Millisecond):
					return nil
				case <-ctx.Done():
					return ctx.Err()
				}
			}, tmo.ErrTimedOut},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tmo.Run(context.Background(), tmo.RealClock{}, tc.d, tc.fn)
			if tc.wantIs == nil && err != nil {
				t.Fatalf("err=%v want nil", err)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err=%v want %v", err, tc.wantIs)
			}
		})
	}
}
