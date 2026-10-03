package triplesync_test

import (
	"errors"
	"testing"

	"ontology/triplesync"
)

func TestSplitLocAndContentMerge(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h0"},
	}
	local := triplesync.Snapshot{
		1: {Parent: 0, Name: "b", Hash: "h0"},
	}
	remote := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h1"},
	}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{
		{Kind: triplesync.ActionSetHash, ID: 1, Hash: "h1"},
	})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionSetLoc, ID: 1, Parent: 0, Name: "b"},
	})
}

// Two independent cross-side collision groups plus an x-append chain exercise
// multi-group, deterministic same-name resolution.
func TestNameCollisionMultiParty(t *testing.T) {
	base := triplesync.Snapshot{}
	local := triplesync.Snapshot{
		5: {Parent: 0, Name: "a", Hash: "h5"},
		8: {Parent: 0, Name: "b", Hash: "h8"},
	}
	remote := triplesync.Snapshot{
		4: {Parent: 0, Name: "a", Hash: "h4"},
		7: {Parent: 0, Name: "b", Hash: "h7"},
	}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 4, Parent: 0, Name: "a", Hash: "h4"},
		{Kind: triplesync.ActionCreate, ID: 7, Parent: 0, Name: "b", Hash: "h7"},
		{Kind: triplesync.ActionSetLoc, ID: 5, Parent: 0, Name: "a.c5"},
		{Kind: triplesync.ActionSetLoc, ID: 8, Parent: 0, Name: "b.c8"},
	})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 5, Parent: 0, Name: "a.c5", Hash: "h5"},
		{Kind: triplesync.ActionCreate, ID: 8, Parent: 0, Name: "b.c8", Hash: "h8"},
	})
}

func TestNameCollisionXAppend(t *testing.T) {
	// Remote already contains "a.c5", so the renamed local loser must keep
	// appending "x" until unique.
	base := triplesync.Snapshot{}
	local := triplesync.Snapshot{
		5: {Parent: 0, Name: "a", Hash: "h5"},
	}
	remote := triplesync.Snapshot{
		4: {Parent: 0, Name: "a", Hash: "h4"},
		6: {Parent: 0, Name: "a.c5", Hash: "h6"},
	}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 4, Parent: 0, Name: "a", Hash: "h4"},
		{Kind: triplesync.ActionCreate, ID: 6, Parent: 0, Name: "a.c5", Hash: "h6"},
		{Kind: triplesync.ActionSetLoc, ID: 5, Parent: 0, Name: "a.c5x"},
	})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 5, Parent: 0, Name: "a.c5x", Hash: "h5"},
	})
}

func TestMergedCycleRejected(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Dir: true},
		2: {Parent: 0, Name: "b", Dir: true},
		3: {Parent: 2, Name: "c", Dir: true},
	}
	local := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Dir: true},
		2: {Parent: 0, Name: "b", Dir: true},
		3: {Parent: 1, Name: "c", Dir: true},
	}
	remote := triplesync.Snapshot{
		1: {Parent: 3, Name: "a", Dir: true},
		2: {Parent: 0, Name: "b", Dir: true},
		3: {Parent: 2, Name: "c", Dir: true},
	}
	p, _ := triplesync.NewPlanner(base)
	_, err := p.Plan(local, remote)
	if !errors.Is(err, triplesync.ErrMergeInvalid) {
		t.Fatalf("err = %v, want ErrMergeInvalid", err)
	}
}

func TestCommitRules(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h"},
	}
	local := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h2"},
	}
	remote := triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h3"},
	}
	p, _ := triplesync.NewPlanner(base)
	if _, err := p.Commit(local, remote); !errors.Is(err, triplesync.ErrHasConflicts) {
		t.Fatalf("commit conflicts: %v", err)
	}
	if _, err := p.Commit(local, remote); !errors.Is(err, triplesync.ErrHasConflicts) {
		t.Fatalf("base changed by rejected commit: %v", err)
	}

	plan, err := p.Commit(local, triplesync.Snapshot{1: {Parent: 0, Name: "a", Hash: "h"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToLocal) != 0 || len(plan.ToRemote) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	plan2, err := p.Plan(local, local)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan2.ToLocal) != 0 || len(plan2.ToRemote) != 0 || len(plan2.Conflicts) != 0 {
		t.Fatalf("base not advanced: %+v", plan2)
	}
}

func TestDeleteOrdering(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "sub", Dir: true},
		3: {Parent: 2, Name: "f", Hash: "h3"},
	}
	local := triplesync.Snapshot{}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, base)
	if err != nil {
		t.Fatal(err)
	}
	// Local deleted the whole chain (each DeleteModify-style silent delete
	// because local also deleted parents); remote is unchanged. Since local
	// has no surviving child, all three deletes push to remote in
	// depth-descending order.
	if len(plan.ToRemote) != 3 ||
		plan.ToRemote[0].ID != 3 || plan.ToRemote[1].ID != 2 || plan.ToRemote[2].ID != 1 {
		t.Fatalf("unexpected delete order: %+v", plan.ToRemote)
	}
}

