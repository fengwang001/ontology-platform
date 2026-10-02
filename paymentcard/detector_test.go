package paymentcard

import (
	"reflect"
	"sync"
	"testing"
)

func newTestDetector(t *testing.T, speed, limit, window int64) *Detector {
	t.Helper()
	detector, err := NewDetector(speed, limit, window)
	if err != nil {
		t.Fatalf("NewDetector() error = %v", err)
	}
	return detector
}

func mustCheck(t *testing.T, detector *Detector, card []byte, at, x, y int64) {
	t.Helper()
	if got := detector.Check(card, at, x, y); got != OutcomeAccepted {
		t.Fatalf("Check(%d,%d,%d) = %s, want %s", at, x, y, got, OutcomeAccepted)
	}
}

func mustImpossible(t *testing.T, detector *Detector, card []byte, at, x, y int64) {
	t.Helper()
	if got := detector.Check(card, at, x, y); got != OutcomeImpossible {
		t.Fatalf("Check(%d,%d,%d) = %s, want %s", at, x, y, got, OutcomeImpossible)
	}
}

func TestConstructionValidation(t *testing.T) {
	cases := []struct {
		speed  int64
		limit  int64
		window int64
	}{
		{0, 2, 100},
		{1_000_001, 2, 100},
		{900, 0, 100},
		{900, 101, 100},
		{900, 2, 0},
		{900, 2, 1_000_000_000_001},
	}

	for index, tc := range cases {
		if _, err := NewDetector(tc.speed, tc.limit, tc.window); err == nil {
			t.Fatalf("case %d: expected construction error", index)
		}
	}
}

func TestSpeedEqualityAndOneMore(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")

	if got := detector.Check(card, 0, 0, 0); got != OutcomeAccepted {
		t.Fatalf("first check = %s, want %s", got, OutcomeAccepted)
	}
	if got := detector.Check(card, 3600, 600, 300); got != OutcomeAccepted {
		t.Fatalf("equal-speed check = %s, want %s", got, OutcomeAccepted)
	}

	snapshot, _ := detector.Snapshot(card)
	if snapshot.T != 3600 || snapshot.X != 600 || snapshot.Y != 300 {
		t.Fatalf("anchor = (%d,%d,%d), want (3600,600,300)", snapshot.T, snapshot.X, snapshot.Y)
	}

	if got := detector.Check(card, 7200, 1501, 0); got != OutcomeImpossible {
		t.Fatalf("one-kilometer-over check = %s, want %s", got, OutcomeImpossible)
	}
}

func TestZeroElapsedDuplicateAndImpossible(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")

	if got := detector.Check(card, 10, 1, 2); got != OutcomeAccepted {
		t.Fatalf("first check = %s, want %s", got, OutcomeAccepted)
	}
	if got := detector.Check(card, 10, 1, 2); got != OutcomeDuplicate {
		t.Fatalf("same-time same-place = %s, want %s", got, OutcomeDuplicate)
	}
	if got := detector.Check(card, 10, 1, 3); got != OutcomeImpossible {
		t.Fatalf("same-time one-away = %s, want %s", got, OutcomeImpossible)
	}
}

func TestRejectedTransactionDoesNotAdvanceAnchor(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")

	mustCheck(t, detector, card, 100, 0, 0)
	if got := detector.Check(card, 101, 100, 0); got != OutcomeImpossible {
		t.Fatalf("impossible check = %s, want %s", got, OutcomeImpossible)
	}
	snapshot, _ := detector.Snapshot(card)
	if snapshot.T != 100 || snapshot.X != 0 || snapshot.Y != 0 || !reflect.DeepEqual(snapshot.Rejections, []int64{101}) {
		t.Fatalf("unexpected snapshot after rejection: %+v", snapshot)
	}
}

func TestSameTimestampDifferentCoordinatesIsNotOutOfOrder(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")

	mustCheck(t, detector, card, 10, 0, 0)
	if got := detector.Check(card, 10, 1, 0); got != OutcomeImpossible {
		t.Fatalf("same timestamp different coordinates = %s, want %s", got, OutcomeImpossible)
	}
	if got := detector.Check(card, 9, 0, 0); got != OutcomeOutOfOrder {
		t.Fatalf("one-second earlier = %s, want %s", got, OutcomeOutOfOrder)
	}
}

