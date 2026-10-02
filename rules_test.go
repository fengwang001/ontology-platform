package ontology

import (
	"bytes"
	"testing"
)

func TestInheritanceFlagsAndGrantAccumulation(t *testing.T) {
	a := mustNewACL(t, 4, 16)
	mustAdd(t, a, "a", RootID, true)
	mustAdd(t, a, "b", "a", true)
	mustAdd(t, a, "f", "b", false)

	mustSet(t, a, RootID, []ACE{
		{Allow: true, Principal: []byte("p"), Mask: 0b0001, Flags: FlagCI},
		{Allow: false, Principal: []byte("p"), Mask: 0b0010, Flags: FlagCI},
		{Allow: true, Principal: []byte("p"), Mask: 0b0100, Flags: FlagOI},
		{Allow: false, Principal: []byte("p"), Mask: 0b1000 | 0b10000000, Flags: FlagCI},
		{Allow: false, Principal: []byte("p"), Mask: 0b10000, Flags: FlagOI | FlagNP},
		{Allow: true, Principal: []byte("p"), Mask: 0b100000, Flags: FlagCI | FlagNP},
	}, false)
	mustSet(t, a, "a", []ACE{
		{Allow: true, Principal: []byte("p"), Mask: 0b0010},
		{Allow: true, Principal: []byte("p"), Mask: 0b1000},
		{Allow: false, Principal: []byte("p"), Mask: 0b10000, Flags: FlagOI | FlagIO},
	}, false)

	token := [][]byte{[]byte("p")}
	decision := mustEval(t, a, token, "a", 0b0011)
	if !decision.Allowed || decision.Result != ResultGrant || decision.DecisiveIndex != 5 || decision.Granted != 0b0011 {
		t.Fatalf("deny covering only granted bits was not ignored: %#v", decision)
	}
	decision = mustEval(t, a, token, "a", 0b10001000)
	if decision.Allowed || decision.Result != ResultDenyHit || decision.DecisiveIndex != 4 ||
		!bytes.Equal(decision.Source, []byte(RootID)) || decision.Granted != 0b1000 {
		t.Fatalf("later inherited deny should hit ungranted bit: %#v", decision)
	}

	effectiveB := mustEffective(t, a, "b")
	assertEffective(t, effectiveB, []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("p"), Mask: 0b10000, Flags: FlagOI | FlagIO}, Source: []byte("a")},
		{ACE: ACE{Allow: false, Principal: []byte("p"), Mask: 0b0010, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: false, Principal: []byte("p"), Mask: 0b1000 | 0b10000000, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: true, Principal: []byte("p"), Mask: 0b0001, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: true, Principal: []byte("p"), Mask: 0b0100, Flags: FlagOI | FlagIO}, Source: []byte(RootID)},
	})

	effectiveF := mustEffective(t, a, "f")
	assertEffective(t, effectiveF, []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("p"), Mask: 0b10000, Flags: 0}, Source: []byte("a")},
		{ACE: ACE{Allow: true, Principal: []byte("p"), Mask: 0b0100, Flags: 0}, Source: []byte(RootID)},
	})
	decision = mustEval(t, a, token, "f", 0b10100)
	if decision.Allowed || decision.Result != ResultDenyHit || decision.DecisiveIndex != 0 || decision.Granted != 0 {
		t.Fatalf("object should receive propagated IO/OI deny with flags cleared: %#v", decision)
	}
	decision = mustEval(t, a, token, "f", 0b0100)
	if !decision.Allowed || decision.DecisiveIndex != 1 || decision.Granted != 0b0100 {
		t.Fatalf("object should receive OI allow: %#v", decision)
	}

	decision = mustEval(t, a, token, "b", 0b10000)
	if decision.Allowed || decision.Result != ResultImplicitDeny || decision.Granted != 0 {
		t.Fatalf("IO entry should not evaluate on this node: %#v", decision)
	}

	mustSet(t, a, "b", []ACE{{Allow: true, Principal: []byte("p"), Mask: 0b100000}}, true)
	effectiveB = mustEffective(t, a, "b")
	assertEffective(t, effectiveB, []EffectiveACE{
		{ACE: ACE{Allow: true, Principal: []byte("p"), Mask: 0b100000}, Source: []byte("b")},
	})
	decision = mustEval(t, a, token, "b", 0b10000)
	if decision.Allowed || decision.Result != ResultImplicitDeny {
		t.Fatalf("protected node should hide inheritance: %#v", decision)
	}
}

