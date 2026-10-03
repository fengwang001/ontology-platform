package scheduler

import (
	"errors"
	"sync"
	"testing"
)

func testScheduler(t *testing.T, config Config) *Scheduler {
	t.Helper()
	scheduler, err := New(config)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return scheduler
}

func assertDeliveries(t *testing.T, got []Delivery, want ...Delivery) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("deliveries = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("delivery %d = %#v, want %#v; all got = %#v", i, got[i], want[i], got)
		}
	}
}

func TestQuietBoundaries(t *testing.T) {
	withinDay := testScheduler(t, Config{QuietStart: 10, QuietEnd: 60, DailyCap: 1, MaxPending: 10})
	crossMidnight := testScheduler(t, Config{QuietStart: 1320, QuietEnd: 420, DailyCap: 1, MaxPending: 10})

	if !withinDay.inQuiet(10) || withinDay.inQuiet(60) {
		t.Fatalf("ordinary quiet boundary failed: qs included, qe excluded")
	}
	if !crossMidnight.inQuiet(1320) || crossMidnight.inQuiet(420) || !crossMidnight.inQuiet(1439) || !crossMidnight.inQuiet(0) {
		t.Fatalf("cross-midnight quiet boundary failed")
	}
}

func TestMergeWindowShiftedAfterAddingWindow(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 10, QuietEnd: 60, MergeWindow: 20, DailyCap: 1, MaxPending: 10})
	if _, err := scheduler.Submit("a", 0, 50); err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	if got := scheduler.pending[0].fireAt; got != 70 {
		t.Fatalf("fireAt = %d, want 70 (now+G before shift)", got)
	}
}

func TestZeroWindowStartingInsideQuiet(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 10, QuietEnd: 60, MergeWindow: 0, DailyCap: 1, MaxPending: 10})
	got, err := scheduler.Submit("a", 0, 40)
	if err != nil {
		t.Fatalf("Submit() error = %v", err)
	}
	assertDeliveries(t, got)
	assertDeliveries(t, mustPoll(t, scheduler, 60), Delivery{Key: "a", Count: 1, At: 60})
}

func TestMergedQuietBatchAndImportantBatch(t *testing.T) {
	scheduler := testScheduler(t, Config{OffsetMinutes: 480, QuietStart: 1320, QuietEnd: 420, MergeWindow: 30, DailyCap: 2, MaxPending: 10})
	mustSubmit(t, scheduler, "a", 0, 830)
	mustSubmit(t, scheduler, "a", 0, 900)
	mustSubmit(t, scheduler, "b", 1, 1000)
	assertDeliveries(t, mustSubmit(t, scheduler, "u", 2, 1100), Delivery{Key: "u", Count: 1, At: 1100})

	assertDeliveries(t, mustPoll(t, scheduler, 1380), Delivery{Key: "a", Count: 2, At: 1380})
	assertDeliveries(t, mustPoll(t, scheduler, 2820), Delivery{Key: "b", Count: 1, At: 2820})
}

func TestDueAtNowProcessedBeforeMerge(t *testing.T) {
	scheduler := testScheduler(t, Config{DailyCap: 10, MaxPending: 10, MergeWindow: 0})
	mustSubmit(t, scheduler, "a", 0, 0)
	assertDeliveries(t, mustSubmit(t, scheduler, "a", 0, 0), Delivery{Key: "a", Count: 1, At: 0})
	assertDeliveries(t, mustPoll(t, scheduler, 0), Delivery{Key: "a", Count: 1, At: 0})
}

func TestUrgentConsumesDailyCapAndSecondShiftQuiet(t *testing.T) {
	scheduler := testScheduler(t, Config{OffsetMinutes: 480, QuietStart: 1320, QuietEnd: 420, MergeWindow: 30, DailyCap: 2, MaxPending: 10})
	mustSubmit(t, scheduler, "a", 0, 830)
	mustSubmit(t, scheduler, "a", 0, 900)
	mustSubmit(t, scheduler, "b", 1, 1000)
	mustSubmit(t, scheduler, "u", 2, 1100)

	assertDeliveries(t, mustPoll(t, scheduler, 1380), Delivery{Key: "a", Count: 2, At: 1380})
	assertDeliveries(t, mustPoll(t, scheduler, 2820), Delivery{Key: "b", Count: 1, At: 2820})
}

func TestNextLocalMidnightCanNeedSecondQuietShift(t *testing.T) {
	scheduler := testScheduler(t, Config{OffsetMinutes: 60, QuietStart: 0, QuietEnd: 30, MergeWindow: 0, DailyCap: 1, MaxPending: 10})
	assertDeliveries(t, mustSubmit(t, scheduler, "u", 2, 1380), Delivery{Key: "u", Count: 1, At: 1380})
	mustSubmit(t, scheduler, "a", 0, 1380)
	assertDeliveries(t, mustPoll(t, scheduler, 2820))
	assertDeliveries(t, mustPoll(t, scheduler, 2850), Delivery{Key: "a", Count: 1, At: 2850})
}

func TestPollCascadesAcrossMultipleDays(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 0, QuietEnd: 0, MergeWindow: 0, DailyCap: 1, MaxPending: 10})
	scheduler.sent[0] = 1
	scheduler.sent[1] = 1
	scheduler.sent[2] = 1
	scheduler.pushBatch(&batch{key: "a", count: 1, fireAt: 0, seq: 1})
	assertDeliveries(t, mustPoll(t, scheduler, 4320),
		Delivery{Key: "a", Count: 1, At: 4320},
	)
	if got := scheduler.batchDeferred; got != 3 {
		t.Fatalf("deferred = %d, want 3", got)
	}
}

