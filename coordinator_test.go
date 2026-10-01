package speculative

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func requireEnqueue(t *testing.T, c *Coordinator, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := c.Enqueue(id); err != nil {
			t.Fatalf("Enqueue(%q) = %v", id, err)
		}
	}
}

func assertStatus(t *testing.T, c *Coordinator, want []StatusItem) {
	t.Helper()
	got := c.Status()
	if len(got) != len(want) {
		t.Fatalf("Status length = %d, want %d (%+v)", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("Status[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestPrefixMergeLeavesLaterEpochsUnchanged(t *testing.T) {
	c, err := NewCoordinator(3, 4)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b", "c", "d")

	got, err := c.Report("b", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	wantResult := Result{Merged: []string{"a", "b"}, Bumped: []string{}}
	if !resultsEqual(got, wantResult) {
		t.Fatalf("Report = %+v, want %+v", got, wantResult)
	}
	if merged := c.Merged(); fmt.Sprint(merged) != "[a b]" {
		t.Fatalf("Merged = %v", merged)
	}
	assertStatus(t, c, []StatusItem{
		{ID: "c", Position: 1, Epoch: 1, Building: true},
		{ID: "d", Position: 2, Epoch: 1, Building: true},
	})
}

func TestFailureBumpsEveryLaterItemIncludingWaiting(t *testing.T) {
	c, err := NewCoordinator(2, 4)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b", "c", "d")

	got, err := c.Report("b", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	wantResult := Result{Removed: "b", Bumped: []string{"c", "d"}}
	if !resultsEqual(got, wantResult) {
		t.Fatalf("Report = %+v, want %+v", got, wantResult)
	}
	assertStatus(t, c, []StatusItem{
		{ID: "a", Position: 1, Epoch: 1, Building: true},
		{ID: "c", Position: 2, Epoch: 2, Building: true},
		{ID: "d", Position: 3, Epoch: 2, Building: false},
	})

	if _, err := c.Report("c", 1, true); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("old pass report = %v, want ErrStaleEpoch", err)
	}
	if _, err := c.Report("d", 2, true); !errors.Is(err, ErrOutsideBuildWindow) {
		t.Fatalf("waiting report = %v, want ErrOutsideBuildWindow", err)
	}
	assertStatus(t, c, []StatusItem{
		{ID: "a", Position: 1, Epoch: 1, Building: true},
		{ID: "c", Position: 2, Epoch: 2, Building: true},
		{ID: "d", Position: 3, Epoch: 2, Building: false},
	})
}

func TestFailureAtPositionOneAndTail(t *testing.T) {
	c, err := NewCoordinator(3, 3)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b")
	got, err := c.Report("a", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Removed: "a", Bumped: []string{"b"}}); !resultsEqual(got, want) {
		t.Fatalf("head failure = %+v, want %+v", got, want)
	}
	assertStatus(t, c, []StatusItem{{ID: "b", Position: 1, Epoch: 2, Building: true}})

	requireEnqueue(t, c, "c")
	got, err = c.Report("c", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Removed: "c", Bumped: []string{}}); !resultsEqual(got, want) {
		t.Fatalf("tail failure = %+v, want %+v", got, want)
	}
	assertStatus(t, c, []StatusItem{{ID: "b", Position: 1, Epoch: 2, Building: true}})
}

func TestBuildWindowBoundary(t *testing.T) {
	waiting, err := NewCoordinator(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, waiting, "a", "b", "c")
	if _, err := waiting.Report("c", 1, true); !errors.Is(err, ErrOutsideBuildWindow) {
		t.Fatalf("position B+1 report = %v, want ErrOutsideBuildWindow", err)
	}

	c, err := NewCoordinator(2, 3)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b", "c")

	got, err := c.Report("b", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Removed: "b", Bumped: []string{"c"}}); !resultsEqual(got, want) {
		t.Fatalf("position B report = %+v, want %+v", got, want)
	}
}

func TestLaterPassMergesPrefixAndEarlierReportBecomesUnknown(t *testing.T) {
	c, err := NewCoordinator(3, 3)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b", "c")

	got, err := c.Report("c", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	if want := (Result{Merged: []string{"a", "b", "c"}, Bumped: []string{}}); !resultsEqual(got, want) {
		t.Fatalf("later pass = %+v, want %+v", got, want)
	}
	if merged := c.Merged(); fmt.Sprint(merged) != "[a b c]" {
		t.Fatalf("Merged = %v", merged)
	}
	if _, err := c.Report("b", 1, true); !errors.Is(err, ErrNotInQueue) {
		t.Fatalf("earlier late report = %v, want ErrNotInQueue", err)
	}
}

func TestMergedRejectedButRemovedCanReenqueue(t *testing.T) {
	c, err := NewCoordinator(3, 3)
	if err != nil {
		t.Fatal(err)
	}
	requireEnqueue(t, c, "a", "b")
	if _, err := c.Report("a", 1, true); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Report("b", 1, false); err != nil {
		t.Fatal(err)
	}

	if err := c.Enqueue("a"); !errors.Is(err, ErrAlreadyMerged) {
		t.Fatalf("Enqueue merged = %v, want ErrAlreadyMerged", err)
	}
	if err := c.Enqueue("b"); err != nil {
		t.Fatalf("Enqueue removed = %v", err)
	}
	assertStatus(t, c, []StatusItem{{ID: "b", Position: 1, Epoch: 1, Building: true}})
}

func TestErrorPrioritiesAndRejectionHasNoEffect(t *testing.T) {
	type testCase struct {
		name  string
		setup func(*testing.T) *Coordinator
		run   func(*Coordinator) error
		want  error
	}

	base := func(buildWindow, capacity int, ids ...string) func(*testing.T) *Coordinator {
		return func(t *testing.T) *Coordinator {
			c, err := NewCoordinator(buildWindow, capacity)
			if err != nil {
				t.Fatal(err)
			}
			requireEnqueue(t, c, ids...)
			return c
		}
	}

	tests := []struct {
		name  string
		setup func(*testing.T) *Coordinator
		run   func(*Coordinator) error
		want  error
	}{
		{
			name:  "constructor build window before capacity",
			setup: func(*testing.T) *Coordinator { return nil },
			run: func(*Coordinator) error {
				_, err := NewCoordinator(0, 0)
				return err
			},
			want: ErrInvalidBuildWindow,
		},
		{
			name:  "constructor capacity",
			setup: func(*testing.T) *Coordinator { return nil },
			run: func(*Coordinator) error {
				_, err := NewCoordinator(1, 0)
				return err
			},
			want: ErrInvalidCapacity,
		},
		{
			name:  "enqueue empty before full queue",
			setup: base(1, 1, "x"),
			run:   func(c *Coordinator) error { return c.Enqueue("") },
			want:  ErrEmptyID,
		},
		{
			name: "enqueue merged before full",
			setup: func(t *testing.T) *Coordinator {
				c := base(1, 1, "a")(t)
				if _, err := c.Report("a", 1, true); err != nil {
					t.Fatal(err)
				}
				return c
			},
			run:  func(c *Coordinator) error { return c.Enqueue("a") },
			want: ErrAlreadyMerged,
		},
		{
			name:  "enqueue queued before full",
			setup: base(1, 1, "c"),
			run:   func(c *Coordinator) error { return c.Enqueue("c") },
			want:  ErrAlreadyQueued,
		},
		{
			name:  "enqueue full",
			setup: base(1, 1, "c"),
			run:   func(c *Coordinator) error { return c.Enqueue("z") },
			want:  ErrQueueFull,
		},
		{
			name:  "report missing before window and epoch",
			setup: base(2, 4, "a", "b", "c", "d"),
			run:   func(c *Coordinator) error { _, err := c.Report("", 9, true); return err },
			want:  ErrNotInQueue,
		},
		{
			name: "report window before epoch",
			setup: func(t *testing.T) *Coordinator {
				c := base(2, 4, "a", "b", "c", "d")(t)
				if _, err := c.Report("b", 1, false); err != nil {
					t.Fatal(err)
				}
				return c
			},
			run:  func(c *Coordinator) error { _, err := c.Report("d", 9, true); return err },
			want: ErrOutsideBuildWindow,
		},
		{
			name: "report stale epoch",
			setup: func(t *testing.T) *Coordinator {
				c := base(2, 4, "a", "b", "c", "d")(t)
				if _, err := c.Report("b", 1, false); err != nil {
					t.Fatal(err)
				}
				return c
			},
			run:  func(c *Coordinator) error { _, err := c.Report("c", 1, true); return err },
			want: ErrStaleEpoch,
		},
		{
			name:  "dequeue missing",
			setup: base(2, 3, "a", "b", "c"),
			run:   func(c *Coordinator) error { _, err := c.Dequeue("missing"); return err },
			want:  ErrNotInQueue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.setup(t)
			var before []StatusItem
			if c != nil {
				before = c.Status()
			}
			err := tt.run(c)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if c != nil {
				assertStatus(t, c, before)
			}
		})
	}
}

func TestConcurrentIdenticalPassReportsExactlyOneSucceeds(t *testing.T) {
	c, err := NewCoordinator(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Enqueue("a"); err != nil {
		t.Fatal(err)
	}

	const reports = 32
	var wg sync.WaitGroup
	var mu sync.Mutex
	successes, unknown := 0, 0
	for index := 0; index < reports; index++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := c.Report("a", 1, true)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil && fmt.Sprint(result.Merged) == "[a]":
				successes++
			case errors.Is(err, ErrNotInQueue):
				unknown++
			default:
				t.Errorf("unexpected report result=%+v err=%v", result, err)
			}
		}()
	}
	wg.Wait()

	if successes != 1 || unknown != reports-1 {
		t.Fatalf("successes = %d, not-in-queue = %d, want 1 and %d", successes, unknown, reports-1)
	}
}