func TestTravelWindowBoundariesPreservesRejections(t *testing.T) {
	detector := newTestDetector(t, 1, 2, 100)
	card := []byte("card")

	mustCheck(t, detector, card, 0, 0, 0)
	mustImpossible(t, detector, card, 1, 100, 0)
	if got := detector.Declare(card, 10, 20); got != OutcomeOK {
		t.Fatalf("Declare() = %s", got)
	}
	if got := detector.Check(card, 10, 100, 0); got != OutcomeAccepted {
		t.Fatalf("travel from boundary = %s, want %s", got, OutcomeAccepted)
	}
	if got := detector.Check(card, 20, 200, 0); got != OutcomeImpossible {
		t.Fatalf("travel to boundary = %s, want %s", got, OutcomeImpossible)
	}

	snapshot, _ := detector.Snapshot(card)
	if !reflect.DeepEqual(snapshot.Rejections, []int64{1, 20}) {
		t.Fatalf("rejections = %v, want [1 20]", snapshot.Rejections)
	}
	if snapshot.T != 10 || snapshot.X != 100 || snapshot.Y != 0 {
		t.Fatalf("anchor = (%d,%d,%d), want (10,100,0)", snapshot.T, snapshot.X, snapshot.Y)
	}
}

func TestRejectionWindowCounting(t *testing.T) {
	card := []byte("card")

	t.Run("exact cutoff expires and one-second-inside counts", func(t *testing.T) {
		detector := newTestDetector(t, 1, 2, 100)
		mustCheck(t, detector, card, 0, 0, 0)
		mustImpossible(t, detector, card, 100, 100, 0)
		mustImpossible(t, detector, card, 200, 100, 0)
		snapshot, _ := detector.Snapshot(card)
		if snapshot.Frozen || !reflect.DeepEqual(snapshot.Rejections, []int64{100, 200}) {
			t.Fatalf("unexpected state after exact cutoff: %+v", snapshot)
		}
		mustImpossible(t, detector, card, 201, 100, 0)
		snapshot, _ = detector.Snapshot(card)
		if !snapshot.Frozen || !reflect.DeepEqual(snapshot.Rejections, []int64{100, 200, 201}) {
			t.Fatalf("expected freeze at third windowed rejection: %+v", snapshot)
		}
	})

	t.Run("future history item counts", func(t *testing.T) {
		detector := newTestDetector(t, 1, 2, 100)
		mustCheck(t, detector, card, 100, 0, 0)
		mustImpossible(t, detector, card, 200, 100, 0)
		mustImpossible(t, detector, card, 150, 100, 0)
		snapshot, _ := detector.Snapshot(card)
		if !snapshot.Frozen || !reflect.DeepEqual(snapshot.Rejections, []int64{200, 150}) {
			t.Fatalf("future rejection should count: %+v", snapshot)
		}
	})

	t.Run("two rejections at least H apart do not freeze", func(t *testing.T) {
		detector := newTestDetector(t, 1, 2, 100)
		mustCheck(t, detector, card, 0, 0, 0)
		mustImpossible(t, detector, card, 100, 100, 0)
		mustImpossible(t, detector, card, 200, 100, 0)
		snapshot, _ := detector.Snapshot(card)
		if snapshot.Frozen {
			t.Fatalf("card should not freeze when rejections are exactly H apart")
		}
	})
}

func TestFreezeReasonsAndUnfreeze(t *testing.T) {
	detector := newTestDetector(t, 1, 2, 100)
	card := []byte("card")

	mustCheck(t, detector, card, 0, 0, 0)
	mustImpossible(t, detector, card, 10, 100, 0)
	if got := detector.Check(card, 20, 100, 0); got != OutcomeImpossible {
		t.Fatalf("threshold transaction = %s, want %s", got, OutcomeImpossible)
	}

	frozenCalls := []Transaction{
		{T: 20, X: 100, Y: 0},
		{T: 20, X: 101, Y: 0},
		{T: 19, X: 0, Y: 0},
		{T: 30, X: 100, Y: 0},
	}
	for index, transaction := range frozenCalls {
		if got := detector.Check(card, transaction.T, transaction.X, transaction.Y); got != OutcomeFrozen {
			t.Fatalf("frozen case %d = %s, want %s", index, got, OutcomeFrozen)
		}
	}

	if got := detector.Unfreeze([]byte("missing")); got != OutcomeCardNotFound {
		t.Fatalf("missing card unfreeze = %s", got)
	}
	if got := detector.Unfreeze(card); got != OutcomeOK {
		t.Fatalf("Unfreeze() = %s", got)
	}
	if got := detector.Unfreeze(card); got != OutcomeNotFrozen {
		t.Fatalf("second unfreeze = %s, want %s", got, OutcomeNotFrozen)
	}

	snapshot, _ := detector.Snapshot(card)
	if snapshot.Frozen || len(snapshot.Rejections) != 0 || !snapshot.HasAnchor {
		t.Fatalf("unfreeze should clear frozen state and history while retaining anchor: %+v", snapshot)
	}
}

