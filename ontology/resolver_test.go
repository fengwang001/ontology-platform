package ontology

import "testing"

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func setupResolverState(t *testing.T) *PermissionState {
	t.Helper()
	ps := NewPermissionState()
	must(t, ps.UpsertGroup("gA", 1))
	must(t, ps.UpsertGroup("gB", 1))
	must(t, ps.UpsertGroup("gHigh", 10))
	must(t, ps.AddMember("alice", "gA"))
	must(t, ps.AddMember("alice", "gB"))
	must(t, ps.AddMember("alice", "gHigh"))
	must(t, ps.AddMember("bob", "gA"))
	return ps
}

func TestInvalidSubjectOutranksEverything(t *testing.T) {
	ps := setupResolverState(t)
	must(t, ps.SetObjectDecl("gHigh", "Doc", Allow))
	must(t, ps.SetLinkDecl("gHigh", "edit", Allow))
	got := DecideAt(ps.Snapshot(), "", "edit", "Doc", "Doc")
	if got.Verdict != VerdictInvalid {
		t.Fatalf("want invalid, got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestDefaultDenyWhenNothingDeclared(t *testing.T) {
	ps := setupResolverState(t)
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictDeny || got.Reason != "default-deny" {
		t.Fatalf("got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestLayerOverrideCombinations(t *testing.T) {
	cases := []struct {
		name       string
		object     Decision
		link       Decision
		group      GroupID
		want       Verdict
		wantReason string
	}{
		{"obj allow + link allow", Allow, Allow, "gA", VerdictAllow, "link-layer-allow"},
		{"obj allow + link deny", Allow, Deny, "gA", VerdictDeny, "link-layer-deny"},
		{"obj deny + link allow", Deny, Allow, "gA", VerdictAllow, "link-layer-allow"},
		{"obj deny + link deny", Deny, Deny, "gA", VerdictDeny, "link-layer-deny"},
		{"obj allow + link unset", Allow, Unset, "gA", VerdictAllow, "object-layer-allow"},
		{"obj deny + link unset", Deny, Unset, "gA", VerdictDeny, "object-layer-deny"},
		{"obj unset + link allow", Unset, Allow, "gA", VerdictAllow, "link-layer-allow"},
		{"obj unset + link deny", Unset, Deny, "gA", VerdictDeny, "link-layer-deny"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ps := setupResolverState(t)
			must(t, ps.SetObjectDecl(tc.group, "Doc", tc.object))
			must(t, ps.SetLinkDecl(tc.group, "edit", tc.link))
			got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
			if got.Verdict != tc.want || got.Reason != tc.wantReason {
				t.Fatalf("want %s/%s, got %s/%s", tc.want, tc.wantReason, got.Verdict, got.Reason)
			}
		})
	}
}

func TestEndpointDenyWinsMerge(t *testing.T) {
	ps := setupResolverState(t)
	// gA allows "Folder", gB denies "Doc"; alice is in both at priority 1.
	must(t, ps.SetObjectDecl("gA", "Folder", Allow))
	must(t, ps.SetObjectDecl("gB", "Doc", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "move", "Folder", "Doc")
	if got.Verdict != VerdictDeny {
		t.Fatalf("endpoint deny must win, got %s (%s)", got.Verdict, got.Reason)
	}

	// Both allowed -> allow.
	must(t, ps.SetObjectDecl("gB", "Doc", Allow))
	got = DecideAt(ps.Snapshot(), "alice", "move", "Folder", "Doc")
	if got.Verdict != VerdictAllow {
		t.Fatalf("both allowed -> allow, got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestSamePriorityContradictionDenied(t *testing.T) {
	ps := setupResolverState(t)
	// gA and gB share priority 1 and contradict on the link type.
	must(t, ps.SetLinkDecl("gA", "edit", Allow))
	must(t, ps.SetLinkDecl("gB", "edit", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictDeny || got.Reason != "link-layer-deny;tier-deny-wins" {
		t.Fatalf("got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestSamePriorityContradictionObjectLayer(t *testing.T) {
	ps := setupResolverState(t)
	must(t, ps.SetObjectDecl("gA", "Doc", Allow))
	must(t, ps.SetObjectDecl("gB", "Doc", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictDeny || !got.FromObjectVote.conflict {
		t.Fatalf("got %s (%s) conflict=%v", got.Verdict, got.Reason, got.FromObjectVote.conflict)
	}
}

func TestHigherPriorityGroupWritesTheVerdict(t *testing.T) {
	ps := setupResolverState(t)
	must(t, ps.SetLinkDecl("gA", "edit", Allow))
	must(t, ps.SetLinkDecl("gHigh", "edit", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictDeny {
		t.Fatalf("higher priority deny wins, got %s", got.Verdict)
	}
	must(t, ps.SetLinkDecl("gHigh", "edit", Allow))
	got = DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictAllow {
		t.Fatalf("higher priority allow wins, got %s", got.Verdict)
	}
}

func TestCrossAxisAmbiguity(t *testing.T) {
	ps := setupResolverState(t)
	// gA: link Allow. gB (equal priority): object Deny on Doc.
	must(t, ps.SetLinkDecl("gA", "edit", Allow))
	must(t, ps.SetObjectDecl("gB", "Doc", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictAmbiguous {
		t.Fatalf("want ambiguous, got %s (%s)", got.Verdict, got.Reason)
	}

	// Raising gA's priority above gB settles it: link Allow stands.
	must(t, ps.UpsertGroup("gA", 5))
	got = DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictAllow {
		t.Fatalf("unequal priorities settle the conflict, got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestSameGroupCrossLayerIsNotAmbiguous(t *testing.T) {
	ps := setupResolverState(t)
	// One group saying link Allow and object Deny is a layer override, not a
	// multi-group contradiction.
	must(t, ps.SetLinkDecl("gA", "edit", Allow))
	must(t, ps.SetObjectDecl("gA", "Doc", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictAllow {
		t.Fatalf("link override within same group, got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestCrossAxisAmbiguityViaTierDenyWins(t *testing.T) {
	ps := setupResolverState(t)
	// gA: link Allow. gB: object Allow on Doc. A third equal-priority group
	// denies Doc, so the object winning tier merges to Deny via deny-wins;
	// the cross-axis conflict must still surface as ambiguity.
	must(t, ps.UpsertGroup("gC", 1))
	must(t, ps.AddMember("alice", "gC"))
	must(t, ps.SetLinkDecl("gA", "edit", Allow))
	must(t, ps.SetObjectDecl("gB", "Doc", Allow))
	must(t, ps.SetObjectDecl("gC", "Doc", Deny))
	got := DecideAt(ps.Snapshot(), "alice", "edit", "Doc", "Doc")
	if got.Verdict != VerdictAmbiguous {
		t.Fatalf("want ambiguous via tier deny-wins, got %s (%s)", got.Verdict, got.Reason)
	}
}

func TestSubjectWithNoMembershipDefaultsDeny(t *testing.T) {
	ps := setupResolverState(t)
	must(t, ps.SetObjectDecl("gA", "Doc", Allow))
	got := DecideAt(ps.Snapshot(), "nobody", "edit", "Doc", "Doc")
	if got.Verdict != VerdictDeny || got.Reason != "default-deny" {
		t.Fatalf("got %s (%s)", got.Verdict, got.Reason)
	}
}
