package view_test

import (
	"errors"
	"math"
	"testing"

	"ontology/change"
	"ontology/view"
)

// TestEmptyGroupDisappears: deleting the last member removes the group
// outright; Lookup reports not-found rather than a zero-valued aggregate.
func TestEmptyGroupDisappears(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(1, "k", "g", 7)); err != nil {
		t.Fatal(err)
	}
	if err := v.Submit(del(2, "k", "g", 7)); err != nil {
		t.Fatal(err)
	}
	if _, ok := v.Lookup("g"); ok {
		t.Fatal("emptied group still returned by Lookup")
	}
	if names := v.GroupNames(); len(names) != 0 {
		t.Fatalf("Groups contains emptied group: %v", names)
	}
}

func TestEmptyView(t *testing.T) {
	v := newTestView(t)
	if len(v.Groups()) != 0 {
		t.Fatal("new view must be empty")
	}
	if v.MaxVersion() != 0 {
		t.Fatal("new view max version must be 0")
	}
	if _, ok := v.Lookup("anything"); ok {
		t.Fatal("empty view lookup must miss")
	}
}

func TestSingleGroupSingleRecord(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(1, "only", "only", 42)); err != nil {
		t.Fatal(err)
	}
	g, ok := v.Lookup("only")
	if !ok {
		t.Fatal("group missing")
	}
	if g.Count != 1 || g.Sum != 42 || g.Min != 42 || g.Max != 42 || g.Distinct != 1 {
		t.Fatalf("unexpected single record result: %+v", g)
	}
}

func TestEmptyStringGroupIsLegal(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(1, "k", "", 1)); err != nil {
		t.Fatalf("empty group key must be legal: %v", err)
	}
	g, ok := v.Lookup("")
	if !ok || g.Count != 1 {
		t.Fatalf("empty group lookup failed: %+v %v", g, ok)
	}
}

func TestMissingGroupAndNaNRejected(t *testing.T) {
	v := newTestView(t)
	missing := ins(1, "k", "g", 1)
	missing.From.GroupPresent = false
	if !errors.Is(v.Submit(missing), view.ErrInvalidChange) {
		t.Fatal("missing group must be rejected")
	}
	nan := ins(2, "k2", "g", math.NaN())
	if !errors.Is(v.Submit(nan), view.ErrInvalidChange) {
		t.Fatal("NaN must be rejected")
	}
	if v.Stats().Rejected != 2 {
		t.Fatalf("rejected=%d want 2", v.Stats().Rejected)
	}
	if len(v.Groups()) != 0 {
		t.Fatal("view polluted by rejected changes")
	}
}

func TestSignedZeroEqual(t *testing.T) {
	v := newTestView(t)
	if err := v.Submit(ins(1, "a", "g", math.Copysign(0, 1))); err != nil {
		t.Fatal(err)
	}
	if err := v.Submit(ins(2, "b", "g", math.Copysign(0, -1))); err != nil {
		t.Fatal(err)
	}
	g, _ := v.Lookup("g")
	if g.Distinct != 1 {
		t.Fatalf("+0/-0 must count as one distinct value, got %v", g.Distinct)
	}
	if math.Signbit(g.Sum) {
		t.Fatal("sum should normalize to +0")
	}
}

// TestUpdateMovesBetweenGroups: an update moves a record out of its old
// group and into a new one; both groups must end correct.
func TestUpdateMovesBetweenGroups(t *testing.T) {
	v := newTestView(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(v.Submit(ins(1, "k", "old", 5)))
	must(v.Submit(ins(2, "stay", "old", 3)))
	upd := change.Change{Version: 3, Op: change.OpUpdate, Key: "k",
		From: change.Row{Group: "old", GroupPresent: true, Value: 5},
		To:   change.Row{Group: "new", GroupPresent: true, Value: 9},
	}
	must(v.Submit(upd))

	old, ok := v.Lookup("old")
	if !ok || old.Count != 1 || old.Sum != 3 || old.Min != 3 || old.Max != 3 {
		t.Fatalf("old group wrong: %+v ok=%v", old, ok)
	}
	nw, ok := v.Lookup("new")
	if !ok || nw.Count != 1 || nw.Sum != 9 || nw.Min != 9 || nw.Max != 9 {
		t.Fatalf("new group wrong: %+v ok=%v", nw, ok)
	}
}

func TestAllRecordsSameGroup(t *testing.T) {
	v := newTestView(t)
	for i := 1; i <= 100; i++ {
		if err := v.Submit(ins(uint64(i), key(i), "only", float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if len(v.Groups()) != 1 {
		t.Fatalf("want exactly 1 group, got %d", len(v.Groups()))
	}
	g, _ := v.Lookup("only")
	if g.Count != 100 || g.Min != 1 || g.Max != 100 || g.Distinct != 100 {
		t.Fatalf("aggregate wrong: %+v", g)
	}
}

func key(i int) string {
	if i == 0 {
		return "k0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return "k" + string(b)
}
