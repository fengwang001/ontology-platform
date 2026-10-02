package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustAppend(t *testing.T, c *Coordinator, term int) int {
	t.Helper()
	idx, err := c.Append(term)
	if err != nil {
		t.Fatalf("Append(%d): %v", term, err)
	}
	return idx
}

func mustCommit(t *testing.T, c *Coordinator, idx int) {
	t.Helper()
	if err := c.Commit(idx); err != nil {
		t.Fatalf("Commit(%d): %v", idx, err)
	}
}

func applyOK(t *testing.T, c *Coordinator, idx int, want bool) {
	t.Helper()
	got, err := c.Apply(idx)
	if err != nil || got != want {
		t.Fatalf("Apply(%d) = (%v, %v), want (%v, nil)", idx, got, err, want)
	}
}

func stateOf(c *Coordinator) []int {
	return []int{
		c.last, c.commit, c.applied,
		c.snapIndex, c.snapTerm,
		c.baseIndex, c.baseTerm,
		c.removed,
	}
}

func assertState(t *testing.T, c *Coordinator, want []int) {
	t.Helper()
	got := stateOf(c)
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("state = %v, want %v", got, want)
		}
	}
}

func exampleCoordinator(t *testing.T) *Coordinator {
	t.Helper()
	c, err := New(2, 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []int{1, 1, 1, 2, 2, 2, 3, 3} {
		mustAppend(t, c, term)
	}
	mustCommit(t, c, 8)
	applyOK(t, c, 3, false)
	applyOK(t, c, 4, true)
	assertState(t, c, []int{8, 8, 4, 4, 2, 2, 1, 2})
	return c
}

func TestNewAndAppendValidation(t *testing.T) {
	for _, args := range [][3]int{{-1, 1, 0}, {0, 0, 0}, {0, 1, -1}} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrParam) {
			t.Fatalf("New(%v): %v", args, err)
		}
	}

	c, err := New(0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Append(0); !errors.Is(err, ErrParam) {
		t.Fatalf("Append(0): %v", err)
	}
	mustAppend(t, c, 2)
	if _, err := c.Append(1); !errors.Is(err, ErrTerm) {
		t.Fatalf("Append(1): %v", err)
	}
	if err := c.Commit(2); !errors.Is(err, ErrRange) {
		t.Fatalf("Commit above last: %v", err)
	}
}

func TestSnapshotThresholdAndRetainRules(t *testing.T) {
	t.Run("threshold equality", func(t *testing.T) {
		c, _ := New(2, 4, 100)
		for _, term := range []int{1, 1, 1, 2} {
			mustAppend(t, c, term)
		}
		mustCommit(t, c, 4)
		applyOK(t, c, 3, false)
		assertState(t, c, []int{4, 4, 3, 0, 0, 0, 0, 0})
		applyOK(t, c, 4, true)
		assertState(t, c, []int{4, 4, 4, 4, 2, 2, 1, 2})
	})

	t.Run("retain larger than snapshot", func(t *testing.T) {
		c, _ := New(10, 1, 100)
		mustAppend(t, c, 1)
		mustAppend(t, c, 2)
		mustCommit(t, c, 2)
		applyOK(t, c, 1, true)
		assertState(t, c, []int{2, 2, 1, 1, 1, 0, 0, 0})
	})

	t.Run("retain zero", func(t *testing.T) {
		c, _ := New(0, 1, 100)
		mustAppend(t, c, 1)
		mustAppend(t, c, 2)
		mustCommit(t, c, 2)
		applyOK(t, c, 1, true)
		assertState(t, c, []int{2, 2, 1, 1, 1, 1, 1, 1})
	})
}

func TestTermAtErrors(t *testing.T) {
	c := exampleCoordinator(t)
	if term, err := c.TermAt(2); err != nil || term != 1 {
		t.Fatalf("TermAt(base) = (%d, %v)", term, err)
	}
	if _, err := c.TermAt(1); !errors.Is(err, ErrCompacted) {
		t.Fatalf("TermAt(1): %v", err)
	}
	if _, err := c.TermAt(9); !errors.Is(err, ErrRange) {
		t.Fatalf("TermAt(9): %v", err)
	}
}

