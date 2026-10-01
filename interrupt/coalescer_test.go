package interrupt

import (
	"errors"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type naiveOp struct {
	kind string
	t    int64
	n    int64
}

type naiveRecord struct {
	time  int64
	count int64
}

type naiveResult struct {
	fired  bool
	record naiveRecord
	err    error
}

type naiveState struct {
	gap       int64
	threshold int64

	pending        int64
	last           int64
	hasLast        bool
	waiting        bool
	masked         bool
	lastOp         int64
	hasPreviousOp  bool
	successfulAcks int64
	totalEvents    int64
	records        []naiveRecord
}

type naiveModel struct {
	state *naiveState
}

func newNaive(gap, thr int64) (*naiveModel, error) {
	if gap < 0 {
		return nil, ErrNegativeGap
	}
	if thr < 1 {
		return nil, ErrThresholdTooSmall
	}

	return &naiveModel{state: &naiveState{
		gap:       gap,
		threshold: thr,
	}}, nil
}

func (m *naiveModel) run(op naiveOp) (naiveResult, error) {
	s := m.state

	if s.hasPreviousOp && op.t < s.lastOp {
		return naiveResult{}, ErrTimeBeforeLast
	}

	switch op.kind {
	case "Event":
		if op.n < 1 {
			return naiveResult{}, ErrInvalidEventCount
		}
	case "Ack":
		if !s.waiting {
			return naiveResult{}, ErrNoPendingAck
		}
	}

	result := naiveResult{}

	if s.hasLast && s.pending > 0 && !s.waiting && !s.masked &&
		s.pending < s.threshold && s.last+s.gap <= op.t {
		result = s.fire(s.last + s.gap)
	}

	switch op.kind {
	case "Event":
		s.pending += op.n
		s.totalEvents += op.n
	case "Tick":
	case "Ack":
		s.waiting = false
		s.successfulAcks++
	case "Mask":
		s.masked = true
	case "Unmask":
		s.masked = false
	}

	if s.pending > 0 && !s.waiting && !s.masked {
		if !s.hasLast {
			if s.pending >= s.threshold {
				result = s.fire(op.t)
			}
		} else if op.t >= s.last+s.gap || s.pending >= s.threshold {
			result = s.fire(op.t)
		}
	}

	s.lastOp = op.t
	s.hasPreviousOp = true
	return result, nil
}

func (s *naiveState) fire(t int64) naiveResult {
	record := naiveRecord{time: t, count: s.pending}
	s.records = append(s.records, record)
	s.pending = 0
	s.last = t
	s.hasLast = true
	s.waiting = true
	return naiveResult{fired: true, record: record}
}

func TestScenarioSkeleton(t *testing.T) {
	c, err := New(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil {
		t.Fatal("expected coalescer")
	}
}

func TestExactAllowedTimeFires(t *testing.T) {
	c, err := New(5, 10)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 10}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 0, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 0, 2}, false, 2)
	assertOp(t, c, naiveOp{"Tick", 4, 0}, false, 2)
	assertOp(t, c, naiveOp{"Tick", 5, 0}, true, 0)

	want := []Record{{Time: 0, Count: 10}, {Time: 5, Count: 2}}
	if got := c.Records(); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestThresholdFiresAcrossGap(t *testing.T) {
	c, err := New(10, 5)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 5}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 1, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 1, 5}, true, 0)

	want := []Record{{Time: 0, Count: 5}, {Time: 1, Count: 5}}
	if got := c.Records(); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestOneBelowThresholdDoesNotFire(t *testing.T) {
	c, err := New(10, 5)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 4}, false, 4)
	assertOp(t, c, naiveOp{"Tick", 1, 0}, false, 4)

	if got := c.Records(); len(got) != 0 {
		t.Fatalf("records = %+v, want none", got)
	}
}

