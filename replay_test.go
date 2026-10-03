package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func op(ts int64, rep string, node, parent int64, name string) Op {
	return Op{TS: ts, Rep: rep, Node: node, Parent: parent, Name: name}
}

func key(ts int64, rep string) Key {
	return Key{TS: ts, Rep: rep}
}

func applyAll(t *testing.T, r *Replayer, ops []Op) {
	t.Helper()
	for _, operation := range ops {
		if _, err := r.Apply(operation); err != nil {
			t.Fatalf("Apply(%+v): %v", operation, err)
		}
	}
}

func assertParent(t *testing.T, r *Replayer, node, parent int64, name string) {
	t.Helper()
	gotParent, gotName, exists := r.Parent(node)
	if !exists || gotParent != parent || gotName != name {
		t.Fatalf("Parent(%d) = (%d,%q,%t), want (%d,%q,true)", node, gotParent, gotName, exists, parent, name)
	}
}

func TestExampleBothArrivalOrders(t *testing.T) {
	o1 := op(1, "A", 2, 0, "x")
	o2 := op(2, "A", 3, 0, "y")
	o3 := op(3, "A", 2, 3, "x")
	o4 := op(3, "B", 3, 2, "y")

	first := New([]string{"A", "B"})
	applyAll(t, first, []Op{o1, o2, o3, o4})
	assertParent(t, first, 3, 0, "y")
	assertParent(t, first, 2, 3, "x")
	if first.Log()[3].Effective {
		t.Fatalf("o4 effective = true, want false")
	}

	second := New([]string{"A", "B"})
	applyAll(t, second, []Op{o1, o2, o4})
	result, err := second.Apply(o3)
	if err != nil {
		t.Fatalf("Apply(o3): %v", err)
	}
	if !result.Effective {
		t.Fatalf("o3 effective = false, want true")
	}
	wantChanged := []Key{key(3, "B")}
	if !reflect.DeepEqual(result.Changed, wantChanged) {
		t.Fatalf("Changed = %+v, want %+v", result.Changed, wantChanged)
	}
	if second.redone != 1 {
		t.Fatalf("redone = %d, want 1", second.redone)
	}
	assertParent(t, second, 3, 0, "y")
	assertParent(t, second, 2, 3, "x")
	if !reflect.DeepEqual(first.Log(), second.Log()) {
		t.Fatalf("logs differ: %+v != %+v", first.Log(), second.Log())
	}
}

func TestParentEqualsNodeIsSkipped(t *testing.T) {
	r := New([]string{"R"})
	setup := op(1, "R", 2, 0, "n")
	self := op(2, "R", 2, 2, "loop")
	if _, err := r.Apply(setup); err != nil {
		t.Fatal(err)
	}
	result, err := r.Apply(self)
	if err != nil {
		t.Fatal(err)
	}
	if result.Effective {
		t.Fatalf("self-parent operation was effective")
	}
	assertParent(t, r, 2, 0, "n")
	if r.Log()[1].Effective {
		t.Fatalf("self-parent log entry was effective")
	}
}

func TestLaterSmallerKeyParentMakesSkippedChildEffective(t *testing.T) {
	r := New([]string{"A", "B"})
	child := op(2, "B", 3, 2, "child")
	parent := op(1, "A", 2, 0, "parent")

	result, err := r.Apply(child)
	if err != nil {
		t.Fatal(err)
	}
	if result.Effective {
		t.Fatalf("child effective before parent exists")
	}
	result, err = r.Apply(parent)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Effective {
		t.Fatalf("parent not effective")
	}
	wantChanged := []Key{key(2, "B")}
	if !reflect.DeepEqual(result.Changed, wantChanged) {
		t.Fatalf("Changed = %+v, want %+v", result.Changed, wantChanged)
	}
	if r.redone != 1 {
		t.Fatalf("redone = %d, want 1", r.redone)
	}
	assertParent(t, r, 2, 0, "parent")
	assertParent(t, r, 3, 2, "child")
}

func TestTrashMoveAndRestore(t *testing.T) {
	makeNode := op(1, "R", 2, 0, "n")
	toTrash := op(2, "R", 2, 1, "n")
	restore := op(3, "R", 2, 0, "restored")

	restored := New([]string{"R"})
	applyAll(t, restored, []Op{makeNode, toTrash, restore})
	if restored.InTrash(2) {
		t.Fatalf("node is still in trash after restore")
	}

	trashed := New([]string{"R"})
	applyAll(t, trashed, []Op{makeNode, toTrash})
	if !trashed.InTrash(2) {
		t.Fatalf("node under trash was not detected")
	}
}

