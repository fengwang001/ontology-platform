package budget

import (
	"errors"
	"sync"
	"testing"
)

func TestHardLimitNeverExceeded(t *testing.T) {
	b, err := New(100)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	violation := false
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				if err := b.Charge(10); err != nil {
					return
				}
				mu.Lock()
				if b.Used() > 100 || b.Peak() > 100 {
					violation = true
				}
				mu.Unlock()
				b.Release(10)
			}
		}()
	}
	wg.Wait()
	if violation {
		t.Fatal("resident bytes exceeded hard limit")
	}
}

func TestOversizeRejected(t *testing.T) {
	b, _ := New(10)
	if err := b.Charge(11); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if err := b.Charge(10); err != nil {
		t.Fatal(err)
	}
}

func TestCloseWakesWaiters(t *testing.T) {
	b, _ := New(10)
	if err := b.Charge(10); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.Charge(1) }()
	b.Close()
	if err := <-done; !errors.Is(err, ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", err)
	}
	if err := b.Charge(1); !errors.Is(err, ErrClosed) {
		t.Fatalf("post-close Charge: want ErrClosed, got %v", err)
	}
}

func TestInvalidLimit(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidLimit) {
		t.Fatalf("want ErrInvalidLimit, got %v", err)
	}
}
