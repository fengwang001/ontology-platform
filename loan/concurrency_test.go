package loan

import (
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	l, err := New(100_000_000, 10_000, 2_000_000)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var waitGroup sync.WaitGroup
	for worker := 0; worker < 32; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for operation := 0; operation < 100; operation++ {
				switch (worker + operation) % 4 {
				case 0:
					_, _ = l.Pay()
				case 1:
					_ = l.SetRate(10_000, 2)
				case 2:
					_ = l.Holiday(1)
				default:
					_ = l.Remaining()
				}
			}
		}(worker)
	}
	waitGroup.Wait()

	l.mu.RLock()
	balance := l.state.balance
	l.mu.RUnlock()
	if balance < 0 {
		t.Fatalf("concurrent result has negative balance=%d", balance)
	}
	if remaining := l.Remaining(); remaining < 0 || remaining > 600 {
		t.Fatalf("concurrent result Remaining()=%d, want 0..600", remaining)
	}
}
