package watchdog

import (
	"reflect"
	"sync"
	"testing"
)

func TestFeedWindowBoundaries(t *testing.T) {
	const (
		t0    int64 = 10
		open  int64 = 3
		close int64 = 8
		pre   int64 = 2
	)

	tests := []struct {
		name          string
		t             int64
		wantReject    bool
		wantRejection Rejection
		wantReason    ResetReason
	}{
		{name: "open minus one is early", t: t0 + open - 1, wantReject: true, wantRejection: RejectEarlyFeed, wantReason: Early},
		{name: "open is valid", t: t0 + open},
		{name: "close minus one is valid", t: t0 + close - 1},
		{name: "close is timeout", t: t0 + close, wantReject: true, wantRejection: RejectResetFeed, wantReason: Timeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, err := New(t0, open, close, pre)
			if err != nil {
				t.Fatalf("New() error = %v", err)
			}

			outcome := w.Feed(tt.t)
			if outcome.Rejected != tt.wantReject {
				t.Fatalf("Rejected = %v, want %v", outcome.Rejected, tt.wantReject)
			}
			if tt.wantReject && outcome.Rejection != tt.wantRejection {
				t.Fatalf("Rejection = %q, want %q", outcome.Rejection, tt.wantRejection)
			}

			events := w.Events()
			if tt.wantReason != "" {
				if len(events) == 0 {
					t.Fatalf("Events() is empty, want reset %q", tt.wantReason)
				}
				last := events[len(events)-1]
				if last.Kind != Reset || last.ResetReason != tt.wantReason {
					t.Fatalf("last event = %+v, want reset %q", last, tt.wantReason)
				}
			}
		})
	}
}

func TestInvalidConstruction(t *testing.T) {
	tests := []struct {
		name      string
		t0        int64
		open      int64
		closeAt   int64
		pre       int64
		wantError error
	}{
		{name: "negative open", open: -1, closeAt: 2, pre: 1, wantError: ErrNegativeOpen},
		{name: "open equals close", open: 2, closeAt: 2, pre: 1, wantError: ErrInvalidWindow},
		{name: "negative pre", open: 0, closeAt: 2, pre: -1, wantError: ErrNegativePre},
		{name: "pre equals close", open: 0, closeAt: 2, pre: 2, wantError: ErrPreTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.t0, tt.open, tt.closeAt, tt.pre)
			if err != tt.wantError {
				t.Fatalf("New() error = %v, want %v", err, tt.wantError)
			}
		})
	}
}

func TestWarningBoundary(t *testing.T) {
	w, err := New(100, 3, 10, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	before := w.Tick(107)
	if before.Rejected || len(before.Events) != 0 {
		t.Fatalf("Tick(107) = %+v, want accepted without a warning", before)
	}

	atWarning := w.Feed(108)
	if atWarning.Rejected {
		t.Fatalf("Feed(108) rejected: %+v", atWarning)
	}
	if len(atWarning.Events) != 1 || atWarning.Events[0] != (Event{Time: 108, Kind: Warning}) {
		t.Fatalf("Feed(108) events = %+v, want one warning at 108", atWarning.Events)
	}

	afterTick := w.Tick(109)
	if len(afterTick.Events) != 0 {
		t.Fatalf("Tick(109) events = %+v, warning must be recorded once", afterTick.Events)
	}
}

func TestZeroPreHasNoWarning(t *testing.T) {
	w, err := New(0, 2, 5, 0)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	beforeTimeout := w.Tick(4)
	if beforeTimeout.Rejected || len(beforeTimeout.Events) != 0 {
		t.Fatalf("Tick(4) = %+v, want no event", beforeTimeout)
	}

	timeout := w.Tick(5)
	want := []Event{{Time: 5, Kind: Reset, ResetReason: Timeout}}
	if len(timeout.Events) != 1 || timeout.Events[0] != want[0] {
		t.Fatalf("Tick(5) events = %+v, want %+v", timeout.Events, want)
	}
}

func TestOpenZeroIsNotEarly(t *testing.T) {
	w, err := New(7, 0, 5, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := w.Feed(7)
	if outcome.Rejected || len(outcome.Events) != 0 {
		t.Fatalf("Feed(7) = %+v, want accepted at open=0", outcome)
	}
}

func TestTickRecordsWarningBeforeTimeout(t *testing.T) {
	w, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := w.Tick(10)
	want := []Event{
		{Time: 8, Kind: Warning},
		{Time: 10, Kind: Reset, ResetReason: Timeout},
	}
	if len(outcome.Events) != len(want) {
		t.Fatalf("events = %+v, want %+v", outcome.Events, want)
	}
	for i := range want {
		if outcome.Events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, outcome.Events[i], want[i])
		}
	}
}