func TestPlanBoundaryUsesBaseTerm(t *testing.T) {
	c := exampleCoordinator(t)
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}

	if err := c.Retreat("p", 3); err != nil {
		t.Fatal(err)
	}
	want := Plan{Kind: PlanAppend, PrevIndex: 2, PrevTerm: 1, From: 3, To: 8}
	if got, err := c.Plan("p"); err != nil || got != want {
		t.Fatalf("Plan = %+v, %v; want %+v", got, err, want)
	}

	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	want = Plan{Kind: PlanNeedSnapshot, SnapIndex: 4, SnapTerm: 2}
	if got, err := c.Plan("p"); err != nil || got != want {
		t.Fatalf("Plan = %+v, %v; want %+v", got, err, want)
	}
}

func TestInFlightSnapshotFinishReleasesCut(t *testing.T) {
	c := exampleCoordinator(t)
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}
	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	if s, term, err := c.StartSnapshot("p"); err != nil || s != 4 || term != 2 {
		t.Fatalf("StartSnapshot = (%d, %d, %v)", s, term, err)
	}

	applyOK(t, c, 8, true)
	assertState(t, c, []int{8, 8, 8, 8, 3, 4, 2, 4})
	if got, err := c.Plan("p"); err != nil || got.Kind != PlanInstalling || got.SnapIndex != 4 {
		t.Fatalf("Plan during install = %+v, %v", got, err)
	}

	if err := c.FinishSnapshot("p"); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, []int{8, 8, 8, 8, 3, 6, 2, 6})
	want := Plan{Kind: PlanNeedSnapshot, SnapIndex: 8, SnapTerm: 3}
	if got, err := c.Plan("p"); err != nil || got != want {
		t.Fatalf("Plan after finish = %+v, %v; want %+v", got, err, want)
	}
}

func TestAbortSnapshotContinuesCompaction(t *testing.T) {
	c := exampleCoordinator(t)
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}
	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.StartSnapshot("p"); err != nil {
		t.Fatal(err)
	}
	applyOK(t, c, 8, true)
	if err := c.AbortSnapshot("p"); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, []int{8, 8, 8, 8, 3, 6, 2, 6})
	p := c.peers["p"]
	if p.match != 0 || p.next != 2 {
		t.Fatalf("peer after abort = match %d next %d", p.match, p.next)
	}
}

func TestNeighborProtectionAndAckDeferred(t *testing.T) {
	build := func(lag int) *Coordinator {
		c, _ := New(0, 1, lag)
		for range 5 {
			mustAppend(t, c, 1)
		}
		mustCommit(t, c, 5)
		if err := c.AddPeer("p"); err != nil {
			t.Fatal(err)
		}
		return c
	}

	equal := build(5)
	applyOK(t, equal, 5, true)
	if equal.baseIndex != 0 || equal.removed != 0 {
		t.Fatalf("equal lag compacted to base=%d removed=%d", equal.baseIndex, equal.removed)
	}

	tooFar := build(4)
	applyOK(t, tooFar, 5, true)
	if tooFar.baseIndex != 5 || tooFar.removed != 5 {
		t.Fatalf("lag+1 compacted to base=%d removed=%d", tooFar.baseIndex, tooFar.removed)
	}

	if err := equal.Ack("p", 1); err != nil {
		t.Fatal(err)
	}
	if equal.baseIndex != 0 {
		t.Fatal("Ack triggered Compact")
	}
	equal.Compact()
	if equal.baseIndex != 1 || equal.removed != 1 {
		t.Fatalf("explicit Compact = base %d removed %d", equal.baseIndex, equal.removed)
	}
}