func TestTickCatchUpUsesAllowedTimeAndAdvancesGap(t *testing.T) {
	c, err := New(5, 10)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 10}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 0, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 0, 2}, false, 2)
	assertOp(t, c, naiveOp{"Tick", 100, 0}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 100, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 100, 3}, true, 0)

	want := []Record{
		{Time: 0, Count: 10},
		{Time: 5, Count: 2},
		{Time: 100, Count: 3},
	}
	if got := c.Records(); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestAckFiresAccumulatedEventsImmediately(t *testing.T) {
	c, err := New(10, 4)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 4}, true, 0)
	assertOp(t, c, naiveOp{"Event", 1, 4}, false, 4)
	assertOp(t, c, naiveOp{"Ack", 1, 0}, true, 0)

	want := []Record{{Time: 0, Count: 4}, {Time: 1, Count: 4}}
	if got := c.Records(); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestUnmaskFiresAccumulatedEventsImmediately(t *testing.T) {
	c, err := New(0, 4)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 4}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 0, 0}, false, 0)
	assertOp(t, c, naiveOp{"Mask", 0, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 1, 3}, false, 3)
	assertOp(t, c, naiveOp{"Unmask", 2, 0}, true, 0)

	want := []Record{{Time: 0, Count: 4}, {Time: 2, Count: 3}}
	if got := c.Records(); !reflect.DeepEqual(got, want) {
		t.Fatalf("records = %+v, want %+v", got, want)
	}
}

func TestMaskSameTimeCatchUpFiresBeforeMask(t *testing.T) {
	c, err := New(5, 10)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 0, 10}, true, 0)
	assertOp(t, c, naiveOp{"Ack", 0, 0}, false, 0)
	assertOp(t, c, naiveOp{"Event", 0, 3}, false, 3)
	assertOp(t, c, naiveOp{"Mask", 5, 0}, true, 0)

	state := c.Snapshot()
	if !state.Masked || !state.WaitingForAck || state.Pending != 0 {
		t.Fatalf("state = %+v, want masked, waiting, no pending", state)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	c, err := New(5, 5)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 2, 2}, false, 2)
	beforeState := c.Snapshot()
	beforeRecords := c.Records()

	assertReject(t, c, naiveOp{"Tick", 1, 0}, ErrTimeBeforeLast)
	assertReject(t, c, naiveOp{"Event", 2, 0}, ErrInvalidEventCount)
	assertReject(t, c, naiveOp{"Ack", 2, 0}, ErrNoPendingAck)

	afterState := c.Snapshot()
	afterRecords := c.Records()
	if !reflect.DeepEqual(afterState, beforeState) || !reflect.DeepEqual(afterRecords, beforeRecords) {
		t.Fatalf("reject changed state: before=%+v %+v after=%+v %+v",
			beforeState, beforeRecords, afterState, afterRecords)
	}
}

func TestRejectedRollbackHasPriority(t *testing.T) {
	c, err := New(5, 5)
	if err != nil {
		t.Fatal(err)
	}

	assertOp(t, c, naiveOp{"Event", 2, 2}, false, 2)
	assertReject(t, c, naiveOp{"Event", 1, 0}, ErrTimeBeforeLast)

	state := c.Snapshot()
	if state.LastOpTime != 2 || state.Pending != 2 {
		t.Fatalf("state after rejected invalid event = %+v", state)
	}
}

func TestRandomSequencesMatchNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	opsKinds := []string{"Event", "Tick", "Ack", "Mask", "Unmask"}

	for iteration := 0; iteration < 200; iteration++ {
		gap := int64(rng.Intn(7))
		threshold := int64(rng.Intn(6) + 1)

		actual, err := New(gap, threshold)
		if err != nil {
			t.Fatal(err)
		}
		naive, err := newNaive(gap, threshold)
		if err != nil {
			t.Fatal(err)
		}

		var clock int64
		var ops []naiveOp
		var successfulEvents int64

		for step := 0; step < 40; step++ {
			op := naiveOp{kind: opsKinds[rng.Intn(len(opsKinds))], t: clock, n: int64(rng.Intn(6) + 1)}
			if rng.Intn(8) == 0 && step > 0 {
				op.t = clock - int64(rng.Intn(3)+1)
			} else if rng.Intn(8) == 0 {
				op.n = 0
			} else if rng.Intn(2) == 0 {
				clock += int64(rng.Intn(4))
				op.t = clock
			}
			ops = append(ops, op)

			actualResult, actualErr := dispatch(actual, op)
			naiveResult, naiveErr := naive.run(op)

			t.Logf("random input iteration=%d step=%d op=%s t=%d n=%d output fired=%t record=%+v err=%v rationale=[%s]",
				iteration, step, op.kind, op.t, op.n, actualResult.Fired, actualResult.Record, actualErr,
				strings.Join(actualResult.Reasons, "; "))

			if !errors.Is(actualErr, naiveErr) {
				t.Fatalf("iteration %d step %d: actual error %v, naive error %v", iteration, step, actualErr, naiveErr)
			}
			if actualErr == nil && op.kind == "Event" {
				successfulEvents += op.n
			}
			if actualErr == nil {
				if actualResult.Fired != naiveResult.fired {
					t.Fatalf("iteration %d step %d: fired mismatch", iteration, step)
				}
				if naiveResult.fired &&
					(actualResult.Record.Time != naiveResult.record.time ||
						actualResult.Record.Count != naiveResult.record.count) {
					t.Fatalf("iteration %d step %d: record mismatch actual=%+v naive=%+v",
						iteration, step, actualResult.Record, naiveResult.record)
				}
			}

			assertStateMatchesNaive(t, actual, naive, iteration, step)
			assertAccounting(t, actual, successfulEvents, iteration, step)
		}

		replayed, err := New(gap, threshold)
		if err != nil {
			t.Fatal(err)
		}
		for _, op := range ops {
			_, err := dispatch(replayed, op)
			if err == nil {
				continue
			}
			if !errors.Is(err, ErrTimeBeforeLast) && !errors.Is(err, ErrInvalidEventCount) && !errors.Is(err, ErrNoPendingAck) {
				t.Fatalf("replay unexpected error: %v", err)
			}
		}
		if got := replayed.Records(); !reflect.DeepEqual(got, actual.Records()) {
			t.Fatalf("replay records = %+v, original = %+v", got, actual.Records())
		}
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	c, err := New(0, 8)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var successMu sync.Mutex
	var successfulEvents int64

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()

			var localEvents int64
			for step := 0; step < 25; step++ {
				at := int64(worker*25 + step + 1)
				var result OpResult
				var err error

				if step%3 != 1 {
					result, err = c.Event(at, 3)
					if err == nil {
						localEvents += 3
					}
				} else {
					result, err = c.Tick(at)
				}

				t.Logf("concurrent input worker=%d op_time=%d output=%+v err=%v", worker, at, result, err)
			}

			successMu.Lock()
			successfulEvents += localEvents
			successMu.Unlock()
		}(worker)
	}
	wg.Wait()

	for {
		state := c.Snapshot()
		if state.WaitingForAck {
			if _, err := c.Ack(300); err != nil {
				t.Fatalf("post-concurrent ack failed: %v", err)
			}
			if _, err := c.Tick(300); err != nil {
				t.Fatalf("post-concurrent tick failed: %v", err)
			}
			continue
		}
		if state.Pending > 0 {
			if _, err := c.Tick(300); err != nil {
				t.Fatalf("post-concurrent tick failed: %v", err)
			}
			continue
		}
		break
	}

	state := c.Snapshot()
	var carried int64
	for _, record := range c.Records() {
		carried += record.Count
	}
	if carried+state.Pending != successfulEvents {
		t.Fatalf("accounting carried=%d pending=%d events=%d", carried, state.Pending, successfulEvents)
	}

	fireAckDifference := int64(len(c.Records())) - state.SuccessfulAcks
	if fireAckDifference != 0 && fireAckDifference != 1 {
		t.Fatalf("trigger/ack difference = %d, want 0 or 1", fireAckDifference)
	}
}

func TestConstructionValidation(t *testing.T) {
	if _, err := New(-1, 1); !errors.Is(err, ErrNegativeGap) {
		t.Fatalf("negative gap error = %v", err)
	}
	if _, err := New(0, 0); !errors.Is(err, ErrThresholdTooSmall) {
		t.Fatalf("threshold error = %v", err)
	}
}