func TestWarningBeforeOpenEarlyFeedKeepsOrder(t *testing.T) {
	w, err := New(0, 5, 10, 8)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := w.Feed(2)
	want := []Event{
		{Time: 2, Kind: Warning},
		{Time: 2, Kind: Reset, ResetReason: Early, FeedRejected: true},
	}
	if !outcome.Rejected || outcome.Rejection != RejectEarlyFeed {
		t.Fatalf("outcome = %+v, want early rejection", outcome)
	}
	if len(outcome.Events) != len(want) {
		t.Fatalf("events = %+v, want %+v", outcome.Events, want)
	}
	for i := range want {
		if outcome.Events[i] != want[i] {
			t.Fatalf("event %d = %+v, want %+v", i, outcome.Events[i], want[i])
		}
	}
}

func TestRestartJudgedAfterCatchUp(t *testing.T) {
	w, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := w.Restart(10)
	if outcome.Rejected {
		t.Fatalf("Restart(10) rejected: %+v", outcome)
	}
	if len(outcome.Events) != 2 || outcome.Events[1].ResetReason != Timeout {
		t.Fatalf("Restart(10) events = %+v, want warning and timeout before restart", outcome.Events)
	}

	feed := w.Feed(13)
	if feed.Rejected || len(feed.Events) != 0 {
		t.Fatalf("Feed(13) after restart = %+v, want accepted in restarted cycle", feed)
	}
}

func TestRejectionsAndStateEffects(t *testing.T) {
	w, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first := w.Feed(3)
	if first.Rejected {
		t.Fatalf("Feed(3) = %+v, want accepted", first)
	}

	rollback := w.Feed(2)
	if !rollback.Rejected || rollback.Rejection != RejectTimeRollback || len(rollback.Events) != 0 {
		t.Fatalf("rollback = %+v, want no recorded events or state change", rollback)
	}

	armedRestart := w.Restart(4)
	if !armedRestart.Rejected || armedRestart.Rejection != RejectRestartArmed || len(armedRestart.Events) != 0 {
		t.Fatalf("armed restart = %+v, want rejection without reset", armedRestart)
	}

	oldRollback := w.Feed(3)
	if !oldRollback.Rejected || oldRollback.Rejection != RejectTimeRollback {
		t.Fatalf("Feed(3) after Restart(4) = %+v, want rollback", oldRollback)
	}

	timeoutFeed := w.Feed(13)
	if !timeoutFeed.Rejected || timeoutFeed.Rejection != RejectResetFeed {
		t.Fatalf("timeout feed = %+v, want feed rejected after timeout", timeoutFeed)
	}

	resetFeed := w.Feed(14)
	if !resetFeed.Rejected || resetFeed.Rejection != RejectResetFeed || len(resetFeed.Events) != 0 {
		t.Fatalf("reset feed = %+v, want rejection with no new events", resetFeed)
	}

	resetRollback := w.Feed(13)
	if !resetRollback.Rejected || resetRollback.Rejection != RejectTimeRollback {
		t.Fatalf("reset rollback = %+v, want time rollback", resetRollback)
	}
}