func TestDropPeerReleasesFixpoint(t *testing.T) {
	c := exampleCoordinator(t)
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}
	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.StartSnapshot("p"); err != nil {
		t.Fatal(err)
	}
	applyOK(t, c, 8, true)
	if err := c.DropPeer("p"); err != nil {
		t.Fatal(err)
	}
	if c.baseIndex != 6 || c.removed != 6 {
		t.Fatalf("DropPeer compact = base %d removed %d", c.baseIndex, c.removed)
	}
	if _, err := c.Plan("p"); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("Plan after drop: %v", err)
	}
}

func TestPeerRangeAndErrorOrder(t *testing.T) {
	c, _ := New(0, 1, 100)
	mustAppend(t, c, 1)
	mustCommit(t, c, 1)

	if err := c.AddPeer(""); !errors.Is(err, ErrParam) {
		t.Fatalf("AddPeer empty: %v", err)
	}
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddPeer("p"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate peer: %v", err)
	}
	if err := c.Ack("missing", 0); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("Ack unknown: %v", err)
	}
	if err := c.Ack("p", 2); !errors.Is(err, ErrRange) {
		t.Fatalf("Ack above last: %v", err)
	}
	if err := c.Ack("p", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Ack("p", 1); err != nil {
		t.Fatalf("Ack(1): %v", err)
	}
	if err := c.Retreat("p", 1); !errors.Is(err, ErrRange) {
		t.Fatalf("Retreat to match: %v", err)
	}
	if err := c.Retreat("p", 3); !errors.Is(err, ErrRange) {
		t.Fatalf("Retreat above next: %v", err)
	}
	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.StartSnapshot("missing"); !errors.Is(err, ErrUnknownPeer) {
		t.Fatalf("Start unknown: %v", err)
	}
	if err := c.FinishSnapshot("p"); !errors.Is(err, ErrNotInFlight) {
		t.Fatalf("Finish without snapshot: %v", err)
	}
	if _, _, err := c.StartSnapshot("p"); !errors.Is(err, ErrNotNeeded) {
		t.Fatalf("Start append plan: %v", err)
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	c := exampleCoordinator(t)
	if err := c.AddPeer("p"); err != nil {
		t.Fatal(err)
	}
	if err := c.Retreat("p", 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.StartSnapshot("p"); err != nil {
		t.Fatal(err)
	}
	before := stateOf(c)
	beforePeer := *c.peers["p"]

	_, _ = c.Apply(9)
	_, _ = c.Append(1)
	_ = c.Commit(9)
	_ = c.Ack("p", 9)
	_ = c.Retreat("p", 2)
	if _, _, err := c.StartSnapshot("p"); !errors.Is(err, ErrInFlight) {
		t.Fatalf("duplicate StartSnapshot: %v", err)
	}
	_ = c.FinishSnapshot("missing")

	assertState(t, c, before)
	if *c.peers["p"] != beforePeer {
		t.Fatalf("peer changed: %+v want %+v", *c.peers["p"], beforePeer)
	}
}

func TestConcurrentCalls(t *testing.T) {
	c, _ := New(0, 1, 100)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "p"
			if i%4 == 0 {
				id = ""
			}
			idx, _ := c.Append(1)
			_ = c.Commit(idx)
			_, _ = c.Apply(idx)
			_ = c.AddPeer("p")
			_, _ = c.Plan(id)
			_ = c.Ack("p", idx)
			_ = c.FinishSnapshot("p")
			_ = c.AbortSnapshot("p")
			_ = c.DropPeer(id)
			_, _ = c.TermAt(idx)
			c.Compact()
		}(i)
	}
	wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()
	if !(c.baseIndex <= c.snapIndex && c.snapIndex <= c.applied && c.applied <= c.commit && c.commit <= c.last) {
		t.Fatalf("invariant violated: base=%d snap=%d applied=%d commit=%d last=%d",
			c.baseIndex, c.snapIndex, c.applied, c.commit, c.last)
	}
	if c.removed != c.baseIndex {
		t.Fatalf("removed = %d, base = %d", c.removed, c.baseIndex)
	}
}
