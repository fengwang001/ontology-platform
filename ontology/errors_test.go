package ontology

import "testing"

func TestDanglingLinkType(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "P"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: "Nope"})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {"Nope"}})
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 10, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrDanglingReference {
		t.Fatalf("kind=%d want dangling", kind)
	}

	// No observable mutation results from the failed determination.
	if got := len(st.AuditLog()); got != 0 {
		t.Fatalf("audit entries after error=%d want 0", got)
	}
	if st.StreamLength() != 3 {
		t.Fatalf("stream mutated by error")
	}
}

func TestDanglingPeer(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 5, ObjectID: "c", TypeID: typeChild})
	// Edge references p before p is created.
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 6, ObjectID: "ghost", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 9, ObjectID: "ghost", TypeID: "P"})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 8, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrDanglingReference {
		t.Fatalf("kind=%d want dangling peer", kind)
	}
}

func TestBeforeFirstAppearance(t *testing.T) {
	st := New()
	buildChildStream(st, 0, 0)
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 0, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrBeforeFirstAppearance {
		t.Fatalf("kind=%d want before-first", kind)
	}
}

func TestAmbiguousOrder(t *testing.T) {
	events := []Event{
		{Kind: EvLinkTypeDeclared, Time: 0, Seq: 1, TypeID: linkParent},
		{Kind: EvObjectCreated, Time: 1, Seq: 2, ObjectID: "p", TypeID: "P"},
		{Kind: EvObjectCreated, Time: 1, Seq: 3, ObjectID: "c", TypeID: typeChild},
		// Same (time, seq) pair -> unresolvable.
		{Kind: EvLinkEstablished, Time: 5, Seq: 7, ObjectID: "p", PeerID: "c", TypeID: linkParent},
		{Kind: EvPropertyAssigned, Time: 5, Seq: 7, ObjectID: "c", PropertyKey: "z"},
	}
	st := New()
	st.ImportStream(events)
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 10, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrAmbiguousOrder {
		t.Fatalf("kind=%d want ambiguous", kind)
	}

	// A prefix ending before the tie is still resolvable.
	res, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 4, SkipCrossCheck: true})
	if err != nil || res.Status != StatusActive {
		t.Fatalf("pre-tie determination: res=%+v err=%v", res, err)
	}
}

// TestErrorPriority: dangling reference beats ambiguous order beats
// before-first according to the documented fixed ordering.
func TestErrorPriority(t *testing.T) {
	events := []Event{
		{Kind: EvObjectCreated, Time: 2, Seq: 1, ObjectID: "c", TypeID: typeChild},
		// dangling peer (ghost created at 13, used at 12) AND an
		// undecidable tie at (12,5).
		{Kind: EvLinkEstablished, Time: 12, Seq: 5, ObjectID: "ghost", PeerID: "c", TypeID: "Missing"},
		{Kind: EvPropertyAssigned, Time: 12, Seq: 5, ObjectID: "c", PropertyKey: "z"},
		{Kind: EvObjectCreated, Time: 13, Seq: 6, ObjectID: "ghost", TypeID: "P"},
	}
	st := New()
	st.ImportStream(events)
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {"Missing"}})

	// dangling + ambiguous -> dangling wins.
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 20, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrDanglingReference {
		t.Fatalf("kind=%d want dangling (top priority)", kind)
	}

	// ambiguous + before-first (query before creation) -> before-first wins.
	_, err = st.Determine(DetermineRequest{ObjectID: "c", AtTime: 1, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrBeforeFirstAppearance {
		t.Fatalf("kind=%d want before-first over ambiguous", kind)
	}
}

// TestRuleSuperseded: a HEAD determination that finalizes after a rule
// adjustment fails with ErrRuleSuperseded (priority 1).
func TestRuleSuperseded(t *testing.T) {
	st := New()
	buildChildStream(st, 5, 10)
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})

	finalizeHook = func() {
		finalizeHook = nil
		mustRule(t, st, "R2", 50, false, map[string][]string{typeChild: {linkParent}})
	}

	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 12})
	if kind := errKind(t, err); kind != ErrRuleSuperseded {
		t.Fatalf("kind=%d want superseded", kind)
	}
	// Failed HEAD determination leaves no audit entry.
	if n := len(st.AuditLog()); n != 0 {
		t.Fatalf("audit entries=%d want 0 after superseded", n)
	}

	// The same pinned version remains fully idempotent afterward.
	r1, err := st.Determine(DetermineRequest{
		ObjectID: "c", AtTime: 12,
		Basis:          Basis{VersionID: "R1"},
		SkipCrossCheck: true,
	})
	if err != nil {
		t.Fatalf("pinned R1: %v", err)
	}
	if r1.Status != StatusRetroactiveOrphan {
		t.Fatalf("pinned R1 status=%d", r1.Status)
	}
}

func TestNoRuleInstalled(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	_, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 5, SkipCrossCheck: true})
	if kind := errKind(t, err); kind != ErrRuleSuperseded {
		t.Fatalf("kind=%d want superseded(no rule)", kind)
	}
}
