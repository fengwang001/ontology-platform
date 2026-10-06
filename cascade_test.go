package cascade

import (
	"errors"
	"sync"
	"testing"
)

func mustCreate(t *testing.T, c *Controller, input CreateObjectInput) {
	t.Helper()
	t.Logf("input=Create(%+v) basis=validate ids owners finalizers and graph invariants", input)
	if err := c.Create(input); err != nil {
		t.Fatalf("actual_error=%v", err)
	}
	t.Log("actual_output=ok basis=object accepted without mutation on validation failure")
}

func mustDelete(t *testing.T, c *Controller, id string, policy Policy, at int64) OperationResult {
	t.Helper()
	t.Logf("input=Delete(id=%q policy=%v at=%d) basis=mark then synchronously converge affected closure", id, policy, at)
	result, err := c.Delete(id, policy, at)
	if err != nil {
		t.Fatalf("actual_error=%v", err)
	}
	t.Logf("actual_output=%+v basis=removed list is fully effective on return", result)
	return result
}

func assertStates(t *testing.T, c *Controller, want map[string]ObjectState) {
	t.Helper()
	got := c.Snapshot()
	if len(got) != len(want) {
		t.Fatalf("snapshot len=%d want=%d actual=%v want=%v", len(got), len(want), got, want)
	}
	for id, wantState := range want {
		gotState, exists := got[id]
		if !exists {
			t.Fatalf("missing object %q", id)
		}
		if !equalState(gotState, wantState) {
			t.Fatalf("state %q=%+v want=%+v", id, gotState, wantState)
		}
	}
}

func equalState(got, want ObjectState) bool {
	return got.ID == want.ID && got.Deleting == want.Deleting && got.Policy == want.Policy &&
		got.DeleteAt == want.DeleteAt && equalOwnerRefs(got.Owners, want.Owners) &&
		equalStrings(got.Finalizers, want.Finalizers)
}

func equalOwnerRefs(got, want []OwnerRef) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestDeletionPolicies(t *testing.T) {
	t.Run("background cascades when all owners disappear", func(t *testing.T) {
		c := NewController()
		mustCreate(t, c, CreateObjectInput{ID: "p"})
		mustCreate(t, c, CreateObjectInput{ID: "b", Owners: []OwnerRef{{OwnerID: "p"}}})
		result := mustDelete(t, c, "p", Background, 1)
		if len(result.Removed) != 2 {
			t.Fatalf("removed=%v want p,b", result.Removed)
		}
		assertStates(t, c, map[string]ObjectState{})
	})

	t.Run("orphan preserves dependent with no owners", func(t *testing.T) {
		c := NewController()
		mustCreate(t, c, CreateObjectInput{ID: "p"})
		mustCreate(t, c, CreateObjectInput{ID: "o", Owners: []OwnerRef{{OwnerID: "p"}}})
		result := mustDelete(t, c, "p", Orphan, 2)
		if len(result.Removed) != 1 || result.Removed[0] != "p" {
			t.Fatalf("removed=%v want [p]", result.Removed)
		}
		assertStates(t, c, map[string]ObjectState{
			"o": {ID: "o", Owners: nil, Finalizers: nil},
		})
	})

	t.Run("foreground propagates through dependency closure", func(t *testing.T) {
		c := NewController()
		mustCreate(t, c, CreateObjectInput{ID: "p"})
		mustCreate(t, c, CreateObjectInput{ID: "d", Owners: []OwnerRef{{OwnerID: "p"}}})
		result := mustDelete(t, c, "p", Foreground, 3)
		if len(result.Removed) != 2 {
			t.Fatalf("removed=%v want d,p closure", result.Removed)
		}
		assertStates(t, c, map[string]ObjectState{})
	})
}

