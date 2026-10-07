package ontology

import "testing"

// TestRetroactiveBoundary enumerates the boundary semantics of a
// retroactive rule applied to a revocation that predates the rule's
// effective point.
func TestRetroactiveBoundary(t *testing.T) {
	// Rule becomes current at t=20, revocation happened at t=10.
	cases := []struct {
		name string
		at   int64
		want ObjectStatus
	}{
		{"before revocation", 9, StatusActive},
		{"at revocation", 10, StatusRetroactiveOrphan},
		{"between revocation and rule effective", 19, StatusRetroactiveOrphan},
		{"at rule effective", 20, StatusRetroactiveOrphan},
		{"well after", 100, StatusRetroactiveOrphan},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := New()
			buildChildStream(st, 5, 10)
			mustRule(t, st, "R1", 20, true, map[string][]string{typeChild: {linkParent}})
			res := mustDetermine(t, st, "c", tc.at)
			if res.Status != tc.want {
				t.Fatalf("status=%d want %d", res.Status, tc.want)
			}
			if tc.want == StatusRetroactiveOrphan && res.VirtualOrphanAt.Time != 10 {
				t.Fatalf("virtual point=%+v want time 10", res.VirtualOrphanAt)
			}
		})
	}
}

// TestNonRetroactiveBoundary enumerates the same timeline with a
// forward-only rule: the pre-effective revocation is grandfathered.
func TestNonRetroactiveBoundary(t *testing.T) {
	cases := []struct {
		name string
		at   int64
		want ObjectStatus
	}{
		{"before rule effective", 19, StatusActive},
		{"at rule effective with no windowed edges", 20, StatusActive},
		{"after, still active (grandfathered)", 100, StatusActive},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := New()
			buildChildStream(st, 5, 10)
			mustRule(t, st, "R1", 20, false, map[string][]string{typeChild: {linkParent}})
			res := mustDetermine(t, st, "c", tc.at)
			if res.Status != tc.want {
				t.Fatalf("status=%d want %d", res.Status, tc.want)
			}
		})
	}
}

// TestNonRetroactiveWindowedRevocation: a forward-only rule does fire
// when establish+revoke both happen after its effective point.
func TestNonRetroactiveWindowedRevocation(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "ParentObj"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	mustRule(t, st, "R1", 10, false, map[string][]string{typeChild: {linkParent}})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 15, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 18, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	if res := mustDetermine(t, st, "c", 17); res.Status != StatusActive {
		t.Fatalf("at 17 status=%d want active", res.Status)
	}
	res := mustDetermine(t, st, "c", 18)
	if res.Status != StatusRetroactiveOrphan {
		t.Fatalf("at 18 status=%d want retroactive orphan", res.Status)
	}
	if res.VirtualOrphanAt.Time != 18 {
		t.Fatalf("virtual point=%+v want 18", res.VirtualOrphanAt)
	}
}

// TestNonRetroactiveReestablishment: a forward-only rule with a
// post-effective re-establishment does not treat the pre-effective
// revocation as the trigger.
func TestNonRetroactiveReestablishment(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "ParentObj"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 8, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	mustRule(t, st, "R1", 10, false, map[string][]string{typeChild: {linkParent}})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 12, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 14, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	res := mustDetermine(t, st, "c", 20)
	if res.Status != StatusRetroactiveOrphan || res.VirtualOrphanAt.Time != 14 {
		t.Fatalf("status=%d virtual=%+v want retroactive@14", res.Status, res.VirtualOrphanAt)
	}
}

// TestMultipleRequiredTypes: the trigger requires every required type to
// have been present and later cleared.
func TestMultipleRequiredTypes(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: "A"})
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: "B"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "P"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "q", TypeID: "Q"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: "A"})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "q", PeerID: "c", TypeID: "B"})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 8, ObjectID: "p", PeerID: "c", TypeID: "A"})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {"A", "B"}})
	if res := mustDetermine(t, st, "c", 8); res.Status != StatusActive {
		t.Fatalf("only A cleared: status=%d want active", res.Status)
	}
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 9, ObjectID: "q", PeerID: "c", TypeID: "B"})
	res := mustDetermine(t, st, "c", 9)
	if res.Status != StatusRetroactiveOrphan || res.VirtualOrphanAt.Time != 9 {
		t.Fatalf("status=%d virtual=%+v want orphan@9", res.Status, res.VirtualOrphanAt)
	}
}

// TestNeverHadRequiredLink: an object that never had a required link is
// not retroactively orphaned (requirement was never "withdrawn").
func TestNeverHadRequiredLink(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	res := mustDetermine(t, st, "c", 50)
	if res.Status != StatusActive {
		t.Fatalf("status=%d want active for never-linked object", res.Status)
	}
}

// TestReestablishmentResetsVirtualPoint: once a new required edge is
// established after the first clear, the earlier virtual point is
// retracted and a later clearance becomes the new point.
func TestReestablishmentResetsVirtualPoint(t *testing.T) {
	st := New()
	buildChildStream(st, 5, 10)
	st.Append(EventInput{Kind: EvLinkEstablished, Time: 12, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	st.Append(EventInput{Kind: EvLinkRevoked, Time: 15, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	res := mustDetermine(t, st, "c", 20)
	if res.Status != StatusRetroactiveOrphan || res.VirtualOrphanAt.Time != 15 {
		t.Fatalf("status=%d virtual=%+v want 15", res.Status, res.VirtualOrphanAt)
	}
	if res := mustDetermine(t, st, "c", 13); res.Status != StatusActive {
		t.Fatalf("re-linked at 12: status=%d want active", res.Status)
	}
}