func resultsEqual(got, want Result) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

type naiveItem struct {
	id    string
	epoch int
}

type naiveCoordinator struct {
	buildWindow int
	capacity    int
	items       []naiveItem
	merged      map[string]struct{}
	mergedOrder []string
}

func newNaive(buildWindow, capacity int) (*naiveCoordinator, error) {
	if buildWindow < 1 {
		return nil, ErrInvalidBuildWindow
	}
	if capacity < 1 {
		return nil, ErrInvalidCapacity
	}
	return &naiveCoordinator{
		buildWindow: buildWindow,
		capacity:    capacity,
		merged:      map[string]struct{}{},
	}, nil
}

func (n *naiveCoordinator) find(id string) int {
	for index, item := range n.items {
		if item.id == id {
			return index
		}
	}
	return -1
}

func (n *naiveCoordinator) enqueue(id string) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := n.merged[id]; ok {
		return ErrAlreadyMerged
	}
	if n.find(id) >= 0 {
		return ErrAlreadyQueued
	}
	if len(n.items) == n.capacity {
		return ErrQueueFull
	}
	n.items = append(n.items, naiveItem{id: id, epoch: 1})
	return nil
}

func (n *naiveCoordinator) report(id string, epoch int, passed bool) (Result, error) {
	position := n.find(id)
	if position < 0 {
		return Result{}, ErrNotInQueue
	}
	if position+1 > n.buildWindow {
		return Result{}, ErrOutsideBuildWindow
	}
	if n.items[position].epoch != epoch {
		return Result{}, ErrStaleEpoch
	}

	result := Result{Merged: []string{}, Bumped: []string{}}
	if passed {
		for _, item := range n.items[:position+1] {
			result.Merged = append(result.Merged, item.id)
			n.merged[item.id] = struct{}{}
			n.mergedOrder = append(n.mergedOrder, item.id)
		}
		n.items = append([]naiveItem(nil), n.items[position+1:]...)
		return result, nil
	}

	result.Removed = id
	oldItems := n.items
	n.items = append(make([]naiveItem, 0, len(oldItems)-1), oldItems[:position]...)
	for _, item := range oldItems[position+1:] {
		item.epoch++
		n.items = append(n.items, item)
		result.Bumped = append(result.Bumped, item.id)
	}
	return result, nil
}

