package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestDirectoryDeleteVetoExample(t *testing.T) {
	base := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
	}
	local := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
		3: {Parent: 1, Name: "g", Hash: "h3"},
	}
	remote := Snapshot{}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	wantLocal := []Action{{Op: Delete, ID: 2}}
	wantRemote := []Action{
		{Op: Create, ID: 1, Parent: 0, Name: "d", Dir: true},
		{Op: Create, ID: 3, Parent: 1, Name: "g", Hash: "h3"},
	}
	if !reflect.DeepEqual(got.ToLocal, wantLocal) {
		t.Fatalf("ToLocal = %#v, want %#v", got.ToLocal, wantLocal)
	}
	if !reflect.DeepEqual(got.ToRemote, wantRemote) {
		t.Fatalf("ToRemote = %#v, want %#v", got.ToRemote, wantRemote)
	}
}

func TestLocationAndContentMergeFromOppositeSides(t *testing.T) {
	base := Snapshot{1: {Parent: 0, Name: "f", Hash: "h"}}
	local := Snapshot{1: {Parent: 0, Name: "g", Hash: "h"}}
	remote := Snapshot{1: {Parent: 0, Name: "f", Hash: "h2"}}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	wantLocal := []Action{{Op: SetHash, ID: 1, Hash: "h2"}}
	wantRemote := []Action{{Op: SetLoc, ID: 1, Parent: 0, Name: "g"}}
	if !reflect.DeepEqual(got.ToLocal, wantLocal) || !reflect.DeepEqual(got.ToRemote, wantRemote) {
		t.Fatalf("plan = %#v", got)
	}
}

func TestConflictKinds(t *testing.T) {
	base := Snapshot{
		1: {Parent: 0, Name: "dm1", Hash: "b"},
		2: {Parent: 0, Name: "dm2", Hash: "b"},
		3: {Parent: 0, Name: "loc", Hash: "b"},
		4: {Parent: 0, Name: "content", Hash: "b"},
	}
	local := Snapshot{
		1: {Parent: 0, Name: "dm1", Hash: "l"},
		3: {Parent: 0, Name: "loc-l", Hash: "b"},
		4: {Parent: 0, Name: "content", Hash: "l"},
	}
	remote := Snapshot{
		2: {Parent: 0, Name: "dm2-mod", Hash: "b"},
		3: {Parent: 0, Name: "loc-r", Hash: "b"},
		4: {Parent: 0, Name: "content", Hash: "r"},
	}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	want := []Conflict{
		{ID: 1, Kind: DeleteModify},
		{ID: 2, Kind: DeleteModify},
		{ID: 3, Kind: Loc},
		{ID: 4, Kind: Content},
	}
	if !reflect.DeepEqual(got.Conflicts, want) {
		t.Fatalf("conflicts = %#v, want %#v", got.Conflicts, want)
	}
}

func TestMultiLevelDirectoryVetoCascade(t *testing.T) {
	base := Snapshot{
		1: {Parent: 0, Name: "a", Dir: true},
		2: {Parent: 1, Name: "b", Dir: true},
		3: {Parent: 2, Name: "f", Hash: "h3"},
	}
	local := Snapshot{
		1: {Parent: 0, Name: "a", Dir: true},
		2: {Parent: 1, Name: "b", Dir: true},
		3: {Parent: 2, Name: "f", Hash: "h3"},
		4: {Parent: 2, Name: "g", Hash: "h4"},
	}
	remote := Snapshot{}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	wantLocal := []Action{{Op: Delete, ID: 3}}
	wantRemote := []Action{
		{Op: Create, ID: 1, Name: "a", Dir: true},
		{Op: Create, ID: 2, Parent: 1, Name: "b", Dir: true},
		{Op: Create, ID: 4, Parent: 2, Name: "g", Hash: "h4"},
	}
	if !reflect.DeepEqual(got.ToLocal, wantLocal) || !reflect.DeepEqual(got.ToRemote, wantRemote) {
		t.Fatalf("plan = %#v", got)
	}
}

func TestRenameAppendsXUntilUnique(t *testing.T) {
	base := Snapshot{}
	local := Snapshot{
		5: {Parent: 0, Name: "a", Hash: "h5"},
		6: {Parent: 0, Name: "a.c5", Hash: "h6"},
		7: {Parent: 0, Name: "a.c5x", Hash: "h7"},
	}
	remote := Snapshot{4: {Parent: 0, Name: "a", Hash: "h4"}}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	wantLocal := []Action{
		{Op: Create, ID: 4, Name: "a", Hash: "h4"},
		{Op: SetLoc, ID: 5, Name: "a.c5xx"},
	}
	wantRemote := []Action{
		{Op: Create, ID: 5, Name: "a.c5xx", Hash: "h5"},
		{Op: Create, ID: 6, Name: "a.c5", Hash: "h6"},
		{Op: Create, ID: 7, Name: "a.c5x", Hash: "h7"},
	}
	if !reflect.DeepEqual(got.ToLocal, wantLocal) || !reflect.DeepEqual(got.ToRemote, wantRemote) {
		t.Fatalf("plan = %#v", got)
	}
}

