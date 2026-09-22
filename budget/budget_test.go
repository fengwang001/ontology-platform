package budget

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestAcquireRelease(t *testing.T) {
	cases := []struct {
		name    string
		limit   int64
		acquire []int64
		wantErr error
	}{
		{"exact fit", 100, []int64{60, 40}, nil},
		{"single too large", 100, []int64{101}, ErrTooLarge},
		{"zero", 100, []int64{0}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := New(tc.limit)
			var err error
			for _, n := range tc.acquire {
				if err = b.Acquire(n); err != nil {
					break
				}
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if b.Used() > b.Limit() {
				t.Fatalf("used %d exceeds limit %d", b.Used(), b.Limit())
			}
		})
	}
}

func TestBlockingAcquireNeverExceeds(t *testing.T) {
	b := New(100)
	if err := b.Acquire(80); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := b.Acquire(50); err != nil { // blocks until release
			t.Error(err)
		}
	}()
	select {
	case <-done:
		t.Fatal("acquire should block while budget exhausted")
	case <-time.After(50 * time.Millisecond):
	}
	if b.Used() > b.Limit() {
		t.Fatalf("used %d exceeds limit", b.Used())
	}
	b.Release(80)
	<-done
	if b.Used() != 50 {
		t.Fatalf("used=%d want 50", b.Used())
	}
	if b.Peak() != 80 {
		t.Fatalf("peak=%d want 80", b.Peak())
	}
}

func TestConcurrentAccounting(t *testing.T) {
	b := New(1000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				if err := b.Acquire(10); err != nil {
					t.Error(err)
					return
				}
				if b.Used() > b.Limit() {
					t.Error("limit exceeded")
					return
				}
				b.Release(10)
			}
		}()
	}
	wg.Wait()
	if b.Used() != 0 {
		t.Fatalf("used=%d want 0", b.Used())
	}
	if b.Peak() > b.Limit() {
		t.Fatalf("peak %d exceeds limit %d", b.Peak(), b.Limit())
	}
}
