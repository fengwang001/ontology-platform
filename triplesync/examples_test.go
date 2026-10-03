package triplesync_test

import (
	"testing"

	"ontology/triplesync"
)

func TestSpecExampleVeto(t *testing.T) {
	base := triplesync.Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
	}
	local := triplesync.Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
		3: {Parent: 1, Name: "g", Hash: "h3"},
	}
	remote := triplesync.Snapshot{}

	p, err := triplesync.NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{
		{Kind: triplesync.ActionDelete, ID: 2},
	})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 1, Parent: 0, Name: "d", Dir: true},
		{Kind: triplesync.ActionCreate, ID: 3, Parent: 1, Name: "g", Hash: "h3"},
	})
	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", plan.Conflicts)
	}
}

func TestSpecExampleNameCollision(t *testing.T) {
	base := triplesync.Snapshot{}
	local := triplesync.Snapshot{
		5: {Parent: 0, Name: "a", Hash: "h5"},
	}
	remote := triplesync.Snapshot{
		4: {Parent: 0, Name: "a", Hash: "h4"},
	}

	p, err := triplesync.NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := p.Plan(local, remote)
	if err != nil {
		t.Fatal(err)
	}
	assertActions(t, plan.ToLocal, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 4, Parent: 0, Name: "a", Hash: "h4"},
		{Kind: triplesync.ActionSetLoc, ID: 5, Parent: 0, Name: "a.c5"},
	})
	assertActions(t, plan.ToRemote, []triplesync.Action{
		{Kind: triplesync.ActionCreate, ID: 5, Parent: 0, Name: "a.c5", Hash: "h5"},
	})
}

func assertActions(t *testing.T, got, want []triplesync.Action) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d actions %v, want %d %v", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("action %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
