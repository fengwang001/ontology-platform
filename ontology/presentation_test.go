package ontology

import "testing"

// TestRetroactiveOrphanWithLaterActivity: a retroactive orphan that
// nevertheless keeps receiving property assignments and new links must
// be reported as retroactive (not cascade) and carry the activity list.
func TestRetroactiveOrphanWithLaterActivity(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: "Tagged"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "P"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "g", TypeID: "G"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 10, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvPropertyAssigned, Time: 12, ObjectID: "c", PropertyKey: "color", PropertyValue: "red"})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 14, ObjectID: "c", PeerID: "g", TypeID: "Tagged"})
	st.Append(EventInput{Kind: EvPropertyAssigned, Time: 16, ObjectID: "c", PropertyKey: "size", PropertyValue: "big"})
	mustRule(t, st, "R1", 20, true, map[string][]string{typeChild: {linkParent}})

	res := mustDetermine(t, st, "c", 30)
	if res.Status != StatusRetroactiveOrphan {
		t.Fatalf("status=%d want retroactive orphan", res.Status)
	}
	if res.HasMarkedRecord {
		t.Fatalf("retroactive orphan must not be equated with a cascade record")
	}
	if res.VirtualOrphanAt.Time != 10 {
		t.Fatalf("virtual point=%+v want 10", res.VirtualOrphanAt)
	}
	if len(res.ActivityAfterVirtual) != 3 {
		t.Fatalf("activity after virtual=%d want 3: %+v", len(res.ActivityAfterVirtual), res.ActivityAfterVirtual)
	}
	kinds := map[EventKind]int{}
	for _, a := range res.ActivityAfterVirtual {
		kinds[a.Kind]++
	}
	if kinds[EvPropertyAssigned] != 2 || kinds[EvLinkEstablished] != 1 {
		t.Fatalf("activity kinds=%+v", kinds)
	}

	net, err := st.Rebuild(30, Basis{})
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	state := net.Objects["c"]
	if state == nil || state.Status != StatusRetroactiveOrphan {
		t.Fatalf("rebuilt status=%+v", state)
	}
	if len(state.ActivityAfterVirtual) != 3 {
		t.Fatalf("rebuilt activity=%+v", state.ActivityAfterVirtual)
	}
}

// TestCascadeOrphan: an explicit OrphanMarked record wins and is
// reported distinctly with its own timestamp.
func TestCascadeOrphan(t *testing.T) {
	st := New()
	buildChildStream(st, 5, 10)
	st.Append(EventInput{Kind: EvOrphanMarked, Time: 11, ObjectID: "c"})
	st.Append(EventInput{Kind: EvPropertyAssigned, Time: 13, ObjectID: "c", PropertyKey: "x"})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	res := mustDetermine(t, st, "c", 20)
	if res.Status != StatusCascadeOrphan {
		t.Fatalf("status=%d want cascade orphan", res.Status)
	}
	if !res.HasMarkedRecord || res.MarkedOrphanAt.Time != 11 {
		t.Fatalf("marked=%+v", res.MarkedOrphanAt)
	}
	if len(res.State.ActivityAfterMarked) != 1 {
		t.Fatalf("marked-later activity=%+v", res.State.ActivityAfterMarked)
	}
}

// TestIdempotency: the same (object, time, pinned version) gives
// identical results no matter when it is called or how often.
func TestIdempotency(t *testing.T) {
	st := New()
	buildChildStream(st, 5, 10)
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})

	var first *DetermineResult
	for i := 0; i < 5; i++ {
		res := mustDetermine(t, st, "c", 12)
		if first == nil {
			first = res
			continue
		}
		if res.Status != first.Status || res.VirtualOrphanAt != first.VirtualOrphanAt ||
			res.EventsScanned != first.EventsScanned {
			t.Fatalf("repeated determination #%d diverged: %+v vs %+v", i, res, first)
		}
	}

	// A later, stricter rule adjustment must not move the pinned verdict.
	mustRule(t, st, "R2", 50, false, map[string][]string{typeChild: {linkParent, "Other"}})
	res, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 12, Basis: Basis{VersionID: "R1"}, SkipCrossCheck: true})
	if err != nil {
		t.Fatalf("pinned determine: %v", err)
	}
	if res.Status != StatusRetroactiveOrphan || res.VirtualOrphanAt != first.VirtualOrphanAt {
		t.Fatalf("pinned verdict moved after later adjustment: %+v", res)
	}
}
