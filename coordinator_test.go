package coordinator

import (
	"errors"
	"testing"
)

func TestSpecExample(t *testing.T) {
	c, err := New(2, 4, 3)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	terms := []int{1, 1, 1, 2, 2, 2, 3, 3}
	for i, term := range terms {
		idx, err := c.Append(term)
		if err != nil || idx != i+1 {
			t.Fatalf("Append(%d) = (%d, %v), want %d, nil", term, idx, err, i+1)
		}
	}
	if err := c.Commit(8); err != nil {
		t.Fatalf("Commit(8) error = %v", err)
	}

	took, err := c.Apply(3)
	if err != nil || took {
		t.Fatalf("Apply(3) = (%v, %v), want false, nil", took, err)
	}
	took, err = c.Apply(4)
	if err != nil || !took {
		t.Fatalf("Apply(4) = (%v, %v), want true, nil", took, err)
	}
	if got := c.snapIndex; got != 4 || c.snapTerm != 2 || c.baseIndex != 2 || c.baseTerm != 1 {
		t.Fatalf("state = snap(%d,%d) base(%d,%d), want snap(4,2) base(2,1)", got, c.snapTerm, c.baseIndex, c.baseTerm)
	}

	if err := c.AddPeer("p"); err != nil {
		t.Fatalf("AddPeer(p) error = %v", err)
	}
	if err := c.Retreat("p", 3); err != nil {
		t.Fatalf("Retreat(p, 3) error = %v", err)
	}
	assertPlan(t, c, "p", Plan{Kind: AppendEntries, PrevIndex: 2, PrevTerm: 1, From: 3, To: 8})

	if err := c.Retreat("p", 2); err != nil {
		t.Fatalf("Retreat(p, 2) error = %v", err)
	}
	assertPlan(t, c, "p", Plan{Kind: NeedSnapshot, Snapshot: 4, SnapTerm: 2})

	s, st, err := c.StartSnapshot("p")
	if err != nil || s != 4 || st != 2 {
		t.Fatalf("StartSnapshot(p) = (%d,%d,%v), want 4,2,nil", s, st, err)
	}
	assertPlan(t, c, "p", Plan{Kind: Installing, Snapshot: 4, SnapTerm: 2})

	took, err = c.Apply(8)
	if err != nil || !took {
		t.Fatalf("Apply(8) = (%v, %v), want true, nil", took, err)
	}
	if c.snapIndex != 8 || c.snapTerm != 3 || c.baseIndex != 4 || c.baseTerm != 2 {
		t.Fatalf("state = snap(%d,%d) base(%d,%d), want snap(8,3) base(4,2)", c.snapIndex, c.snapTerm, c.baseIndex, c.baseTerm)
	}

	if err := c.FinishSnapshot("p"); err != nil {
		t.Fatalf("FinishSnapshot(p) error = %v", err)
	}
	if c.peers["p"].match != 4 || c.peers["p"].next != 5 || c.baseIndex != 6 || c.baseTerm != 2 {
		t.Fatalf("after finish peer=(%d,%d) base=(%d,%d), want (4,5) base=(6,2)", c.peers["p"].match, c.peers["p"].next, c.baseIndex, c.baseTerm)
	}
	assertPlan(t, c, "p", Plan{Kind: NeedSnapshot, Snapshot: 8, SnapTerm: 3})
}

func assertPlan(t *testing.T, c *Coordinator, id string, want Plan) {
	t.Helper()
	got, err := c.Plan(id)
	if err != nil {
		t.Fatalf("Plan(%q) error = %v", id, err)
	}
	if got != want {
		t.Fatalf("Plan(%q) = %+v, want %+v", id, got, want)
	}
}

func TestNewAndAppendValidation(t *testing.T) {
	for _, args := range [][3]int{{-1, 1, 0}, {0, 0, 0}, {0, 1, -1}} {
		if _, err := New(args[0], args[1], args[2]); !errors.Is(err, ErrParam) {
			t.Fatalf("New(%v) error = %v, want ErrParam", args, err)
		}
	}

	c, _ := New(0, 1, 0)
	if _, err := c.Append(0); !errors.Is(err, ErrParam) {
		t.Fatalf("Append(0) error = %v, want ErrParam", err)
	}
	if _, err := c.Append(1); err != nil {
		t.Fatalf("Append(1) error = %v", err)
	}
	if _, err := c.Append(0); !errors.Is(err, ErrParam) {
		t.Fatalf("Append(0) after append error = %v, want ErrParam", err)
	}
	if _, err := c.Append(2); err != nil {
		t.Fatalf("Append(2) error = %v", err)
	}
	if _, err := c.Append(1); !errors.Is(err, ErrTerm) {
		t.Fatalf("Append(1) error = %v, want ErrTerm", err)
	}
}