func TestNaiveSkeleton(t *testing.T) {
	if !errors.Is(ErrNegativeGap, ErrNegativeGap) {
		t.Fatal("sentinel error unavailable")
	}
}

func assertOp(t *testing.T, c *Coalescer, op naiveOp, wantFired bool, wantPending int64) {
	t.Helper()

	result, err := dispatch(c, op)
	if err != nil {
		t.Fatalf("%+v returned error %v", op, err)
	}
	if result.Fired != wantFired || result.Pending != wantPending {
		t.Fatalf("%+v result = %+v, want fired=%v pending=%d", op, result, wantFired, wantPending)
	}

	t.Logf("input op=%s t=%d n=%d output fired=%t record=%+v pending=%d rationale=[%s]",
		op.kind, op.t, op.n, result.Fired, result.Record, result.Pending, strings.Join(result.Reasons, "; "))
}

func assertReject(t *testing.T, c *Coalescer, op naiveOp, want error) {
	t.Helper()

	result, err := dispatch(c, op)
	if !errors.Is(err, want) {
		t.Fatalf("%+v error = %v, want %v", op, err, want)
	}

	t.Logf("input op=%s t=%d n=%d rejected output=%+v reason=%v",
		op.kind, op.t, op.n, result, err)
}

func assertStateMatchesNaive(t *testing.T, c *Coalescer, n *naiveModel, iteration, step int) {
	t.Helper()

	state := c.Snapshot()
	if state.Pending != n.state.pending ||
		state.HasTriggered != n.state.hasLast ||
		state.WaitingForAck != n.state.waiting ||
		state.Masked != n.state.masked ||
		state.LastOpTime != n.state.lastOp ||
		state.HasPreviousOp != n.state.hasPreviousOp ||
		state.SuccessfulAcks != n.state.successfulAcks {
		t.Fatalf("iteration %d step %d actual state %+v differs from naive %+v", iteration, step, state, n.state)
	}
	if state.HasTriggered && state.LastTrigger != n.state.last {
		t.Fatalf("iteration %d step %d last trigger actual=%d naive=%d", iteration, step, state.LastTrigger, n.state.last)
	}

	actualRecords := c.Records()
	if len(actualRecords) != len(n.state.records) {
		t.Fatalf("iteration %d step %d record length actual=%d naive=%d",
			iteration, step, len(actualRecords), len(n.state.records))
	}
	for i := range actualRecords {
		if actualRecords[i].Time != n.state.records[i].time ||
			actualRecords[i].Count != n.state.records[i].count {
			t.Fatalf("iteration %d step %d record %d actual=%+v naive=%+v",
				iteration, step, i, actualRecords[i], n.state.records[i])
		}
	}
}

func assertAccounting(t *testing.T, c *Coalescer, successfulEvents int64, iteration, step int) {
	t.Helper()

	state := c.Snapshot()
	var carried int64
	var lastTime int64
	for i, record := range c.Records() {
		if i > 0 && record.Time < lastTime {
			t.Fatalf("iteration %d step %d record time decreased: %+v", iteration, step, c.Records())
		}
		lastTime = record.Time
		carried += record.Count
	}

	if carried+state.Pending != successfulEvents {
		t.Fatalf("iteration %d step %d accounting carried=%d pending=%d events=%d",
			iteration, step, carried, state.Pending, successfulEvents)
	}
	fireAckDifference := int64(len(c.Records())) - state.SuccessfulAcks
	if fireAckDifference != 0 && fireAckDifference != 1 {
		t.Fatalf("iteration %d step %d trigger/ack difference=%d", iteration, step, fireAckDifference)
	}
}

func dispatch(c *Coalescer, op naiveOp) (OpResult, error) {
	switch op.kind {
	case "Event":
		return c.Event(op.t, op.n)
	case "Tick":
		return c.Tick(op.t)
	case "Ack":
		return c.Ack(op.t)
	case "Mask":
		return c.Mask(op.t)
	case "Unmask":
		return c.Unmask(op.t)
	default:
		panic("unknown op " + op.kind)
	}
}