func TestBothSidesDeleteNoAction(t *testing.T) {
	base := triplesync.Snapshot{1: {Parent: 0, Name: "f", Hash: "h"}}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(triplesync.Snapshot{}, triplesync.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToLocal) != 0 || len(plan.ToRemote) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("expected empty plan, got %+v", plan)
	}
}

func TestIdenticalTwoSidedChange(t *testing.T) {
	base := triplesync.Snapshot{1: {Parent: 0, Name: "f", Hash: "h"}}
	s := triplesync.Snapshot{1: {Parent: 0, Name: "g", Hash: "h2"}}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(s, s)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.ToLocal) != 0 || len(plan.ToRemote) != 0 || len(plan.Conflicts) != 0 {
		t.Fatalf("identical changes should produce no actions, got %+v", plan)
	}
}

func TestSnapshotValidationErrors(t *testing.T) {
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 9, Name: "x", Hash: "h"}}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("dangling parent: %v", err)
	}
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 0, Name: "a/b", Hash: "h"}}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("slash name: %v", err)
	}
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 1, Name: "a", Hash: "h"}}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("self cycle: %v", err)
	}
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{
		1: {Parent: 0, Name: "a", Dir: true},
		2: {Parent: 0, Name: "a", Dir: true},
	}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("duplicate sibling: %v", err)
	}
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 0, Name: "a", Dir: true, Hash: "x"}}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("dir hash: %v", err)
	}
	if _, err := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 0, Name: "a", Hash: ""}}); !errors.Is(err, triplesync.ErrBadBase) {
		t.Fatalf("empty file hash: %v", err)
	}
	p, _ := triplesync.NewPlanner(triplesync.Snapshot{1: {Parent: 0, Name: "a", Hash: "h"}})
	if _, err := p.Plan(triplesync.Snapshot{}, triplesync.Snapshot{2: {Parent: 3, Name: "x", Hash: "h"}}); !errors.Is(err, triplesync.ErrBadRemote) {
		t.Fatalf("bad remote: %v", err)
	}
	if _, err := p.Plan(triplesync.Snapshot{2: {Parent: 3, Name: "x", Hash: "h"}}, triplesync.Snapshot{}); !errors.Is(err, triplesync.ErrBadLocal) {
		t.Fatalf("bad local: %v", err)
	}
	if _, err := p.Plan(
		triplesync.Snapshot{1: {Parent: 0, Name: "a", Dir: true}},
		triplesync.Snapshot{1: {Parent: 0, Name: "a", Hash: "h"}},
	); !errors.Is(err, triplesync.ErrBadLocal) {
		t.Fatalf("dir mismatch local: %v", err)
	}
	p2, _ := triplesync.NewPlanner(triplesync.Snapshot{})
	if _, err := p2.Plan(
		triplesync.Snapshot{1: {Parent: 0, Name: "a", Dir: true}},
		triplesync.Snapshot{1: {Parent: 0, Name: "a", Hash: "h"}},
	); !errors.Is(err, triplesync.ErrBadRemote) {
		t.Fatalf("new id dir mismatch: %v", err)
	}
}

func TestFourConflictKinds(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "f", Hash: "h"},
		2: {Parent: 0, Name: "g", Hash: "h"},
		3: {Parent: 0, Name: "k", Hash: "h"},
	}
	local := triplesync.Snapshot{
		1: {Parent: 0, Name: "f", Hash: "hL"},
		2: {Parent: 0, Name: "gL", Hash: "h"},
		3: {Parent: 0, Name: "k", Hash: "hL"},
	}
	remote := triplesync.Snapshot{
		2: {Parent: 0, Name: "gR", Hash: "h"},
		3: {Parent: 0, Name: "k", Hash: "hR"},
	}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	want := []triplesync.Conflict{
		{ID: 1, Kind: triplesync.ConflictDeleteModify},
		{ID: 2, Kind: triplesync.ConflictLoc},
		{ID: 3, Kind: triplesync.ConflictContent},
	}
	if len(plan.Conflicts) != len(want) {
		t.Fatalf("conflicts = %v, want %v", plan.Conflicts, want)
	}
	for i := range want {
		if plan.Conflicts[i] != want[i] {
			t.Fatalf("conflict %d = %+v, want %+v", i, plan.Conflicts[i], want[i])
		}
	}
}

func TestVetoMultiLevelCascade(t *testing.T) {
	base := triplesync.Snapshot{
		10: {Parent: 0, Name: "a", Dir: true},
		11: {Parent: 10, Name: "b", Dir: true},
		12: {Parent: 11, Name: "c", Dir: true},
	}
	local := triplesync.Snapshot{
		10: {Parent: 0, Name: "a", Dir: true},
		11: {Parent: 10, Name: "b", Dir: true},
		12: {Parent: 11, Name: "c", Dir: true},
		13: {Parent: 12, Name: "new", Hash: "z"},
	}
	remote := triplesync.Snapshot{}
	p, _ := triplesync.NewPlanner(base)
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 10, Parent: 0, Name: "a", Dir: true},
		{Kind: triplesync.ActionCreate, ID: 11, Parent: 10, Name: "b", Dir: true},
		{Kind: triplesync.ActionCreate, ID: 12, Parent: 11, Name: "c", Dir: true},
		{Kind: triplesync.ActionCreate, ID: 13, Parent: 12, Name: "new", Hash: "z"},
	})
}
