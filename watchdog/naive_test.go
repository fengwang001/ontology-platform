package watchdog

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type naiveOperationKind string

const (
	naiveFeed    naiveOperationKind = "Feed"
	naiveTick    naiveOperationKind = "Tick"
	naiveRestart naiveOperationKind = "Restart"
)

type naiveOperation struct {
	kind naiveOperationKind
	t    int64
}

type naiveResult struct {
	outcome Outcome
	basis   []string
}

type naiveWatchdog struct {
	t0      int64
	open    int64
	closeAt int64
	pre     int64
	lastOp  int64
	f       int64
	warned  bool
	reset   bool
	record  []Event
}

func newNaive(t0, open, closeAt, pre int64) *naiveWatchdog {
	return &naiveWatchdog{
		t0:      t0,
		open:    open,
		closeAt: closeAt,
		pre:     pre,
		lastOp:  t0,
		f:       t0,
		record:  []Event{},
	}
}

func (n *naiveWatchdog) apply(op naiveOperation) naiveResult {
	result := naiveResult{outcome: Outcome{Time: op.t, Events: []Event{}}, basis: []string{}}

	if op.t < n.lastOp {
		result.basis = append(result.basis, fmt.Sprintf("t=%d is earlier than previous operation time %d", op.t, n.lastOp))
		result.outcome.Rejected = true
		result.outcome.Rejection = RejectTimeRollback
		return result
	}

	if !n.reset && n.pre > 0 && !n.warned {
		warningAt := n.f + n.closeAt - n.pre
		if warningAt <= op.t {
			event := Event{Time: warningAt, Kind: Warning}
			n.record = append(n.record, event)
			result.outcome.Events = append(result.outcome.Events, event)
			n.warned = true
			result.basis = append(result.basis, fmt.Sprintf("warning due at %d before operation at %d", warningAt, op.t))
		}
	}

	if !n.reset {
		timeoutAt := n.f + n.closeAt
		if timeoutAt <= op.t {
			event := Event{Time: timeoutAt, Kind: Reset, ResetReason: Timeout}
			n.record = append(n.record, event)
			result.outcome.Events = append(result.outcome.Events, event)
			n.reset = true
			result.basis = append(result.basis, fmt.Sprintf("timeout due at %d before operation at %d", timeoutAt, op.t))
		}
	}

	switch op.kind {
	case naiveFeed:
		if n.reset {
			n.lastOp = op.t
			result.basis = append(result.basis, "feed is rejected because watchdog is reset")
			return n.reject(op.t, RejectResetFeed, result)
		}
		elapsed := op.t - n.f
		if elapsed < n.open {
			event := Event{Time: op.t, Kind: Reset, ResetReason: Early, FeedRejected: true}
			n.record = append(n.record, event)
			result.outcome.Events = append(result.outcome.Events, event)
			n.reset = true
			n.lastOp = op.t
			result.basis = append(result.basis, fmt.Sprintf("elapsed=%d is earlier than open=%d", elapsed, n.open))
			result.outcome.Rejected = true
			result.outcome.Rejection = RejectEarlyFeed
			return result
		}
		n.f = op.t
		n.warned = false
		n.lastOp = op.t
		result.basis = append(result.basis, fmt.Sprintf("elapsed=%d is in [%d,%d)", elapsed, n.open, n.closeAt))
	case naiveTick:
		n.lastOp = op.t
		result.basis = append(result.basis, "tick only records due events")
	case naiveRestart:
		if !n.reset {
			n.lastOp = op.t
			result.basis = append(result.basis, "restart is rejected because watchdog is armed")
			return n.reject(op.t, RejectRestartArmed, result)
		}
		n.reset = false
		n.f = op.t
		n.warned = false
		n.lastOp = op.t
		result.basis = append(result.basis, "restart starts a fresh cycle")
	}

	return result
}

func (n *naiveWatchdog) reject(t int64, reason Rejection, result naiveResult) naiveResult {
	n.lastOp = t
	result.outcome.Rejected = true
	result.outcome.Rejection = reason
	result.outcome.Events = cloneEvents(result.outcome.Events)
	return result
}

func (n *naiveWatchdog) snapshot() []Event {
	return cloneEvents(n.record)
}

func TestRandomReplayMatchesNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(1094))

	for sequence := 0; sequence < 80; sequence++ {
		t0 := rng.Int63n(10)
		open := rng.Int63n(5)
		closeAt := open + 1 + rng.Int63n(5)
		pre := rng.Int63n(closeAt)

		w, err := New(t0, open, closeAt, pre)
		if err != nil {
			t.Fatalf("sequence %d: New(%d,%d,%d,%d) error = %v", sequence, t0, open, closeAt, pre, err)
		}
		model := newNaive(t0, open, closeAt, pre)
		t.Logf("sequence %d input: New(t0=%d open=%d close=%d pre=%d)", sequence, t0, open, closeAt, pre)

		current := t0
		for step := 0; step < 50; step++ {
			opTime := current
			var op naiveOperation
			if rng.Intn(8) == 0 && current > 0 {
				opTime = current - 1
			} else {
				opTime = current + rng.Int63n(closeAt+3)
				current = opTime
			}

			switch rng.Intn(3) {
			case 0:
				op = naiveOperation{kind: naiveFeed, t: opTime}
			case 1:
				op = naiveOperation{kind: naiveTick, t: opTime}
			default:
				op = naiveOperation{kind: naiveRestart, t: opTime}
			}

			got := applyProduct(w, op)
			want := model.apply(op)
			t.Logf("sequence %d step %d input: %s(%d) output: %+v basis: %v", sequence, step, op.kind, op.t, got, want.basis)

			if !reflect.DeepEqual(got, want.outcome) {
				t.Fatalf("sequence %d step %d: %s(%d) = %+v, want %+v", sequence, step, op.kind, op.t, got, want.outcome)
			}
			if !reflect.DeepEqual(w.Events(), model.snapshot()) {
				t.Fatalf("sequence %d step %d event table mismatch:\nproduct=%+v\nnaive=%+v", sequence, step, w.Events(), model.snapshot())
			}
		}
	}
}

func applyProduct(w *Watchdog, op naiveOperation) Outcome {
	switch op.kind {
	case naiveFeed:
		return w.Feed(op.t)
	case naiveTick:
		return w.Tick(op.t)
	default:
		return w.Restart(op.t)
	}
}
