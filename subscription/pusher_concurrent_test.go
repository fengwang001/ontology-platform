package subscription

import (
	"bytes"
	"log/slog"
	"sync"
	"testing"
)

func collectChanges(updates <-chan Change[int], stop <-chan struct{}) []Change[int] {
	received := make([]Change[int], 0)
	for {
		select {
		case change := <-updates:
			received = append(received, change)
		case <-stop:
			return received
		}
	}
}

func TestConcurrentPushSequenceAndDeliverySet(t *testing.T) {
	t.Parallel()

	const goroutines = 32
	const pushesPerGoroutine = 100
	const totalPushes = goroutines * pushesPerGoroutine

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	pusher := NewPusherWithLogger[int](logger)

	allUpdates := make(chan Change[int], totalPushes)
	oddUpdates := make(chan Change[int], totalPushes)
	lateUpdates := make(chan Change[int], totalPushes)

	if err := pusher.Register("all", 1, allUpdates); err != nil {
		t.Fatalf("Register(all) error = %v", err)
	}
	if err := pusher.Register("odd", 3, oddUpdates); err != nil {
		t.Fatalf("Register(odd) error = %v", err)
	}

	stopCollectors := make(chan struct{})
	var collectors sync.WaitGroup
	collectors.Add(2)
	var allReceived []Change[int]
	var oddReceived []Change[int]
	go func() {
		defer collectors.Done()
		allReceived = collectChanges(allUpdates, stopCollectors)
	}()
	go func() {
		defer collectors.Done()
		oddReceived = collectChanges(oddUpdates, stopCollectors)
	}()

	readerStop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-readerStop:
					return
				default:
					_ = pusher.Snapshot().CurrentSequence
					_, _ = pusher.DeliveredThrough("all")
					_, _ = pusher.DeliveredThrough("odd")
				}
			}
		}()
	}

	var pushers sync.WaitGroup
	for worker := range goroutines {
		pushers.Add(1)
		go func(worker int) {
			defer pushers.Done()
			for index := range pushesPerGoroutine {
				pusher.Push(3 + 2*(worker*pushesPerGoroutine+index))
			}
		}(worker)
	}
	pushers.Wait()
	close(readerStop)
	readers.Wait()

	if got := pusher.CurrentSequence(); got != totalPushes {
		t.Fatalf("CurrentSequence = %d, want %d", got, totalPushes)
	}

	if err := pusher.Register("late", 1, lateUpdates); err != nil {
		t.Fatalf("Register(late) error = %v", err)
	}
	if len(lateUpdates) != 0 {
		t.Fatalf("late subscription received %d historical changes, want 0", len(lateUpdates))
	}

	close(stopCollectors)
	collectors.Wait()

	assertExactDeliverySet := func(name string, received []Change[int], lowerBound int) {
		t.Helper()
		if len(received) != totalPushes {
			t.Fatalf("%s delivery count = %d, want %d", name, len(received), totalPushes)
		}
		seen := make(map[uint64]int, totalPushes)
		for _, change := range received {
			if change.Key < lowerBound {
				t.Fatalf("%s received non-matching change %+v", name, change)
			}
			seen[change.Sequence]++
		}
		for sequence := uint64(1); sequence <= totalPushes; sequence++ {
			if seen[sequence] != 1 {
				t.Fatalf("%s sequence %d count = %d, want 1", name, sequence, seen[sequence])
			}
		}
	}
	assertExactDeliverySet("all", allReceived, 1)
	assertExactDeliverySet("odd", oddReceived, 3)

	allDelivered, exists := pusher.DeliveredThrough("all")
	if !exists || allDelivered != totalPushes {
		t.Fatalf("all DeliveredThrough = (%d, %v), want (%d, true)", allDelivered, exists, totalPushes)
	}
	oddDelivered, exists := pusher.DeliveredThrough("odd")
	if !exists || oddDelivered != totalPushes {
		t.Fatalf("odd DeliveredThrough = (%d, %v), want (%d, true)", oddDelivered, exists, totalPushes)
	}

	t.Logf("input: %d goroutines x %d odd-key changes; result: current sequence %d and each active subscription got its exact matching set; decision: key >= lower_bound and start_after applies\n%s", goroutines, pushesPerGoroutine, totalPushes, logs.String())
}

func TestConcurrentUnsubscribeStopsDelivery(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logs, nil))
	pusher := NewPusherWithLogger[int](logger)
	updates := make(chan Change[int], 8)

	if err := pusher.Register("alpha", 1, updates); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	var workers sync.WaitGroup
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 20 {
				pusher.Push(1)
			}
		}()
	}

	if err := pusher.Unsubscribe("alpha"); err != nil {
		t.Fatalf("Unsubscribe() error = %v", err)
	}
	workers.Wait()

	afterUnsubscribeStart := pusher.CurrentSequence()
	afterUnsubscribe := pusher.Push(1)
	if afterUnsubscribe.Sequence <= afterUnsubscribeStart {
		t.Fatalf("after-unsubscribe sequence = %d, want > %d", afterUnsubscribe.Sequence, afterUnsubscribeStart)
	}

	deliveredBefore := len(updates)
	pusher.Push(1)
	if len(updates) != deliveredBefore {
		t.Fatalf("old channel deliveries changed after unsubscribe: before=%d after=%d", deliveredBefore, len(updates))
	}

	if err := pusher.Register("alpha", 1, updates); err != nil {
		t.Fatalf("re-Register() error = %v", err)
	}

	t.Logf("input: concurrent pushes with unsubscribe; result: no delivery after unsubscribe and id can be reused; decision: inactive subscriptions are skipped\n%s", logs.String())
}