func (n *naiveCoordinator) dequeue(id string) (Result, error) {
	position := n.find(id)
	if position < 0 {
		return Result{}, ErrNotInQueue
	}

	result := Result{Removed: id, Merged: []string{}, Bumped: []string{}}
	oldItems := n.items
	n.items = append(make([]naiveItem, 0, len(oldItems)-1), oldItems[:position]...)
	for _, item := range oldItems[position+1:] {
		item.epoch++
		n.items = append(n.items, item)
		result.Bumped = append(result.Bumped, item.id)
	}
	return result, nil
}

func (n *naiveCoordinator) status() []StatusItem {
	status := make([]StatusItem, len(n.items))
	for index, item := range n.items {
		status[index] = StatusItem{
			ID:       item.id,
			Position: index + 1,
			Epoch:    item.epoch,
			Building: index+1 <= n.buildWindow,
		}
	}
	return status
}

type randomOperation struct {
	kind   string
	id     string
	epoch  int
	passed bool
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	const sequences = 2000
	idPool := []string{"", "a", "b", "c", "d", "e", "f", "x", "y", "z", "unknown"}

	for sequence := 0; sequence < sequences; sequence++ {
		rng := rand.New(rand.NewSource(int64(sequence + 1)))
		buildWindow := rng.Intn(4) + 1
		capacity := rng.Intn(5) + 1
		actual, err := NewCoordinator(buildWindow, capacity)
		if err != nil {
			t.Fatalf("sequence %d: NewCoordinator: %v", sequence, err)
		}
		expected, err := newNaive(buildWindow, capacity)
		if err != nil {
			t.Fatalf("sequence %d: newNaive: %v", sequence, err)
		}
		replay, err := NewCoordinator(buildWindow, capacity)
		if err != nil {
			t.Fatalf("sequence %d: replay NewCoordinator: %v", sequence, err)
		}

		steps := rng.Intn(61) + 20
		for step := 0; step < steps; step++ {
			op := randomOperation{kind: "enqueue", id: idPool[rng.Intn(len(idPool))]}
			switch rng.Intn(10) {
			case 0, 1, 2:
				op.kind = "enqueue"
			case 3, 4, 5, 6:
				op.kind = "report"
				op.id = idPool[rng.Intn(len(idPool))]
				if position := expected.find(op.id); position >= 0 {
					if rng.Intn(4) == 0 {
						op.epoch = expected.items[position].epoch + rng.Intn(3) - 1
						if op.epoch == expected.items[position].epoch {
							op.epoch++
						}
					} else {
						op.epoch = expected.items[position].epoch
					}
				} else {
					op.epoch = rng.Intn(5) + 1
				}
				op.passed = rng.Intn(2) == 0
			default:
				op.kind = "dequeue"
				op.id = idPool[rng.Intn(len(idPool))]
			}
			beforeStatus := fmt.Sprint(expected.status())
			actualResult, actualErr := runOperation(actual, op)
			expectedResult, expectedErr := runNaive(expected, op)
			replayResult, replayErr := runOperation(replay, op)
			basis := decisionBasis(expected, op, expectedErr, expectedResult)
			t.Logf("seq=%04d step=%02d B=%d Cap=%d before=%s input=%s actual={result=%v err=%v} expected={result=%v err=%v} basis=%q",
				sequence+1, step, buildWindow, capacity, beforeStatus, formatOperation(op),
				actualResult, errorName(actualErr), expectedResult, errorName(expectedErr), basis)

			if errorName(actualErr) != errorName(expectedErr) {
				t.Fatalf("sequence %d step %d %s error = %v, want %v", sequence+1, step, formatOperation(op), actualErr, expectedErr)
			}
			if fmt.Sprint(actualResult) != fmt.Sprint(expectedResult) {
				t.Fatalf("sequence %d step %d %s result = %v, want %v", sequence+1, step, formatOperation(op), actualResult, expectedResult)
			}
			if fmt.Sprint(actual.Status()) != fmt.Sprint(expected.status()) {
				t.Fatalf("sequence %d step %d status = %v, want %v", sequence+1, step, actual.Status(), expected.status())
			}
			if fmt.Sprint(actual.Merged()) != fmt.Sprint(expected.mergedOrder) {
				t.Fatalf("sequence %d step %d merged = %v, want %v", sequence+1, step, actual.Merged(), expected.mergedOrder)
			}
			replayRecord := fmt.Sprintf("{result=%v err=%v}", replayResult, errorName(replayErr))
			if replayRecord != fmt.Sprintf("{result=%v err=%v}", actualResult, errorName(actualErr)) {
				t.Fatalf("sequence %d step %d replay mismatch: %s != %s", sequence+1, step, replayRecord, basis)
			}
		}
	}
}

