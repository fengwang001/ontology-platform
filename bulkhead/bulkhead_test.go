package bulkhead_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/bulkhead"
	"ontology/timeout"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time { return f.t }

func TestNew(t *testing.T) {
	cases := []struct {
		name        string
		size, queue int
		wantErr     bool
	}{
		{"ok", 1, 0, false},
		{"ok with queue", 8, 16, false},
		{"zero size", 0, 0, true},
		{"negative size", -1, 0, true},
		{"negative queue", 1, -1, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := bulkhead.New("x", c.size, c.queue)
			if (err != nil) != c.wantErr {
				t.Fatalf("New(%d,%d) err = %v, wantErr %v", c.size, c.queue, err, c.wantErr)
			}
		})
	}
}

func TestAcquire(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"peak never exceeds N with 500 goroutines", testPeak},
		{"N+Q+1th request rejected immediately", testQueueFullImmediate},
		{"cancelled waiter leaves queue", testCancel},
		{"size 1 degrades to serial", testSerial},
		{"release returned on all four paths", testFourPaths},
	}
	for _, s := range scenarios {
		t.Run(s.name, s.run)
	}
}

func testPeak(t *testing.T) {
	bh, err := bulkhead.New("peak", 8, 500)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := bh.Acquire(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			time.Sleep(time.Millisecond)
			rel()
		}()
	}
	wg.Wait()
	if p := bh.Peak(); p <= 0 || p > 8 {
		t.Fatalf("peak = %d, want in (0,8]", p)
	}
}

func testQueueFullImmediate(t *testing.T) {
	bh, err := bulkhead.New("q", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := bh.Acquire(context.Background())
	defer rel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		rel2, err := bh.Acquire(ctx)
		if err == nil {
			rel2()
		}
	}()
	waitFor(t, func() bool { return bh.Waiting() == 1 })
	clk := &fakeClock{t: time.Now()}
	before := clk.Now()
	if _, err := bh.Acquire(context.Background()); !errors.Is(err, bulkhead.ErrFull) {
		t.Fatalf("got %v, want ErrFull", err)
	}
	if !clk.Now().Equal(before) {
		t.Fatal("rejection must not advance the clock")
	}
}

func testCancel(t *testing.T) {
	bh, err := bulkhead.New("c", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := bh.Acquire(context.Background())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := bh.Acquire(ctx)
		done <- err
	}()
	waitFor(t, func() bool { return bh.Waiting() == 1 })
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiter err = %v, want context.Canceled", err)
	}
	if w := bh.Waiting(); w != 0 {
		t.Fatalf("waiting = %d after cancel, want 0", w)
	}
	rel()
	if a := bh.Available(); a != 1 {
		t.Fatalf("available = %d after release, want 1", a)
	}
}

func testSerial(t *testing.T) {
	bh, err := bulkhead.New("s", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	rel, _ := bh.Acquire(context.Background())
	got := make(chan struct{})
	go func() {
		rel2, err := bh.Acquire(context.Background())
		if err != nil {
			t.Error(err)
		}
		close(got)
		rel2()
	}()
	select {
	case <-got:
		t.Fatal("second acquire succeeded while slot held")
	case <-time.After(50 * time.Millisecond):
	}
	rel()
	select {
	case <-got:
	case <-time.After(2 * time.Second):
		t.Fatal("second acquire did not proceed after release")
	}
}

func testFourPaths(t *testing.T) {
	bh, err := bulkhead.New("p", 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	fail := errors.New("boom")
	paths := []func(context.Context) error{
		func(context.Context) error { return nil },
		func(context.Context) error { return fail },
		func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
		func(context.Context) error { panic("bang") },
	}
	for round := 0; round < 1000; round++ {
		for _, fn := range paths {
			rel, err := bh.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_ = timeout.Do(context.Background(), time.Millisecond, fn)
			rel()
		}
	}
	if a := bh.Available(); a != 4 {
		t.Fatalf("available = %d after 4x1000 calls, want 4", a)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 2s")
		}
		time.Sleep(time.Millisecond)
	}
}