func TestMultipleOwnersAndForegroundBlocking(t *testing.T) {
	c := NewController()
	mustCreate(t, c, CreateObjectInput{ID: "a"})
	mustCreate(t, c, CreateObjectInput{ID: "b"})
	mustCreate(t, c, CreateObjectInput{ID: "shared", Owners: []OwnerRef{{OwnerID: "a"}, {OwnerID: "b"}}})
	result := mustDelete(t, c, "a", Foreground, 1)
	if len(result.Removed) != 1 {
		t.Fatalf("removed=%v want only a", result.Removed)
	}
	assertStates(t, c, map[string]ObjectState{
		"b":      {ID: "b"},
		"shared": {ID: "shared", Owners: []OwnerRef{{OwnerID: "b"}}},
	})

	mustDelete(t, c, "b", Background, 2)
	assertStates(t, c, map[string]ObjectState{})

	c = NewController()
	mustCreate(t, c, CreateObjectInput{ID: "p"})
	mustCreate(t, c, CreateObjectInput{ID: "blocked", Owners: []OwnerRef{{OwnerID: "p", BlockDeletion: true}}, Finalizers: []string{"guard"}})
	mustCreate(t, c, CreateObjectInput{ID: "leaf", Owners: []OwnerRef{{OwnerID: "blocked"}}})
	result = mustDelete(t, c, "p", Foreground, 3)
	if len(result.Removed) != 1 || result.Removed[0] != "leaf" {
		t.Fatalf("removed=%v want leaf only, blocked and p remain deleting", result.Removed)
	}
	assertStates(t, c, map[string]ObjectState{
		"p":       {ID: "p", Deleting: true, Policy: Foreground, DeleteAt: 3},
		"blocked": {ID: "blocked", Deleting: true, Policy: Foreground, DeleteAt: 3, Owners: []OwnerRef{{OwnerID: "p", BlockDeletion: true}}, Finalizers: []string{"guard"}},
	})
}

func TestFinalizersUnblockFullCascade(t *testing.T) {
	c := NewController()
	mustCreate(t, c, CreateObjectInput{ID: "p", Finalizers: []string{"guard"}})
	mustCreate(t, c, CreateObjectInput{ID: "d", Owners: []OwnerRef{{OwnerID: "p"}}})
	mustCreate(t, c, CreateObjectInput{ID: "x"})
	mustDelete(t, c, "p", Background, 1)
	assertStates(t, c, map[string]ObjectState{
		"p": {ID: "p", Deleting: true, Policy: Background, DeleteAt: 1, Finalizers: []string{"guard"}},
		"d": {ID: "d", Owners: []OwnerRef{{OwnerID: "p"}}},
		"x": {ID: "x"},
	})

	t.Log("input=AddFinalizer(p,f1) basis=deleting objects reject new finalizers")
	if err := c.AddFinalizer("p", "f1"); !isKind(err, Conflict) {
		t.Fatalf("actual_error=%v want conflict", err)
	}
	t.Log("actual_output=conflict basis=state conflict outranks nothing because object exists")

	c = NewController()
	mustCreate(t, c, CreateObjectInput{ID: "p", Finalizers: []string{"f1"}})
	mustCreate(t, c, CreateObjectInput{ID: "d", Owners: []OwnerRef{{OwnerID: "p"}}, Finalizers: []string{"f2"}})
	mustDelete(t, c, "p", Background, 2)
	t.Logf("input=RemoveFinalizer(p,f1 at=3) basis=one edge remains blocked by d finalizer")
	result, err := c.RemoveFinalizer("p", "f1", 3)
	if err != nil {
		t.Fatalf("actual_error=%v", err)
	}
	t.Logf("actual_output=%+v basis=p removed but d still deleting", result)
	result, err = c.RemoveFinalizer("d", "f2", 4)
	if err != nil {
		t.Fatalf("actual_error=%v", err)
	}
	t.Logf("actual_output=%+v basis=removing final finalizer converges entire cascade", result)
	if len(result.Removed) != 1 || result.Removed[0] != "d" {
		t.Fatalf("removed=%v want d", result.Removed)
	}
	assertStates(t, c, map[string]ObjectState{})
}

