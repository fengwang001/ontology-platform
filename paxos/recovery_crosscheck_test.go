package paxos

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naivePlan is an independent, deliberately straightforward per-slot
// reference implementation used to cross-check Planner.Plan. It also
// returns a human-readable rationale for every slot decision.
func naivePlan(reps []PromiseReport) (RecoveryPlan, []string, error) {
	start := 1
	maxSlot := 0
	for _, r := range reps {
		if r.Chosen+1 > start {
			start = r.Chosen + 1
		}
		for s := range r.Accepted {
			if s > maxSlot {
				maxSlot = s
			}
		}
	}

	type candidate struct {
		ballot int
		value  string
	}
	var entries []PlanEntry
	var rationale []string
	for s := start; s <= maxSlot; s++ {
		var cands []candidate
		for _, r := range reps {
			if av, ok := r.Accepted[s]; ok {
				cands = append(cands, candidate{av.Ballot, av.Value})
			}
		}
		if len(cands) == 0 {
			entries = append(entries, PlanEntry{Slot: s, Noop: true})
			rationale = append(rationale, fmt.Sprintf("slot %d: no accepts -> Noop (ballot 0)", s))
			continue
		}
		sort.Slice(cands, func(i, j int) bool { return cands[i].ballot > cands[j].ballot })
		top := cands[0]
		conflict := false
		for _, c := range cands {
			if c.ballot != top.ballot {
				break
			}
			if c.value != top.value {
				conflict = true
			}
		}
		rationale = append(rationale, fmt.Sprintf("slot %d: candidates %v -> highest ballot %d value %q", s, cands, top.ballot, top.value))
		if conflict {
			return RecoveryPlan{}, rationale, &ConflictError{Slot: s}
		}
		entries = append(entries, PlanEntry{Slot: s, Value: top.value, Ballot: top.ballot})
	}

	nextFree := maxSlot
	if start-1 > nextFree {
		nextFree = start - 1
	}
	nextFree++
	return RecoveryPlan{Start: start, NextFree: nextFree, Entries: entries}, rationale, nil
}

func describeReports(reps []PromiseReport) string {
	var sb strings.Builder
	for i, r := range reps {
		slots := make([]int, 0, len(r.Accepted))
		for s := range r.Accepted {
			slots = append(slots, s)
		}
		sort.Ints(slots)
		fmt.Fprintf(&sb, "  rep[%d] pb=%d chosen=%d accepted={", i, r.PB, r.Chosen)
		for _, s := range slots {
			av := r.Accepted[s]
			fmt.Fprintf(&sb, "%d:(%d,%q) ", s, av.Ballot, av.Value)
		}
		sb.WriteString("}\n")
	}
	return sb.String()
}

func TestPlanAgainstNaivePerSlotComputation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	values := []string{"", "a", "b", "cc"}

	for iter := 0; iter < 300; iter++ {
		n := 1 + rng.Intn(7)
		b := 1 + rng.Intn(6)
		majority := n/2 + 1
		k := majority + rng.Intn(n-majority+1)

		froms := rng.Perm(n)[:k]
		reps := make([]PromiseReport, k)
		for i := range reps {
			chosen := rng.Intn(6)
			var accepted map[int]AcceptedValue
			if extra := rng.Intn(4); extra > 0 && b >= 2 {
				accepted = make(map[int]AcceptedValue)
				for _, s := range rng.Perm(8)[:extra] {
					accepted[chosen+1+s] = AcceptedValue{
						Ballot: 1 + rng.Intn(b-1),
						Value:  values[rng.Intn(len(values))],
					}
				}
			}
			reps[i] = report(b, chosen, accepted)
		}

		want, rationale, wantErr := naivePlan(reps)

		p := mustPlanner(t, b, n)
		for i, from := range froms {
			mustAddPromise(t, p, from, reps[i])
		}
		got, gotErr := p.Plan()

		t.Logf("case %d: b=%d n=%d majority=%d reports=%d\n%s", iter, b, n, majority, k, describeReports(reps))
		t.Logf("case %d: rationale: %s", iter, strings.Join(rationale, "; "))

		var wantConflict, gotConflict *ConflictError
		switch {
		case errors.As(wantErr, &wantConflict):
			if !errors.As(gotErr, &gotConflict) {
				t.Fatalf("case %d: got err=%v, want conflict at slot %d", iter, gotErr, wantConflict.Slot)
			}
			if gotConflict.Slot != wantConflict.Slot {
				t.Fatalf("case %d: conflict slot=%d, want %d", iter, gotConflict.Slot, wantConflict.Slot)
			}
			t.Logf("case %d: both report conflict at slot %d", iter, wantConflict.Slot)
		case wantErr != nil:
			t.Fatalf("case %d: naive failed unexpectedly: %v", iter, wantErr)
		default:
			if gotErr != nil {
				t.Fatalf("case %d: Plan failed: %v (naive succeeded)", iter, gotErr)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("case %d:\n got %+v\nwant %+v", iter, got, want)
			}
			t.Logf("case %d: plan start=%d nextFree=%d entries=%+v", iter, got.Start, got.NextFree, got.Entries)
		}

		// Arrival order must not matter: replay in a different shuffle.
		if wantErr == nil {
			q := mustPlanner(t, b, n)
			for _, idx := range rng.Perm(k) {
				mustAddPromise(t, q, froms[idx], reps[idx])
			}
			again, err := q.Plan()
			if err != nil {
				t.Fatalf("case %d: replay Plan failed: %v", iter, err)
			}
			if !reflect.DeepEqual(again, want) {
				t.Fatalf("case %d: arrival order changed result:\n got %+v\nwant %+v", iter, again, want)
			}
		}
	}
}