func TestNegativeOffsetUsesFloorDay(t *testing.T) {
	scheduler := testScheduler(t, Config{OffsetMinutes: -120, MergeWindow: 0, DailyCap: 1, MaxPending: 10})
	if got := localDay(120, -120); got != 0 {
		t.Fatalf("localDay(120) = %d, want 0", got)
	}
	if got := localDay(0, -120); got != -1 {
		t.Fatalf("localDay(0) = %d, want -1", got)
	}
	assertDeliveries(t, mustSubmit(t, scheduler, "u", 2, 0), Delivery{Key: "u", Count: 1, At: 0})
	mustSubmit(t, scheduler, "a", 0, 0)
	assertDeliveries(t, mustPoll(t, scheduler, 0))
	assertDeliveries(t, mustPoll(t, scheduler, 120), Delivery{Key: "a", Count: 1, At: 120})
}

func TestImportantDoesNotMerge(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 10, QuietEnd: 20, MergeWindow: 10, DailyCap: 10, MaxPending: 10})
	mustSubmit(t, scheduler, "a", 1, 10)
	mustSubmit(t, scheduler, "a", 1, 11)
	if got := len(scheduler.pending); got != 2 {
		t.Fatalf("pending len = %d, want 2", got)
	}
}

func TestQueueFullExactlyAndOneBelow(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 10, QuietEnd: 20, MergeWindow: 10, DailyCap: 10, MaxPending: 2})
	mustSubmit(t, scheduler, "a", 1, 10)
	mustSubmit(t, scheduler, "b", 1, 11)
	if _, err := scheduler.Submit("c", 1, 12); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("full queue error = %v, want ErrQueueFull", err)
	}
	if got := len(scheduler.pending); got != 2 {
		t.Fatalf("rejected submit changed pending len to %d", got)
	}
}

func TestRejectedQueueFullDoesNotProcessDue(t *testing.T) {
	scheduler := testScheduler(t, Config{MergeWindow: 0, DailyCap: 10, MaxPending: 1})
	mustSubmit(t, scheduler, "a", 0, 0)
	if _, err := scheduler.Submit("b", 1, 1); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error = %v, want ErrQueueFull", err)
	}
	if got := len(scheduler.pending); got != 1 {
		t.Fatalf("due batch was processed by rejected operation, pending len = %d", got)
	}
	assertDeliveries(t, mustPoll(t, scheduler, 1), Delivery{Key: "a", Count: 1, At: 0})
}

func TestRejectionReasonsOrderAndNoStateChange(t *testing.T) {
	scheduler := testScheduler(t, Config{QuietStart: 10, QuietEnd: 20, MergeWindow: 0, DailyCap: 10, MaxPending: 1})
	mustSubmit(t, scheduler, "a", 1, 10)

	if _, err := New(Config{OffsetMinutes: -721, DailyCap: 1, MaxPending: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid config error = %v", err)
	}
	if _, err := scheduler.Submit("", 0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid submit error = %v, want invalid argument", err)
	}
	if _, err := scheduler.Poll(4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("error = %v, want clock rollback", err)
	}
	if _, err := scheduler.Submit("b", 1, 10); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("error = %v, want queue full", err)
	}
}

func TestDueExaminationIndependentOfPendingTotal(t *testing.T) {
	for _, total := range []int{1000, 100000} {
		scheduler := testScheduler(t, Config{DailyCap: 1000, MaxPending: total})
		scheduler.pushBatch(&batch{key: "due", count: 1, fireAt: 0, seq: 1})
		for i := 1; i < total; i++ {
			scheduler.pushBatch(&batch{key: "future", count: 1, fireAt: int64(10000 + i), seq: uint64(i + 1)})
		}
		mustPoll(t, scheduler, 0)
		if scheduler.batchExamined != 2 || scheduler.batchDeferred != 0 {
			t.Fatalf("total %d: examined=%d deferred=%d, want 2 and 0", total, scheduler.batchExamined, scheduler.batchDeferred)
		}
	}
}

func TestConcurrentOperationsSerialize(t *testing.T) {
	scheduler := testScheduler(t, Config{MergeWindow: 1, DailyCap: 1000, MaxPending: 1000})
	const goroutines = 16
	var wait sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 20; i++ {
				now := int64(worker*20 + i)
				_, _ = scheduler.Submit("k", 0, now)
				_, _ = scheduler.Poll(now + 1)
			}
		}(worker)
	}
	wait.Wait()
	if scheduler.lastNow != goroutines*20 {
		t.Fatalf("lastNow = %d, want %d", scheduler.lastNow, goroutines*20)
	}
}

func mustSubmit(t *testing.T, scheduler *Scheduler, key string, priority int, now int64) []Delivery {
	t.Helper()
	deliveries, err := scheduler.Submit(key, priority, now)
	if err != nil {
		t.Fatalf("Submit(%q, %d, %d) error = %v", key, priority, now, err)
	}
	return deliveries
}

func mustPoll(t *testing.T, scheduler *Scheduler, now int64) []Delivery {
	t.Helper()
	deliveries, err := scheduler.Poll(now)
	if err != nil {
		t.Fatalf("Poll(%d) error = %v", now, err)
	}
	return deliveries
}
