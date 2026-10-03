package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentCallsAreSerializable(t *testing.T) {
	tracker := mustNewTracker(t, 2)
	if err := tracker.Send([]byte("m"), [][]byte{[]byte("A"), []byte("B")}, 50); err != nil {
		t.Fatal(err)
	}

	var wait sync.WaitGroup
	for worker := range 16 {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for attempt := 1; attempt <= 16; attempt++ {
				kind := []ReceiptKind{Sent, Delivered, Read, Soft, Hard}[worker%5]
				_, _ = tracker.Receipt([]byte("m"), []byte("A"), kind, attempt, int64(attempt))
				_, _ = tracker.Tick(int64(attempt))
				_, _ = tracker.Status([]byte("m"))
			}
		}(worker)
	}

	wait.Add(1)
	go func() {
		defer wait.Done()
		for i := 1; i <= 16; i++ {
			_, _ = tracker.Receipt([]byte("m"), []byte("B"), Soft, i, int64(i))
		}
	}()

	wait.Wait()

	status, err := tracker.Status([]byte("m"))
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Recipients) != 2 {
		t.Fatalf("recipient count = %d, want 2", len(status.Recipients))
	}
}