func TestConcurrentMovesSameNodeLargerKeyWins(t *testing.T) {
	fromA := op(5, "A", 2, 0, "a")
	fromB := op(5, "B", 2, 1, "b")

	first := New([]string{"A", "B"})
	applyAll(t, first, []Op{fromA, fromB})
	assertParent(t, first, 2, 1, "b")

	second := New([]string{"A", "B"})
	applyAll(t, second, []Op{fromB, fromA})
	assertParent(t, second, 2, 1, "b")
}

func TestStableRejectsAtWatermarkAndFoldsPrefix(t *testing.T) {
	r := New([]string{"A", "B"})
	first := op(1, "A", 2, 0, "one")
	second := op(2, "B", 3, 2, "two")
	third := op(3, "A", 2, 3, "cycle")
	applyAll(t, r, []Op{first, second, third})

	folded, err := r.Ack("A", 2)
	if err != nil {
		t.Fatal(err)
	}
	if folded != 0 {
		t.Fatalf("folded = %d, want 0", folded)
	}
	folded, err = r.Ack("B", 2)
	if err != nil {
		t.Fatal(err)
	}
	if folded != 2 {
		t.Fatalf("folded = %d, want 2", folded)
	}
	if len(r.Log()) != 1 {
		t.Fatalf("log length = %d, want 1", len(r.Log()))
	}
	assertParent(t, r, 3, 2, "two")

	if _, err := r.Apply(op(2, "A", 4, 0, "late")); !errors.Is(err, ErrStale) {
		t.Fatalf("ts == stable error = %v, want ErrStale", err)
	}
	accepted := op(3, "B", 4, 0, "new")
	result, err := r.Apply(accepted)
	if err != nil {
		t.Fatalf("ts == stable+1: %v", err)
	}
	if !result.Effective {
		t.Fatalf("operation above stable was not effective")
	}
	if len(result.Changed) != 0 {
		t.Fatalf("Changed = %+v, must not contain folded operations", result.Changed)
	}
}

func TestAckPriorityAndRegress(t *testing.T) {
	r := New([]string{"A", "B"})
	if _, err := r.Ack("unknown", 1); !errors.Is(err, ErrUnknownRep) {
		t.Fatalf("unknown ack error = %v, want ErrUnknownRep", err)
	}
	if _, err := r.Ack("A", 2); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ack("A", 1); !errors.Is(err, ErrAckRegress) {
		t.Fatalf("regress error = %v, want ErrAckRegress", err)
	}
}

func TestApplyValidationPriority(t *testing.T) {
	r := New([]string{"A"})
	if _, err := r.Apply(op(0, "unknown", 2, 0, "")); err == nil {
		t.Fatalf("invalid operation was accepted")
	}
	if _, err := r.Apply(op(1, "unknown", 2, 0, "ok")); !errors.Is(err, ErrUnknownRep) {
		t.Fatalf("unknown replica error = %v", err)
	}
	r.acks["A"] = 5
	r.stable = 5
	if _, err := r.Apply(op(5, "A", 2, 0, "ok")); !errors.Is(err, ErrStale) {
		t.Fatalf("stale error = %v", err)
	}
	existing := op(6, "A", 2, 0, "ok")
	if _, err := r.Apply(existing); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(existing); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	if _, err := r.Apply(op(7, "A", 2, 0, "bad/name")); err == nil {
		t.Fatalf("slash name was accepted")
	}
}

func TestConcurrentCalls(t *testing.T) {
	r := New([]string{"A", "B"})
	ops := []Op{
		op(1, "A", 2, 0, "a"),
		op(2, "B", 3, 2, "b"),
		op(3, "A", 2, 1, "a2"),
		op(4, "B", 4, 1, "c"),
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()
			for j := range ops {
				_, _ = r.Apply(ops[(j+offset)%len(ops)])
				_ = r.InTrash(2)
				_ = r.Log()
			}
		}(i)
	}
	wg.Wait()
	if len(r.Log()) != len(ops) {
		t.Fatalf("log length = %d, want %d", len(r.Log()), len(ops))
	}
}
