package binding

import (
	"reflect"
	"sort"
	"testing"
)

func TestOwnersAndCounts(t *testing.T) {
	tb := NewTable(3)
	tb.AddBinding("f1", 100, 7)
	tb.AddOwner("f1", 8)
	tb.AddBinding("f2", 101, 7)
	if tb.Count(7) != 2 || tb.Count(8) != 1 {
		t.Fatalf("counts = %d/%d, want 2/1", tb.Count(7), tb.Count(8))
	}
	if _, emptied := tb.RemoveOwner("f1", 8); emptied {
		t.Fatal("binding emptied while 7 still owns it")
	}
	label, emptied := tb.RemoveOwner("f1", 7)
	if !emptied || label != 100 {
		t.Fatalf("RemoveOwner = %d,%v, want 100,true", label, emptied)
	}
	if _, ok := tb.Get("f1"); ok {
		t.Fatal("binding should be gone")
	}
	if tb.Count(7) != 1 {
		t.Fatalf("count = %d, want 1", tb.Count(7))
	}
	if _, emptied := tb.RemoveOwner("f2", 9); emptied {
		t.Fatal("removing a non-owner must not empty the binding")
	}
}

func TestMarkStaleKeepsOldDeadline(t *testing.T) {
	tb := NewTable(5)
	tb.AddBinding("f1", 100, 7)
	tb.AddBinding("f2", 101, 7)
	changed := tb.MarkStale(7, 70)
	sort.Strings(changed)
	if !reflect.DeepEqual(changed, []string{"f1", "f2"}) {
		t.Fatalf("changed = %v", changed)
	}
	tb.Refresh("f1", 7)
	// Second down: refreshed f1 gets the new deadline, stale f2 keeps its own.
	changed = tb.MarkStale(7, 90)
	if !reflect.DeepEqual(changed, []string{"f1"}) {
		t.Fatalf("changed = %v, want [f1]", changed)
	}
	if o := tb.Owner("f1", 7); !o.Stale || o.Deadline != 90 {
		t.Fatalf("f1 owner = %+v, want stale until 90", o)
	}
	if o := tb.Owner("f2", 7); !o.Stale || o.Deadline != 70 {
		t.Fatalf("f2 owner = %+v, want stale until 70", o)
	}
	stale := tb.StaleFecs(7)
	sort.Strings(stale)
	if !reflect.DeepEqual(stale, []string{"f1", "f2"}) {
		t.Fatalf("StaleFecs = %v", stale)
	}
}

func TestPlaceholderAndStates(t *testing.T) {
	tb := NewTable(5)
	if tb.State(3) != StateNormal {
		t.Fatal("default state should be normal")
	}
	tb.SetBinding("f1", 100)
	tb.AddPlaceholder("f1", 110)
	if tb.Count(PlaceholderID) != 0 {
		t.Fatal("placeholder must not count as a client ownership")
	}
	// Claim sequence: add the real owner, then drop the placeholder.
	tb.AddOwner("f1", 9)
	if _, emptied := tb.RemoveOwner("f1", PlaceholderID); emptied {
		t.Fatal("binding must survive the claim")
	}
	if o := tb.Owner("f1", PlaceholderID); o != nil {
		t.Fatal("placeholder should be gone")
	}
	if tb.Count(9) != 1 {
		t.Fatalf("count = %d, want 1", tb.Count(9))
	}
	tb.SetState(3, StateOffline)
	if tb.State(3) != StateOffline {
		t.Fatal("state not recorded")
	}
}