func TestRepeatedDeleteUpgradesOnlyBackgroundToForeground(t *testing.T) {
	c := NewController()
	mustCreate(t, c, CreateObjectInput{ID: "p", Finalizers: []string{"guard"}})
	mustCreate(t, c, CreateObjectInput{ID: "d", Owners: []OwnerRef{{OwnerID: "p", BlockDeletion: true}}})
	first := mustDelete(t, c, "p", Background, 1)
	if len(first.Removed) != 0 {
		t.Fatalf("removed=%v want none", first.Removed)
	}
	second := mustDelete(t, c, "p", Orphan, 2)
	if !second.NoChange || second.Upgraded {
		t.Fatalf("second=%+v want no-change", second)
	}
	third := mustDelete(t, c, "p", Foreground, 3)
	if !third.Upgraded || len(third.Removed) != 1 || third.Removed[0] != "d" {
		t.Fatalf("third=%+v want upgrade and d removal while p finalizer remains", third)
	}
	result, err := c.RemoveFinalizer("p", "guard", 4)
	if err != nil {
		t.Fatalf("remove finalizer: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "p" {
		t.Fatalf("result=%+v want p removal", result)
	}
}

func TestGraphAndErrorPriority(t *testing.T) {
	c := NewController()
	mustCreate(t, c, CreateObjectInput{ID: "a"})

	_, err := c.Delete("", Background, 1)
	if !isKind(err, InvalidArgument) {
		t.Fatalf("err=%v want invalid", err)
	}
	_, err = c.Delete("missing", Background, 1)
	if !isKind(err, ObjectNotFound) {
		t.Fatalf("err=%v want not found", err)
	}

	err = c.Create(CreateObjectInput{ID: "self", Owners: []OwnerRef{{OwnerID: "self"}}})
	if !isKind(err, CycleDetected) {
		t.Fatalf("err=%v want cycle", err)
	}
	err = c.Create(CreateObjectInput{ID: "b", Owners: []OwnerRef{{OwnerID: "a"}, {OwnerID: "a", BlockDeletion: true}}})
	if !isKind(err, InvalidArgument) {
		t.Fatalf("err=%v want duplicate invalid", err)
	}
	err = c.Create(CreateObjectInput{ID: "b", Owners: []OwnerRef{{OwnerID: "missing"}, {OwnerID: "b"}}})
	if !isKind(err, CycleDetected) {
		t.Fatalf("err=%v want cycle before missing", err)
	}
	err = c.Create(CreateObjectInput{ID: "b", Owners: []OwnerRef{{OwnerID: "missing"}}})
	if !isKind(err, OwnerMissing) {
		t.Fatalf("err=%v want missing owner", err)
	}

	mustCreate(t, c, CreateObjectInput{ID: "d", Owners: []OwnerRef{{OwnerID: "a"}}})
	if err := c.AddFinalizer("a", "guard"); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	mustDelete(t, c, "a", Orphan, 2)
	_, err = c.ReplaceOwners("a", nil, 3)
	if !isKind(err, Conflict) {
		t.Fatalf("err=%v want conflict", err)
	}
	_, err = c.RemoveFinalizer("missing", "x", 3)
	if !isKind(err, ObjectNotFound) {
		t.Fatalf("err=%v want not found before invalid finalizer semantics", err)
	}

	c2 := NewController()
	mustCreate(t, c2, CreateObjectInput{ID: "x"})
	mustCreate(t, c2, CreateObjectInput{ID: "y", Owners: []OwnerRef{{OwnerID: "x"}}})
	_, err = c2.ReplaceOwners("x", []OwnerRef{{OwnerID: "y"}}, 4)
	if !isKind(err, CycleDetected) {
		t.Fatalf("err=%v want replacement cycle", err)
	}
}

func TestConcurrentOperationsSerialize(t *testing.T) {
	c := NewController()
	for _, id := range []string{"p", "d1", "d2", "d3"} {
		input := CreateObjectInput{ID: id}
		if id != "p" {
			input.Owners = []OwnerRef{{OwnerID: "p"}}
		}
		mustCreate(t, c, input)
	}

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := c.Delete("p", Background, int64(10+i))
			if err != nil && !isKind(err, ObjectNotFound) {
				t.Errorf("delete: %v", err)
			}
		}(i)
	}
	wg.Wait()

	states := c.Snapshot()
	if len(states) != 0 {
		t.Fatalf("states=%v want empty; each cascade has one serializable effect", states)
	}
}

func isKind(err error, kind ErrorKind) bool {
	var controllerErr ControllerError
	if !errors.As(err, &controllerErr) {
		return false
	}
	return controllerErr.Kind == kind
}