func TestInvalidMergedStates(t *testing.T) {
	t.Run("cycle", func(t *testing.T) {
		base := Snapshot{
			1: {Parent: 0, Name: "a", Dir: true},
			2: {Parent: 0, Name: "b", Dir: true},
		}
		local := Snapshot{
			1: {Parent: 0, Name: "a", Dir: true},
			2: {Parent: 1, Name: "b", Dir: true},
		}
		remote := Snapshot{
			1: {Parent: 2, Name: "a", Dir: true},
			2: {Parent: 0, Name: "b", Dir: true},
		}
		planner, _ := NewPlanner(base)
		_, err := planner.Plan(local, remote)
		if !errors.Is(err, ErrMergeInvalid) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("dangling", func(t *testing.T) {
		state := Snapshot{2: {Parent: 99, Name: "f", Hash: "h2"}}
		if validateSnapshot(state) {
			t.Fatal("dangling final state was accepted")
		}
	})
}

func TestCommitUpdatesBase(t *testing.T) {
	base := Snapshot{1: {Parent: 0, Name: "f", Hash: "old"}}
	local := Snapshot{1: {Parent: 0, Name: "f", Hash: "new"}}
	remote := Snapshot{1: {Parent: 0, Name: "f", Hash: "old"}}
	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	merged, _, err := planner.Commit(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	want := Snapshot{1: {Parent: 0, Name: "f", Hash: "new"}}
	if !reflect.DeepEqual(merged, want) {
		t.Fatalf("merged = %#v", merged)
	}
}

func TestCommitConflictDoesNotUpdateBase(t *testing.T) {
	base := Snapshot{1: {Parent: 0, Name: "f", Hash: "old"}}
	local := Snapshot{1: {Parent: 0, Name: "f", Hash: "local"}}
	remote := Snapshot{1: {Parent: 0, Name: "f", Hash: "remote"}}
	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	_, plan, err := planner.Commit(local, remote)
	if !errors.Is(err, ErrHasConflicts) {
		t.Fatalf("err = %v", err)
	}
	if len(plan.Conflicts) != 1 || plan.Conflicts[0] != (Conflict{ID: 1, Kind: Content}) {
		t.Fatalf("plan = %#v", plan)
	}
	replay, err := planner.Plan(base, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay.ToLocal) != 0 || len(replay.ToRemote) != 0 || len(replay.Conflicts) != 0 {
		t.Fatalf("base changed: %#v", replay)
	}
}

func TestConcurrentPlansAreStable(t *testing.T) {
	base := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
	}
	local := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
		3: {Parent: 1, Name: "g", Hash: "h3"},
	}
	remote := Snapshot{}
	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	var wait sync.WaitGroup
	results := make([]PlanResult, goroutines)
	errs := make([]error, goroutines)
	wait.Add(goroutines)
	for index := 0; index < goroutines; index++ {
		go func(index int) {
			defer wait.Done()
			results[index], errs[index] = planner.Plan(local, remote)
		}(index)
	}
	wait.Wait()

	for index := 1; index < goroutines; index++ {
		if errs[index] != nil {
			t.Fatal(errs[index])
		}
		if !reflect.DeepEqual(results[index], results[0]) {
			t.Fatalf("result %d differs from result 0", index)
		}
	}
}

func TestDuplicateCreateResolutionExample(t *testing.T) {
	base := Snapshot{}
	local := Snapshot{5: {Parent: 0, Name: "a", Hash: "h5"}}
	remote := Snapshot{4: {Parent: 0, Name: "a", Hash: "h4"}}

	planner, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := planner.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}

	wantLocal := []Action{
		{Op: Create, ID: 4, Parent: 0, Name: "a", Hash: "h4"},
		{Op: SetLoc, ID: 5, Parent: 0, Name: "a.c5"},
	}
	wantRemote := []Action{
		{Op: Create, ID: 5, Parent: 0, Name: "a.c5", Hash: "h5"},
	}
	if !reflect.DeepEqual(got.ToLocal, wantLocal) {
		t.Fatalf("ToLocal = %#v, want %#v", got.ToLocal, wantLocal)
	}
	if !reflect.DeepEqual(got.ToRemote, wantRemote) {
		t.Fatalf("ToRemote = %#v, want %#v", got.ToRemote, wantRemote)
	}
}
