package dwellqueue

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	testTarget   = 100 * time.Millisecond
	testInterval = 1000 * time.Millisecond
	testMaxSize  = 10
)

func TestBurstShorterThanObservationDoesNotDrop(t *testing.T) {
	m := newTestManager(t, 100)
	enqueueMany(t, m, 3, 0)

	result := dequeue(t, m, testTarget-time.Millisecond, "dwell is below T")
	if result.Sequence != 1 || len(result.Dropped) != 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if m.firstExceededSet {
		t.Fatal("first-exceeded time was set for a short burst")
	}
}

func TestFirstDropExactlyOneObservationAfterFirstExceedance(t *testing.T) {
	m := newTestManager(t, 200)
	enqueueMany(t, m, 6, 0)

	result := dequeue(t, m, testTarget, "dwell equals T and queue remains over one maximum packet")
	if result.Sequence != 1 || !m.firstExceededSet || m.firstExceededAt != testTarget+testInterval {
		t.Fatalf("first exceedance not scheduled at T+I: %+v", result)
	}

	dequeue(t, m, testTarget+testInterval-time.Millisecond, "still before first-exceeded deadline")
	result = dequeue(t, m, testTarget+testInterval, "deadline reached: drop and enter dropping state")
	if len(result.Dropped) != 1 {
		t.Fatalf("expected one entering drop, got %+v", result)
	}
	if result.Dropped[0].Sequence != 3 || result.Dropped[0].Count != 1 {
		t.Fatalf("unexpected entering drop: %+v", result.Dropped[0])
	}
	if result.Sequence != 4 {
		t.Fatalf("expected next packet after entering drop, got %+v", result)
	}
}

func TestDropIntervalsShrinkBySquareRoot(t *testing.T) {
	m := newTestManager(t, 300)
	enqueueMany(t, m, 12, 0)

	dequeue(t, m, testTarget, "first exceedance starts observation")
	first := dequeue(t, m, testTarget+testInterval, "I elapsed, count 1 drop")
	assertDropped(t, first, 2, 1, testTarget+2*testInterval)

	second := dequeue(t, m, testTarget+2*testInterval, "I/sqrt(1) elapsed")
	assertDropped(t, second, 4, 2, 2807106781*time.Nanosecond)

	third := dequeue(t, m, 2807106781*time.Nanosecond, "I/sqrt(2) elapsed")
	assertDropped(t, third, 6, 3, 3384457050*time.Nanosecond)

	if d := dropInterval(testInterval, 2); d != 707106781*time.Nanosecond {
		t.Fatalf("I/sqrt(2) = %v", d)
	}
	if d := dropInterval(testInterval, 3); d != 577350269*time.Nanosecond {
		t.Fatalf("I/sqrt(3) = %v", d)
	}
}

func TestReentryWithinSixteenIntervalsUsesPreviousCount(t *testing.T) {
	m := newTestManager(t, 300)
	enqueueMany(t, m, 12, 0)

	dequeue(t, m, testTarget, "first exceedance")
	dequeue(t, m, 1100*time.Millisecond, "drop count 1")
	dequeue(t, m, 2100*time.Millisecond, "drop count 2")
	dequeue(t, m, 2807106781*time.Nanosecond, "drop count 3")
	dequeue(t, m, 3384457050*time.Nanosecond, "drop count 4")
	exit := dequeue(t, m, 3961807319*time.Nanosecond, "drop count 5, then last packet is not exceeded")
	if exit.Reason != ReasonExitDropping || len(exit.Dropped) != 1 || exit.Sequence != 11 {
		t.Fatalf("expected count-5 exit, got %+v", exit)
	}

	enqueueMany(t, m, 3, 4000*time.Millisecond)
	dequeue(t, m, 4100*time.Millisecond, "new congestion starts a fresh observation")
	reentry := dequeue(t, m, 5100*time.Millisecond, "reentry within 16I inherits 5-2=3")
	if len(reentry.Dropped) != 1 || reentry.Dropped[0].Count != 3 {
		t.Fatalf("expected inherited count 3, got %+v", reentry)
	}
	if reentry.Dropped[0].NextDropAt != 5677350269*time.Nanosecond {
		t.Fatalf("unexpected inherited next drop: %+v", reentry.Dropped[0])
	}
	if reentry.Dropped[0].Sequence != 13 || reentry.Sequence != 14 {
		t.Fatalf("unexpected reentry packets: %+v", reentry)
	}
}