func TestFirstOperationRollsBackFromT0(t *testing.T) {
	w, err := New(10, 3, 8, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	outcome := w.Feed(9)
	if !outcome.Rejected || outcome.Rejection != RejectTimeRollback {
		t.Fatalf("Feed(9) = %+v, want rollback against t0=10", outcome)
	}

	valid := w.Feed(13)
	if valid.Rejected {
		t.Fatalf("Feed(13) after rollback = %+v, want unchanged armed state and valid feed", valid)
	}
}

func TestReplayProducesIdenticalEventTable(t *testing.T) {
	operations := []naiveOperation{
		{kind: naiveTick, t: 6},
		{kind: naiveFeed, t: 7},
		{kind: naiveTick, t: 12},
		{kind: naiveFeed, t: 13},
		{kind: naiveRestart, t: 15},
		{kind: naiveRestart, t: 14},
		{kind: naiveTick, t: 17},
		{kind: naiveRestart, t: 20},
	}

	w1, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("first New() error = %v", err)
	}
	w2, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("second New() error = %v", err)
	}

	var outcomes1, outcomes2 []Outcome
	for _, operation := range operations {
		outcomes1 = append(outcomes1, applyProduct(w1, operation))
		outcomes2 = append(outcomes2, applyProduct(w2, operation))
	}

	if !reflect.DeepEqual(outcomes1, outcomes2) {
		t.Fatalf("outcomes differ on replay:\nfirst=%+v\nsecond=%+v", outcomes1, outcomes2)
	}
	if !reflect.DeepEqual(w1.Events(), w2.Events()) {
		t.Fatalf("event tables differ on replay:\nfirst=%+v\nsecond=%+v", w1.Events(), w2.Events())
	}
}

func TestEventsSnapshotIsImmutable(t *testing.T) {
	w, err := New(0, 3, 10, 2)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	w.Tick(8)

	first := w.Events()
	first[0] = Event{Time: 999, Kind: Reset, ResetReason: Early}
	second := w.Events()
	if second[0] != (Event{Time: 8, Kind: Warning}) {
		t.Fatalf("mutated Events() result changed watchdog state: %+v", second)
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now int64
}

func (c *fakeClock) Now() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(now int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

func TestInjectedClock(t *testing.T) {
	clock := &fakeClock{now: 10}
	w, err := NewWithClock(10, 3, 8, 2, clock)
	if err != nil {
		t.Fatalf("NewWithClock() error = %v", err)
	}

	clock.Advance(16)
	warning, err := w.TickNow()
	if err != nil || warning.Rejected || len(warning.Events) != 1 || warning.Events[0].Kind != Warning {
		t.Fatalf("TickNow() = %+v, %v, want injected-clock warning", warning, err)
	}

	clock.Advance(18)
	timeout, err := w.FeedNow()
	if err != nil || !timeout.Rejected || timeout.Rejection != RejectResetFeed || len(timeout.Events) != 1 {
		t.Fatalf("FeedNow() = %+v, %v, want rejected timeout feed", timeout, err)
	}

	clock.Advance(20)
	restart, err := w.RestartNow()
	if err != nil || restart.Rejected {
		t.Fatalf("RestartNow() = %+v, %v, want accepted", restart, err)
	}
}

func TestInjectedClockMissing(t *testing.T) {
	w, err := New(0, 1, 2, 1)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if _, err := w.TickNow(); err != ErrNoClock {
		t.Fatalf("TickNow() error = %v, want %v", err, ErrNoClock)
	}
	if _, err := w.FeedNow(); err != ErrNoClock {
		t.Fatalf("FeedNow() error = %v, want %v", err, ErrNoClock)
	}
	if _, err := w.RestartNow(); err != ErrNoClock {
		t.Fatalf("RestartNow() error = %v, want %v", err, ErrNoClock)
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	w, err := New(0, 4, 20, 5)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	var wait sync.WaitGroup
	for goroutineID := 0; goroutineID < 8; goroutineID++ {
		wait.Add(1)
		go func(id int64) {
			defer wait.Done()
			for i := int64(0); i < 20; i++ {
				at := id*25 + i
				switch i % 3 {
				case 0:
					w.Feed(at)
				case 1:
					w.Tick(at)
				default:
					w.Restart(at)
				}
				_ = w.Events()
			}
		}(int64(goroutineID))
	}
	wait.Wait()

	events := w.Events()
	for i := 1; i < len(events); i++ {
		if events[i-1].Time > events[i].Time {
			t.Fatalf("events are not nondecreasing at %d: %+v", i, events)
		}
	}

	resetCount := 0
	for _, event := range events {
		if event.Kind == Reset {
			resetCount++
		}
	}
	if resetCount == 0 {
		t.Fatalf("concurrent replay recorded no resets; event table = %+v", events)
	}
}
