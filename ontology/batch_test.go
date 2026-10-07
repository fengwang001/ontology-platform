package ontology_test

import (
	"bytes"
	"strings"
	"testing"

	"ontology/ontology"
)

func testConfig() ontology.Config {
	return ontology.Config{
		ObjectTypes: map[string]ontology.ObjectType{
			"Person": {Name: "Person"},
			"Org":    {Name: "Org"},
		},
		LinkTypes: map[string]ontology.LinkType{
			"member": {
				Name:       "member",
				SourceType: "Person",
				TargetType: "Org",
				MaxSource:  2,
				MaxTarget:  -1,
			},
		},
	}
}

func newPopulatedStore(t *testing.T) *ontology.Store {
	t.Helper()
	s := ontology.New(testConfig())
	mustCreate(t, s, "alice", "Person")
	mustCreate(t, s, "bob", "Person")
	mustCreate(t, s, "acme", "Org")
	return s
}

func mustCreate(t *testing.T, s *ontology.Store, id, typ string) {
	t.Helper()
	if err := s.CreateInstance(ontology.ID(id), typ); err != nil {
		t.Fatalf("create %s: %v", id, err)
	}
}

func TestCommitHappyPath(t *testing.T) {
	s := newPopulatedStore(t)
	res := s.Commit(ontology.Batch{
		ID: "b1",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 1},
			{Instance: "acme", ExpectedVersion: 1},
		},
		Ops: []ontology.Op{
			{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "name", Value: "Alice"},
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "acme"},
		},
	})
	if res.Status != ontology.StatusCommitted {
		t.Fatalf("status = %s, want committed: %+v", res.Status, res)
	}
	if got := res.Versions["alice"].To; got != 2 {
		t.Fatalf("alice version = %d, want 2", got)
	}
	if got := res.Versions["acme"].To; got != 2 {
		t.Fatalf("acme version = %d, want 2", got)
	}
	snap, _ := s.Get("alice")
	if snap.Version != 2 || snap.Attrs["name"] != "Alice" {
		t.Fatalf("post state wrong: %+v", snap)
	}
}

func TestDuplicatePreconditionCheckedFirst(t *testing.T) {
	s := newPopulatedStore(t)
	res := s.Commit(ontology.Batch{
		ID: "b-dup",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 1},
			{Instance: "alice", ExpectedVersion: 99},
		},
	})
	if res.Status != ontology.StatusDuplicatePrecondition {
		t.Fatalf("status = %s, want duplicate_precondition", res.Status)
	}
	if res.Duplicate != "alice" {
		t.Fatalf("duplicate = %q, want alice", res.Duplicate)
	}
	snap, _ := s.Get("alice")
	if snap.Version != 1 {
		t.Fatalf("version changed to %d, want 1", snap.Version)
	}
}

func TestVersionMismatchRejectsWholeBatch(t *testing.T) {
	s := newPopulatedStore(t)
	// Advance alice to version 2 on her own.
	r1 := s.Commit(ontology.Batch{
		ID:            "setup",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 1}},
		Ops:           []ontology.Op{{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "x", Value: 1}},
	})
	if r1.Status != ontology.StatusCommitted {
		t.Fatalf("setup: %+v", r1)
	}

	res := s.Commit(ontology.Batch{
		ID: "stale",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 1}, // stale
			{Instance: "bob", ExpectedVersion: 1},   // fine
		},
		Ops: []ontology.Op{
			{Kind: ontology.OpSetAttr, Instance: "bob", Attr: "name", Value: "Bob"},
		},
	})
	if res.Status != ontology.StatusVersionMismatch {
		t.Fatalf("status = %s, want version_mismatch", res.Status)
	}
	if len(res.Mismatches) != 1 || res.Mismatches[0].Instance != "alice" || res.Mismatches[0].Actual != 2 {
		t.Fatalf("mismatches = %+v", res.Mismatches)
	}
	if res.Observed["alice"] != 2 || res.Observed["bob"] != 1 {
		t.Fatalf("observed evidence wrong: %+v", res.Observed)
	}
	bob, _ := s.Get("bob")
	if _, touched := bob.Attrs["name"]; touched {
		t.Fatal("rejected batch mutated bob")
	}
	if bob.Version != 1 {
		t.Fatalf("bob version = %d, want 1", bob.Version)
	}
}