func TestEmptyDequeueExitsDroppingAndClearsFirstExceedance(t *testing.T) {
	m := newTestManager(t, 100)
	m.dropping = true
	m.count = 4
	m.nextDropAt = 5000 * time.Millisecond
	m.firstExceededAt = 4000 * time.Millisecond
	m.firstExceededSet = true

	result := dequeue(t, m, 6000*time.Millisecond, "empty queue forces dropping-state exit")
	if result.Reason != ReasonEmpty {
		t.Fatalf("expected empty result, got %+v", result)
	}
	assertNormalState(t, m)
	if !m.haveLastExit || m.lastExitCount != 4 || m.lastExitAt != 6000*time.Millisecond {
		t.Fatalf("dropping exit was not recorded: %+v", m)
	}
}

func TestTailDropOnCapacityOverflow(t *testing.T) {
	m := newTestManager(t, 25)

	first := enqueue(t, m, makePacket(10), 0, "first packet fits")
	second := enqueue(t, m, makePacket(10), 0, "second packet fills capacity")
	third := enqueue(t, m, makePacket(10), 0, "third packet exceeds capacity")
	if !first.Accepted || !second.Accepted || third.Accepted || third.Reason != ReasonTailDropped {
		t.Fatalf("unexpected tail-drop results: %+v %+v %+v", first, second, third)
	}
	stats := m.Stats()
	if stats.TailDrops != 1 || stats.Bytes != 20 || stats.Packets != 2 {
		t.Fatalf("tail drop changed queue state: %+v", stats)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	invalid := []struct {
		name     string
		target   time.Duration
		interval time.Duration
		maxSize  int
		capacity int
		want     error
	}{
		{"non-positive parameter", 0, testInterval, 10, 100, ErrNonPositiveParameter},
		{"target equals interval", testInterval, testInterval, 10, 100, ErrTargetTooLarge},
		{"zero maximum packet", testTarget, testInterval, 0, 100, ErrNonPositiveParameter},
		{"zero capacity", testTarget, testInterval, 10, 0, ErrNonPositiveParameter},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.target, tc.interval, tc.maxSize, tc.capacity)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	m := newTestManager(t, 100)
	enqueue(t, m, makePacket(10), 10, "accepted operation establishes clock")
	before := m.Stats()

	if _, err := m.Enqueue(nil, 11); !errors.Is(err, ErrInvalidPacketLength) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Enqueue(makePacket(11), 12); !errors.Is(err, ErrPacketTooLarge) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Enqueue(makePacket(10), 9); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("got %v", err)
	}
	if _, err := m.Dequeue(8); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("got %v", err)
	}
	if after := m.Stats(); before != after {
		t.Fatalf("rejected operation changed stats: before %+v after %+v", before, after)
	}
}

func TestReplayProducesSameDropSequence(t *testing.T) {
	first := replayScenario(t)
	second := replayScenario(t)
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatalf("replay differs:\nfirst=%v\nsecond=%v", first, second)
	}
}

func TestConcurrentInvariant(t *testing.T) {
	m := newTestManager(t, 500)
	var producers sync.WaitGroup
	var consumers sync.WaitGroup
	stopConsumers := make(chan struct{})

	for worker := 0; worker < 8; worker++ {
		consumers.Add(1)
		go func() {
			defer consumers.Done()
			for {
				select {
				case <-stopConsumers:
					return
				default:
					result, err := m.dequeue(0, true)
					if err != nil {
						t.Errorf("dequeue: %v", err)
						return
					}
					if result.Reason == ReasonEmpty {
						runtime.Gosched()
					}
				}
			}
		}()
	}

	for worker := 0; worker < 8; worker++ {
		producers.Add(1)
		go func() {
			defer producers.Done()
			for i := 0; i < 20; i++ {
				if _, err := m.enqueue(makePacket(10), 0, true); err != nil {
					t.Errorf("enqueue: %v", err)
				}
			}
		}()
	}

	producers.Wait()
	for {
		stats := m.Stats()
		if stats.Packets == 0 {
			break
		}
		if _, err := m.dequeue(0, true); err != nil {
			t.Fatal(err)
		}
	}
	close(stopConsumers)
	consumers.Wait()

	stats := m.Stats()
	if stats.EnqueueSuccesses != stats.Dequeues+stats.ActiveDrops+uint64(stats.Packets) {
		t.Fatalf("invariant violated: %+v", stats)
	}
}

