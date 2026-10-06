package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestRejectionChangesNothing verifies rejected operations add no audit entry
// and do not alter any observable state.
func TestRejectionChangesNothing(t *testing.T) {
	// Reference engine that performs only the accepted operations.
	clean := func() *Engine {
		e := newBasicEngine(t)
		addRev(t, e, "r1", "g1", 1)
		addRev(t, e, "r2", "g2", 1)
		must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
		return e
	}

	e := clean()
	v := e.Snapshot()
	tk := taskFor(v, "s", "r1").ID
	eventsBefore := len(v.Events)

	// A battery of rejected calls interleaved with one accepted submit.
	assertKind(t, e.Submit("", 0), ErrInvalidArgument)
	assertKind(t, e.Submit("ghost", 0), ErrNotFound)
	assertKind(t, e.Submit(tk, 3), ErrScore)  // off step-2 grid
	assertKind(t, e.Submit(tk, 12), ErrScore) // over max
	assertKind(t, e.Withdraw("ghost"), ErrNotFound)
	assertKind(t, e.Deactivate("ghost"), ErrNotFound)
	if got := len(e.Snapshot().Events); got != eventsBefore {
		t.Fatalf("rejected ops added %d audit events; want 0", got-eventsBefore)
	}

	must(t, e.Submit(tk, 2)) // the single accepted operation
	acceptedEvents := len(e.Snapshot().Events)
	assertKind(t, e.Submit(tk, 4), ErrTaskState) // duplicate
	assertKind(t, e.Withdraw(tk), ErrTaskState)  // already submitted
	assertKind(t, e.Submit(tk, 3), ErrTaskState) // state outranks score
	if got := len(e.Snapshot().Events); got != acceptedEvents {
		t.Fatalf("post-accept rejects added %d events; want 0", got-acceptedEvents)
	}

	want := clean()
	must(t, want.Submit(taskFor(want.Snapshot(), "s", "r1").ID, 2))
	if !equivSheetState(e.Snapshot(), want.Snapshot().Sheets) {
		t.Fatalf("rejected ops changed state:\ngot =%#v\nwant=%#v", e.Snapshot().Sheets, want.Snapshot().Sheets)
	}
}

func equivSheetState(a View, sheets []SheetView) bool {
	if len(a.Sheets) != len(sheets) {
		return false
	}
	for i := range a.Sheets {
		x, y := a.Sheets[i], sheets[i]
		if x.ID != y.ID || x.PendingTask != y.PendingTask || len(x.Tasks) != len(y.Tasks) {
			return false
		}
		for j := range x.Tasks {
			p, q := x.Tasks[j], y.Tasks[j]
			if p.ReviewerID != q.ReviewerID || p.Role != q.Role || p.Withdrawn != q.Withdrawn {
				return false
			}
			if (p.Score == nil) != (q.Score == nil) {
				return false
			}
			if p.Score != nil && *p.Score != *q.Score {
				return false
			}
		}
	}
	return true
}

// TestConcurrentEquivalentToSerial fires many operations from many goroutines.
// The mutex makes every accepted call linearizable; afterwards every finalized
// sheet has exactly two or three valid scores and all structural invariants
// hold, matching some serial order.
func TestConcurrentEquivalentToSerial(t *testing.T) {
	e := New()
	for _, g := range []string{"g1", "g2", "g3"} {
		must(t, e.AddGroup(Group{ID: g}))
	}
	must(t, e.AddQuestion(Question{ID: "q", MaxScore: 20, Step: 1, Threshold: 3}))

	const reviewers = 12
	for i := range reviewers {
		g := []string{"g1", "g2", "g3"}[i%3]
		addRev(t, e, fmt.Sprintf("r%02d", i), g, 1000)
	}
	const sheets = 60
	for i := range sheets {
		must(t, e.AddSheet(Sheet{
			ID: fmt.Sprintf("s%02d", i), Question: "q", Student: fmt.Sprintf("stu%02d", i%20),
		}))
	}

	var wg sync.WaitGroup
	v := e.Snapshot()
	var allTasks []TaskView
	for _, s := range v.Sheets {
		allTasks = append(allTasks, s.Tasks...)
	}
	for i, tk := range allTasks {
		if tk.ReviewerID == "" || tk.Withdrawn {
			continue
		}
		wg.Add(1)
		go func(id string, k int) {
			defer wg.Done()
			// Scores chosen so gaps are sometimes <= 3 and sometimes larger.
			_ = e.Submit(id, []int{10, 11, 18, 5, 10, 20}[k%6])
		}(tk.ID, i)
	}
	wg.Wait()

	v = e.Snapshot()
	for _, s := range v.Sheets {
		valid := 0
		groups := map[string]map[string]bool{}
		reviewers := map[string]bool{}
		for _, tk := range s.Tasks {
			if tk.Withdrawn || tk.Score == nil {
				continue
			}
			valid++
			reviewers[tk.ReviewerID] = true
			g := v.Reviewers[tk.ReviewerID].GroupID
			if groups[g] == nil {
				groups[g] = map[string]bool{}
			}
			groups[g][tk.ReviewerID] = true
		}
		if s.FinalScore == nil {
			continue // arbitration may still be pending under random gaps
		}
		if valid != 2 && valid != 3 {
			t.Fatalf("finalized sheet %s has %d valid scores, want 2 or 3", s.ID, valid)
		}
		if len(reviewers) != valid {
			t.Fatalf("sheet %s: a reviewer marked it twice", s.ID)
		}
		if valid == 2 && len(groups) != 2 {
			t.Fatalf("sheet %s: initial reviewers not from distinct groups", s.ID)
		}
		if valid == 3 && len(groups) != 3 {
			t.Fatalf("sheet %s: arbitrator not from a third group", s.ID)
		}
	}
}

func TestTieBreakMinLoadThenMinID(t *testing.T) {
	e := New()
	must(t, e.AddGroup(Group{ID: "g1"}))
	must(t, e.AddGroup(Group{ID: "g2"}))
	must(t, e.AddQuestion(Question{ID: "q", MaxScore: 10, Step: 1, Threshold: 2}))
	// Three g2 reviewers all at load 0; selection must be the smallest ID.
	for _, id := range []string{"zb", "aa", "mm"} {
		addRev(t, e, id, "g2", 10)
	}
	addRev(t, e, "only-g1", "g1", 10)
	must(t, e.AddSheet(Sheet{ID: "s", Question: "q", Student: "st"}))
	if taskFor(e.Snapshot(), "s", "aa").ID == "" {
		t.Fatalf("expected min-id tie-break to choose aa")
	}
	// Next sheet: aa is at load 1, others at 0 -> choose among 0-load: mm? no,
	// smallest of {mm, zb} = mm.
	must(t, e.AddSheet(Sheet{ID: "s2", Question: "q", Student: "st2"}))
	if taskFor(e.Snapshot(), "s2", "mm").ID == "" {
		t.Fatalf("expected min-load then min-id to choose mm")
	}
}