func TestInvalidAndRejectedOperationsDoNotChangeState(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")
	mustCheck(t, detector, card, 10, 1, 2)
	before, _ := detector.Snapshot(card)

	invalidCalls := []struct {
		t int64
		x int64
		y int64
	}{
		{-1, 0, 0},
		{1_000_000_000_001, 0, 0},
		{0, -1_000_000_001, 0},
		{0, 1_000_000_001, 0},
		{0, 0, -1_000_000_001},
		{0, 0, 1_000_000_001},
	}
	for index, tc := range invalidCalls {
		if got := detector.Check(card, tc.t, tc.x, tc.y); got != OutcomeInvalidParameters {
			t.Fatalf("invalid case %d = %s", index, got)
		}
	}
	if got := detector.Check(nil, 11, 1, 2); got != OutcomeInvalidParameters {
		t.Fatalf("empty card = %s", got)
	}
	if got := detector.Check(card, 9, 1, 2); got != OutcomeOutOfOrder {
		t.Fatalf("out-of-order = %s", got)
	}
	if got := detector.Check(card, 10, 1, 2); got != OutcomeDuplicate {
		t.Fatalf("duplicate = %s", got)
	}

	after, _ := detector.Snapshot(card)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejected calls\\nbefore: %+v\\nafter:  %+v", before, after)
	}
}

func TestDeclareValidationAndReplacement(t *testing.T) {
	detector := newTestDetector(t, 1, 2, 100)
	missing := []byte("missing")

	if got := detector.Declare(missing, 10, 10); got != OutcomeInvalidParameters {
		t.Fatalf("empty interval = %s", got)
	}
	if got := detector.Declare(missing, -1, 10); got != OutcomeInvalidParameters {
		t.Fatalf("negative from = %s", got)
	}
	if got := detector.Declare(missing, 10, 10_000_000_000_001); got != OutcomeInvalidParameters {
		t.Fatalf("to above limit = %s", got)
	}
	if _, exists := detector.Snapshot(missing); exists {
		t.Fatalf("invalid declare created card state")
	}

	if got := detector.Declare(missing, 10, 20); got != OutcomeOK {
		t.Fatalf("valid declare = %s", got)
	}
	if got := detector.Declare(missing, 30, 40); got != OutcomeOK {
		t.Fatalf("replacement declare = %s", got)
	}
	snapshot, _ := detector.Snapshot(missing)
	if !snapshot.HasTravelWindow || snapshot.TravelFrom != 30 || snapshot.TravelTo != 40 {
		t.Fatalf("travel window = %+v, want [30,40)", snapshot)
	}
}