func runOperation(c *Coordinator, op randomOperation) (Result, error) {
	switch op.kind {
	case "enqueue":
		return Result{}, c.Enqueue(op.id)
	case "report":
		return c.Report(op.id, op.epoch, op.passed)
	default:
		return c.Dequeue(op.id)
	}
}

func runNaive(n *naiveCoordinator, op randomOperation) (Result, error) {
	switch op.kind {
	case "enqueue":
		return Result{}, n.enqueue(op.id)
	case "report":
		return n.report(op.id, op.epoch, op.passed)
	default:
		return n.dequeue(op.id)
	}
}

func formatOperation(op randomOperation) string {
	switch op.kind {
	case "enqueue":
		return fmt.Sprintf("Enqueue(%q)", op.id)
	case "report":
		return fmt.Sprintf("Report(%q,%d,%t)", op.id, op.epoch, op.passed)
	default:
		return fmt.Sprintf("Dequeue(%q)", op.id)
	}
}

func decisionBasis(n *naiveCoordinator, op randomOperation, err error, result Result) string {
	if err != nil {
		return "rejected: " + errorName(err)
	}
	if len(result.Merged) > 0 {
		return fmt.Sprintf("accepted: merge prefix %v; surviving epochs remain unchanged", result.Merged)
	}
	if op.kind == "enqueue" {
		return "accepted: append at tail with epoch 1"
	}
	return fmt.Sprintf("accepted: remove %s and increment epochs of successors %v", result.Removed, result.Bumped)
}

func errorName(err error) string {
	switch {
	case err == nil:
		return "<nil>"
	case errors.Is(err, ErrInvalidBuildWindow):
		return "ErrInvalidBuildWindow"
	case errors.Is(err, ErrInvalidCapacity):
		return "ErrInvalidCapacity"
	case errors.Is(err, ErrEmptyID):
		return "ErrEmptyID"
	case errors.Is(err, ErrAlreadyMerged):
		return "ErrAlreadyMerged"
	case errors.Is(err, ErrAlreadyQueued):
		return "ErrAlreadyQueued"
	case errors.Is(err, ErrQueueFull):
		return "ErrQueueFull"
	case errors.Is(err, ErrNotInQueue):
		return "ErrNotInQueue"
	case errors.Is(err, ErrOutsideBuildWindow):
		return "ErrOutsideBuildWindow"
	case errors.Is(err, ErrStaleEpoch):
		return "ErrStaleEpoch"
	default:
		return err.Error()
	}
}