func TestMoveChangesInheritanceAndChecksSubtreeDepth(t *testing.T) {
	a := mustNewACL(t, 3, 8)
	mustAdd(t, a, "old", RootID, true)
	mustAdd(t, a, "b", "old", true)
	mustAdd(t, a, "d", "b", true)
	mustAdd(t, a, "new", RootID, true)
	mustAdd(t, a, "deep", "new", true)

	mustSet(t, a, RootID, []ACE{{Allow: true, Principal: []byte("p"), Mask: 0b0001, Flags: FlagCI}}, false)
	mustSet(t, a, "old", []ACE{{Allow: true, Principal: []byte("p"), Mask: 0b0010, Flags: FlagCI}}, false)
	mustSet(t, a, "new", []ACE{{Allow: true, Principal: []byte("p"), Mask: 0b0100, Flags: FlagCI}}, false)

	token := [][]byte{[]byte("p")}
	decision := mustEval(t, a, token, "b", 0b0011)
	if !decision.Allowed || decision.Granted != 0b0011 {
		t.Fatalf("before move decision = %#v", decision)
	}

	if err := a.Move([]byte("b"), []byte("deep")); !errorsIs(err, ErrLimitExceeded) {
		t.Fatalf("move subtree depth error = %v, want %v", err, ErrLimitExceeded)
	}
	before := a.Version()
	if err := a.Move([]byte("b"), []byte("new")); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if a.Version() != before+1 {
		t.Fatalf("version = %d, want %d", a.Version(), before+1)
	}

	decision = mustEval(t, a, token, "d", 0b0101)
	if !decision.Allowed || decision.Granted != 0b0101 || decision.DecisiveIndex != 1 {
		t.Fatalf("after move inheritance = %#v", decision)
	}
	if err := a.Move([]byte("d"), []byte("b")); !errorsIs(err, ErrConflict) {
		t.Fatalf("move into descendant error = %v, want %v", err, ErrConflict)
	}
}

func errorsIs(err, target error) bool {
	return err != nil && (err == target || unwrapOnce(err) == target)
}

func unwrapOnce(err error) error {
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return wrapped.Unwrap()
	}
	return nil
}

func TestEvalCountersStopAtProtectedNodeAndIgnoreUnrelatedTree(t *testing.T) {
	a := mustNewACL(t, 64, 16)
	mustAdd(t, a, "a", RootID, true)
	mustAdd(t, a, "b", "a", true)
	mustSet(t, a, RootID, []ACE{
		{Principal: []byte("p"), Mask: 1},
		{Principal: []byte("p"), Mask: 2},
		{Principal: []byte("p"), Mask: 4},
	}, false)
	mustSet(t, a, "a", []ACE{
		{Principal: []byte("p"), Mask: 8},
		{Principal: []byte("p"), Mask: 16},
	}, false)
	mustSet(t, a, "b", []ACE{{Allow: true, Principal: []byte("p"), Mask: 32}}, true)

	beforeEntries := a.evalEntryCount()
	beforeNodes := a.evalNodeCount()
	mustEval(t, a, [][]byte{[]byte("q")}, "b", 32)
	firstEntries := a.evalEntryCount() - beforeEntries
	firstNodes := a.evalNodeCount() - beforeNodes
	if firstEntries != 1 || firstNodes != 1 {
		t.Fatalf("counters = entries %d nodes %d, want 1, 1", firstEntries, firstNodes)
	}

	for i := 0; i < 10_000; i++ {
		mustAdd(t, a, "u"+itoa(i), RootID, true)
	}
	beforeEntries = a.evalEntryCount()
	beforeNodes = a.evalNodeCount()
	mustEval(t, a, [][]byte{[]byte("q")}, "b", 32)
	secondEntries := a.evalEntryCount() - beforeEntries
	secondNodes := a.evalNodeCount() - beforeNodes
	if secondEntries != firstEntries || secondNodes != firstNodes {
		t.Fatalf("counters changed with unrelated tree: %d,%d vs %d,%d", secondEntries, secondNodes, firstEntries, firstNodes)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}