func TestCheckBatchOrderingMidBatchFreezeAndValidation(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("card")
	mustCheck(t, detector, card, 3600, 600, 300)

	transactions := []Transaction{
		{T: 3800, X: 0, Y: 0},
		{T: 3601, X: 0, Y: 0},
		{T: 3750, X: 0, Y: 0},
	}
	results := detector.CheckBatch(card, transactions)
	want := []Outcome{OutcomeImpossible, OutcomeImpossible, OutcomeImpossible}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("batch results = %v, want %v", results, want)
	}
	snapshot, _ := detector.Snapshot(card)
	if !snapshot.Frozen || snapshot.T != 3600 || snapshot.X != 600 || snapshot.Y != 300 {
		t.Fatalf("unexpected post-batch snapshot: %+v", snapshot)
	}
	if !reflect.DeepEqual(snapshot.Rejections, []int64{3601, 3750, 3800}) {
		t.Fatalf("batch rejection history = %v", snapshot.Rejections)
	}

	frozenBatch := []Transaction{{T: 100, X: 0, Y: 0}, {T: 200, X: 0, Y: 0}}
	results = detector.CheckBatch(card, frozenBatch)
	want = []Outcome{OutcomeFrozen, OutcomeFrozen}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("frozen batch = %v, want %v", results, want)
	}

	invalidBatch := []Transaction{{T: 0, X: 0, Y: 0}, {T: -1, X: 0, Y: 0}}
	results = detector.CheckBatch([]byte("new-card"), invalidBatch)
	want = []Outcome{OutcomeInvalidParameters, OutcomeInvalidParameters}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("invalid batch = %v, want %v", results, want)
	}
	if _, exists := detector.Snapshot([]byte("new-card")); exists {
		t.Fatalf("invalid batch created card state")
	}
}

func TestCheckBatchStableOrderForEqualTimestamps(t *testing.T) {
	detector := newTestDetector(t, 1, 100, 100)
	card := []byte("card")
	mustCheck(t, detector, card, 0, 0, 0)

	transactions := []Transaction{
		{T: 1, X: 0, Y: 0},
		{T: 1, X: 0, Y: 0},
		{T: 1, X: 1, Y: 0},
	}
	results := detector.CheckBatch(card, transactions)
	want := []Outcome{OutcomeAccepted, OutcomeDuplicate, OutcomeImpossible}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("equal-timestamp batch = %v, want %v", results, want)
	}
}

func TestCanonicalSingleCardExample(t *testing.T) {
	detector := newTestDetector(t, 900, 2, 100)
	card := []byte("canonical")

	cases := []struct {
		transaction Transaction
		want        Outcome
		frozen      bool
		rejections  []int64
	}{
		{Transaction{0, 0, 0}, OutcomeAccepted, false, nil},
		{Transaction{3600, 600, 300}, OutcomeAccepted, false, nil},
		{Transaction{3600, 600, 300}, OutcomeDuplicate, false, nil},
		{Transaction{3601, 0, 0}, OutcomeImpossible, false, []int64{3601}},
		{Transaction{3750, 0, 0}, OutcomeImpossible, false, []int64{3601, 3750}},
		{Transaction{3800, 0, 0}, OutcomeImpossible, true, []int64{3601, 3750, 3800}},
	}

	for index, tc := range cases {
		got := detector.Check(card, tc.transaction.T, tc.transaction.X, tc.transaction.Y)
		if got != tc.want {
			t.Fatalf("case %d = %s, want %s", index, got, tc.want)
		}
		snapshot, _ := detector.Snapshot(card)
		if snapshot.Frozen != tc.frozen || !reflect.DeepEqual(snapshot.Rejections, tc.rejections) {
			t.Fatalf("case %d state = frozen:%v rejections:%v, want frozen:%v rejections:%v",
				index, snapshot.Frozen, snapshot.Rejections, tc.frozen, tc.rejections)
		}
	}
}

func TestFrozenCheckHasPriorityOverAllRejectionReasons(t *testing.T) {
	detector := newTestDetector(t, 1, 2, 100)
	card := []byte("frozen-priority")

	mustCheck(t, detector, card, 10, 0, 0)
	mustImpossible(t, detector, card, 20, 100, 0)
	mustImpossible(t, detector, card, 30, 100, 0)

	priorityCalls := []Transaction{
		{T: 10, X: 0, Y: 0},
		{T: 9, X: 0, Y: 0},
		{T: 10, X: 1, Y: 0},
	}
	for index, transaction := range priorityCalls {
		if got := detector.Check(card, transaction.T, transaction.X, transaction.Y); got != OutcomeFrozen {
			t.Fatalf("priority case %d = %s, want %s", index, got, OutcomeFrozen)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	detector := newTestDetector(t, 1_000_000, 100, 1_000_000_000)
	var wait sync.WaitGroup

	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			card := []byte{byte(worker)}
			for at := int64(0); at < 50; at++ {
				detector.Check(card, at, int64(worker), at)
				detector.Snapshot(card)
			}
			detector.Declare(card, 50, 60)
			detector.Unfreeze(card)
		}(worker)
	}
	wait.Wait()
}