type replayRecord struct {
	accepted []uint64
	returned []uint64
	dropped  []uint64
}

func replayScenario(t *testing.T) replayRecord {
	t.Helper()
	m := newTestManager(t, 300)
	record := replayRecord{}

	for i := 0; i < 12; i++ {
		result := enqueue(t, m, makePacket(testMaxSize), 0, "replay enqueue")
		record.accepted = append(record.accepted, result.Sequence)
	}
	for _, now := range []time.Duration{100, 1100, 2100, 2807, 3384, 3887} {
		result := dequeue(t, m, now*time.Millisecond, "replay congestion timeline")
		if result.Sequence != 0 {
			record.returned = append(record.returned, result.Sequence)
		}
		for _, decision := range result.Dropped {
			record.dropped = append(record.dropped, decision.Sequence)
		}
	}
	return record
}

func newTestManager(t *testing.T, capacity int) *Manager {
	t.Helper()
	m, err := New(testTarget, testInterval, testMaxSize, capacity)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func enqueueMany(t *testing.T, m *Manager, count int, now time.Duration) {
	t.Helper()
	for i := 0; i < count; i++ {
		enqueue(t, m, makePacket(testMaxSize), now, fmt.Sprintf("seed packet %d", i+1))
	}
}

func makePacket(length int) []byte {
	return make([]byte, length)
}

func enqueue(t *testing.T, m *Manager, data []byte, now time.Duration, basis string) EnqueueResult {
	t.Helper()
	result, err := m.Enqueue(data, now)
	t.Logf("INPUT  Enqueue len=%d now=%v basis=%q | OUTPUT seq=%d accepted=%t reason=%s bytes=%d err=%v",
		len(data), now, basis, result.Sequence, result.Accepted, result.Reason, result.Bytes, err)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return result
}

func dequeue(t *testing.T, m *Manager, now time.Duration, basis string) DequeueResult {
	t.Helper()
	result, err := m.Dequeue(now)
	t.Logf("INPUT  Dequeue now=%v basis=%q | OUTPUT seq=%d dwell=%v reason=%s count=%d next=%v firstOver=%v bytes=%d dropped=%v err=%v",
		now, basis, result.Sequence, result.DwellTime, result.Reason, result.Count, result.NextDropAt,
		result.FirstOverAt, result.Bytes, result.Dropped, err)
	if err != nil {
		t.Fatalf("dequeue: %v", err)
	}
	return result
}

func assertDropped(t *testing.T, result DequeueResult, sequence uint64, count int, next time.Duration) {
	t.Helper()
	if len(result.Dropped) == 0 {
		t.Fatalf("expected sequence %d to be dropped, got %+v", sequence, result)
	}
	decision := result.Dropped[len(result.Dropped)-1]
	if decision.Sequence != sequence || decision.Count != count || decision.NextDropAt != next {
		t.Fatalf("unexpected drop decision: got %+v, want sequence=%d count=%d next=%v",
			decision, sequence, count, next)
	}
}

func assertNormalState(t *testing.T, m *Manager) {
	t.Helper()
	stats := m.Stats()
	if stats.Dropping || stats.Count != 0 || stats.Bytes != 0 || stats.Packets != 0 {
		t.Fatalf("normal state not restored: %+v", stats)
	}
	if m.firstExceededSet || m.nextDropAt != 0 {
		t.Fatalf("scheduled state was not cleared: %+v", m)
	}
}