func TestCardinalityExactlyAtBoundary(t *testing.T) {
	s := newPopulatedStore(t)
	mustCreate(t, s, "carol", "Person")
	mustCreate(t, s, "initech", "Org")

	// member MaxSource = 2: two memberships commit, the third violates.
	fill := ontology.Batch{
		ID:            "fill",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 1}},
		Ops: []ontology.Op{
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "acme"},
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "initech"},
		},
	}
	if r := s.Commit(fill); r.Status != ontology.StatusCommitted {
		t.Fatalf("fill = %+v", r)
	}

	over := ontology.Batch{
		ID:            "over",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 2}},
		Ops: []ontology.Op{
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "acme"},
		},
	}
	// The third membership is a different org, so degree becomes 3.
	mustCreate(t, s, "umbrella", "Org")
	over.Ops[0].Other = "umbrella"
	r := s.Commit(over)
	if r.Status != ontology.StatusCardinalityViolation {
		t.Fatalf("status = %s, want cardinality_violation; %+v", r.Status, r)
	}
	if len(r.Cardinality) != 1 || r.Cardinality[0].Count != 3 || r.Cardinality[0].Max != 2 {
		t.Fatalf("cardinality evidence = %+v", r.Cardinality)
	}
	alice, _ := s.Get("alice")
	if alice.Version != 2 {
		t.Fatalf("alice version = %d, want unchanged 2", alice.Version)
	}
}

func TestIndependentVersionProgressions(t *testing.T) {
	s := newPopulatedStore(t)
	for i := 0; i < 3; i++ {
		r := s.Commit(ontology.Batch{
			ID:            "alice-only",
			Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: uint64(1 + i)}},
			Ops:           []ontology.Op{{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "n", Value: i}},
		})
		if r.Status != ontology.StatusCommitted {
			t.Fatalf("iter %d: %+v", i, r)
		}
	}
	alice, _ := s.Get("alice")
	bob, _ := s.Get("bob")
	if alice.Version != 4 {
		t.Fatalf("alice = %d, want 4", alice.Version)
	}
	if bob.Version != 1 {
		t.Fatalf("bob = %d, want 1 (joint batches must not unify versions)", bob.Version)
	}

	// A joint batch still advances each endpoint by exactly one own step.
	r := s.Commit(ontology.Batch{
		ID: "joint",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 4},
			{Instance: "bob", ExpectedVersion: 1},
		},
		Ops: []ontology.Op{{Kind: ontology.OpSetAttr, Instance: "bob", Attr: "y", Value: 2}},
	})
	if r.Status != ontology.StatusCommitted {
		t.Fatalf("joint: %+v", r)
	}
	alice, _ = s.Get("alice")
	bob, _ = s.Get("bob")
	if alice.Version != 5 || bob.Version != 2 {
		t.Fatalf("post joint versions alice=%d bob=%d, want 5/2", alice.Version, bob.Version)
	}
}

func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	s := newPopulatedStore(t)
	before := s.SnapshotAll([]ontology.ID{"alice", "bob", "acme"})
	r := s.Commit(ontology.Batch{
		ID: "nope",
		Preconditions: []ontology.Precondition{
			{Instance: "alice", ExpectedVersion: 1},
			{Instance: "bob", ExpectedVersion: 42},
		},
		Ops: []ontology.Op{
			{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "name", Value: "should-not-land"},
			{Kind: ontology.OpAddLink, Instance: "alice", LinkType: "member", Other: "acme"},
		},
	})
	if r.Status != ontology.StatusVersionMismatch {
		t.Fatalf("status = %s", r.Status)
	}
	after := s.SnapshotAll([]ontology.ID{"alice", "bob", "acme"})
	for id, b := range before {
		a := after[id]
		if a.Version != b.Version || len(a.Attrs) != len(b.Attrs) {
			t.Fatalf("instance %s changed: before=%+v after=%+v", id, b, a)
		}
	}
}

func TestJournalRoundTrip(t *testing.T) {
	s := newPopulatedStore(t)
	s.Commit(ontology.Batch{
		ID:            "ok",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 1}},
		Ops:           []ontology.Op{{Kind: ontology.OpSetAttr, Instance: "alice", Attr: "a", Value: 1}},
	})
	s.Commit(ontology.Batch{
		ID:            "bad",
		Preconditions: []ontology.Precondition{{Instance: "alice", ExpectedVersion: 1}},
	})

	var buf bytes.Buffer
	if _, err := s.Journal().WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"status":"committed"`) ||
		!strings.Contains(buf.String(), `"status":"version_mismatch"`) ||
		!strings.Contains(buf.String(), `"kind":"create"`) {
		t.Fatalf("journal missing evidence: %s", buf.String())
	}

	report, err := ontology.ReplayJournal(testConfig(), bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if report.Batches != 2 || report.Committed != 1 || report.Rejected != 1 {
		t.Fatalf("report = %+v", report)
	}
	if report.Versions["alice"] != 2 {
		t.Fatalf("replayed alice = %d, want 2", report.Versions["alice"])
	}
}
