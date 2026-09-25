package bulkhead

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/timeout"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 2000; i++ {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		n, q   int
		wantOK bool
	}{
		{1, 0, true}, {8, 100, true}, {0, 1, false}, {-1, 1, false}, {1, -1, false},
	}
	for _, c := range cases {
		_, err := New(c.n, c.q, nil)
		if gotOK := err == nil; gotOK != c.wantOK {
			t.Errorf("New(%d, %d): ok=%v, want %v", c.n, c.q, gotOK, c.wantOK)
		}
	}
}

func TestPeakUnderConcurrency(t *testing.T) {
	bh, _ := New(8, 500, nil)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := bh.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
			bh.Release()
		}()
	}
	close(start)
	wg.Wait()
	if bh.Peak() != 8 {
		t.Fatalf("peak %d, want exactly 8 (never above limit)", bh.Peak())
	}
}

func TestQueueFullRejectsImmediately(t *testing.T) {
	c := &fakeClock{t: time.Unix(100, 0)}
	bh, _ := New(1, 1, c.Now)
	if err := bh.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { bh.Acquire(ctx) }() //nolint:errcheck
	waitFor(t, func() bool { return bh.Waiting() == 1 })
	t0 := c.Now()
	err := bh.Acquire(context.Background())
	if !errors.Is(err, ErrFull) {
		t.Fatalf("err=%v, want ErrFull", err)
	}
	if !c.Now().Equal(t0) {
		t.Fatal("injected clock advanced on rejection")
	}
	if !bh.LastReject().Equal(t0) {
		t.Fatal("LastReject not stamped with injected clock")
	}
}

func TestCancelReleasesQueueSlot(t *testing.T) {
	bh, _ := New(1, 1, nil)
	if err := bh.Acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- bh.Acquire(ctx) }()
	waitFor(t, func() bool { return bh.Waiting() == 1 })
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	waitFor(t, func() bool { return bh.Waiting() == 0 })
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { bh.Acquire(ctx2) }() //nolint:errcheck
	waitFor(t, func() bool { return bh.Waiting() == 1 })
	bh.Release()
	waitFor(t, func() bool { return bh.Waiting() == 0 && bh.InFlight() == 1 })
}

func TestSerialWhenLimitOne(t *testing.T) {
	bh, _ := New(1, 64, nil)
	var wg sync.WaitGroup
	counter := 0
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := bh.Acquire(context.Background()); err != nil {
				t.Errorf("Acquire: %v", err)
				return
			}
			counter++
			bh.Release()
		}()
	}
	wg.Wait()
	if counter != 50 || bh.Peak() != 1 {
		t.Fatalf("counter=%d peak=%d, want 50 and 1", counter, bh.Peak())
	}
}

func TestIsolationBetweenDownstreams(t *testing.T) {
	bx, _ := New(2, 0, nil)
	by, _ := New(2, 0, nil)
	for i := 0; i < 2; i++ {
		if err := bx.Acquire(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := bx.Acquire(ctx); err == nil {
		t.Fatal("X should be exhausted")
	}
	for i := 0; i < 100; i++ {
		if err := by.Acquire(context.Background()); err != nil {
			t.Fatalf("Y affected by X: %v", err)
		}
		by.Release()
	}
}

func TestReleaseOnAllPaths(t *testing.T) {
	paths := []struct {
		name string
		fn   func(context.Context) error
		want func(error) bool
	}{
		{"success", func(context.Context) error { return nil },
			func(err error) bool { return err == nil }},
		{"failure", func(context.Context) error { return errors.New("boom") },
			func(err error) bool { return err != nil && err.Error() == "boom" }},
		{"timeout", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			func(err error) bool { return errors.Is(err, timeout.ErrTimeout) }},
		{"panic", func(context.Context) error { panic("bang") },
			func(err error) bool {
				var pe *timeout.PanicError
				return errors.As(err, &pe)
			}},
	}
	for _, p := range paths {
		t.Run(p.name, func(t *testing.T) {
			bh, _ := New(4, 4, nil)
			for i := 0; i < 1000; i++ {
				if err := bh.Acquire(context.Background()); err != nil {
					t.Fatal(err)
				}
				err := timeout.Do(context.Background(), time.Millisecond, p.fn)
				bh.Release()
				if !p.want(err) {
					t.Fatalf("path %s: unexpected err %v", p.name, err)
				}
			}
			if bh.InFlight() != 0 || bh.Waiting() != 0 {
				t.Fatalf("path %s: leaked permits, in=%d wait=%d",
					p.name, bh.InFlight(), bh.Waiting())
			}
		})
	}
}
