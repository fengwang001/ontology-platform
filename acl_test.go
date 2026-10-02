package ontology

import (
	"bytes"
	"errors"
	"testing"
)

func mustNewACL(t *testing.T, D, K int) *ACL {
	t.Helper()
	a, err := New(D, K)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", D, K, err)
	}
	return a
}

func mustAdd(t *testing.T, a *ACL, id, parent string, container bool) {
	t.Helper()
	if err := a.AddNode([]byte(id), []byte(parent), container); err != nil {
		t.Fatalf("AddNode(%q under %q): %v", id, parent, err)
	}
}

func mustSet(t *testing.T, a *ACL, node string, aces []ACE, protected bool) {
	t.Helper()
	if err := a.SetACL([]byte(node), aces, protected); err != nil {
		t.Fatalf("SetACL(%q): %v", node, err)
	}
}

func assertEffective(t *testing.T, got []EffectiveACE, want []EffectiveACE) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("effective length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Allow != want[i].Allow ||
			!bytes.Equal(got[i].Principal, want[i].Principal) ||
			got[i].Mask != want[i].Mask ||
			got[i].Flags != want[i].Flags ||
			!bytes.Equal(got[i].Source, want[i].Source) {
			t.Fatalf("effective[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestSpecExample(t *testing.T) {
	a := mustNewACL(t, 4, 16)
	mustAdd(t, a, "d", RootID, true)
	mustAdd(t, a, "f", "d", false)
	mustAdd(t, a, "g", "d", true)

	mustSet(t, a, RootID, []ACE{
		{Allow: true, Principal: []byte("grp"), Mask: 0b0011, Flags: FlagOI | FlagCI},
		{Allow: false, Principal: []byte("usr"), Mask: 0b0010, Flags: FlagCI},
	}, false)
	mustSet(t, a, "d", []ACE{
		{Allow: true, Principal: []byte("usr"), Mask: 0b0010},
		{Allow: false, Principal: []byte("grp"), Mask: 0b0100, Flags: FlagOI},
	}, false)

	rootWant := []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("usr"), Mask: 0b0010, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: true, Principal: []byte("grp"), Mask: 0b0011, Flags: FlagOI | FlagCI}, Source: []byte(RootID)},
	}
	assertEffective(t, mustEffective(t, a, RootID), rootWant)

	dWant := []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("grp"), Mask: 0b0100, Flags: FlagOI}, Source: []byte("d")},
		{ACE: ACE{Allow: true, Principal: []byte("usr"), Mask: 0b0010}, Source: []byte("d")},
		{ACE: ACE{Allow: false, Principal: []byte("usr"), Mask: 0b0010, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: true, Principal: []byte("grp"), Mask: 0b0011, Flags: FlagOI | FlagCI}, Source: []byte(RootID)},
	}
	assertEffective(t, mustEffective(t, a, "d"), dWant)

	token := [][]byte{[]byte("usr"), []byte("grp")}
	decision := mustEval(t, a, token, "d", 0b0010)
	if decision.Allowed != true || decision.Result != ResultGrant || decision.DecisiveIndex != 1 ||
		!bytes.Equal(decision.Source, []byte("d")) || decision.Inherited || decision.Granted != 0b0010 {
		t.Fatalf("d/0010 decision = %#v", decision)
	}

	decision = mustEval(t, a, token, "d", 0b0011)
	if !decision.Allowed || decision.Result != ResultGrant || decision.DecisiveIndex != 3 ||
		!bytes.Equal(decision.Source, []byte(RootID)) || !decision.Inherited || decision.Granted != 0b0011 {
		t.Fatalf("d/0011 decision = %#v", decision)
	}

	decision = mustEval(t, a, token, "d", 0b0111)
	if decision.Allowed || decision.Result != ResultDenyHit || decision.DecisiveIndex != 0 ||
		!bytes.Equal(decision.Source, []byte("d")) || decision.Inherited || decision.Granted != 0 {
		t.Fatalf("d/0111 decision = %#v", decision)
	}

	fWant := []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("grp"), Mask: 0b0100}, Source: []byte("d")},
		{ACE: ACE{Allow: true, Principal: []byte("grp"), Mask: 0b0011}, Source: []byte(RootID)},
	}
	assertEffective(t, mustEffective(t, a, "f"), fWant)

	decision = mustEval(t, a, token, "f", 0b0110)
	if decision.Allowed || decision.Result != ResultDenyHit || decision.DecisiveIndex != 0 ||
		!bytes.Equal(decision.Source, []byte("d")) || !decision.Inherited {
		t.Fatalf("f/0110 decision = %#v", decision)
	}
	decision = mustEval(t, a, token, "f", 0b0010)
	if !decision.Allowed || decision.Result != ResultGrant || decision.DecisiveIndex != 1 ||
		!bytes.Equal(decision.Source, []byte(RootID)) || !decision.Inherited || decision.Granted != 0b0010 {
		t.Fatalf("f/0010 decision = %#v", decision)
	}

	gWant := []EffectiveACE{
		{ACE: ACE{Allow: false, Principal: []byte("grp"), Mask: 0b0100, Flags: FlagOI | FlagIO}, Source: []byte("d")},
		{ACE: ACE{Allow: false, Principal: []byte("usr"), Mask: 0b0010, Flags: FlagCI}, Source: []byte(RootID)},
		{ACE: ACE{Allow: true, Principal: []byte("grp"), Mask: 0b0011, Flags: FlagOI | FlagCI}, Source: []byte(RootID)},
	}
	assertEffective(t, mustEffective(t, a, "g"), gWant)

	decision = mustEval(t, a, token, "g", 0b0010)
	if decision.Allowed || decision.Result != ResultDenyHit || decision.DecisiveIndex != 1 ||
		decision.Granted != 0 {
		t.Fatalf("g/0010 decision = %#v", decision)
	}
	decision = mustEval(t, a, token, "g", 0b0001)
	if !decision.Allowed || decision.Result != ResultGrant || decision.DecisiveIndex != 2 ||
		decision.Granted != 0b0001 {
		t.Fatalf("g/0001 decision = %#v", decision)
	}

	mustSet(t, a, "g", []ACE{{Allow: true, Principal: []byte("usr"), Mask: 0b0001}}, true)
	assertEffective(t, mustEffective(t, a, "g"), []EffectiveACE{
		{ACE: ACE{Allow: true, Principal: []byte("usr"), Mask: 0b0001}, Source: []byte("g")},
	})
	decision = mustEval(t, a, token, "g", 0b0010)
	if decision.Allowed || decision.Result != ResultImplicitDeny || decision.DecisiveIndex != -1 ||
		decision.Source != nil || decision.Inherited || decision.Granted != 0 {
		t.Fatalf("protected g/0010 decision = %#v", decision)
	}
}

func mustEffective(t *testing.T, a *ACL, node string) []EffectiveACE {
	t.Helper()
	got, err := a.Effective([]byte(node))
	if err != nil {
		t.Fatalf("Effective(%q): %v", node, err)
	}
	return got
}

func mustEval(t *testing.T, a *ACL, token [][]byte, node string, request uint16) Decision {
	t.Helper()
	decision, err := a.Eval(token, []byte(node), request)
	if err != nil {
		t.Fatalf("Eval(%q, %b): %v", node, request, err)
	}
	return decision
}

func TestRejectedOperationsPreserveVersion(t *testing.T) {
	a := mustNewACL(t, 2, 2)
	mustAdd(t, a, "d", RootID, true)
	mustAdd(t, a, "f", "d", false)
	mustAdd(t, a, "c", "d", true)

	operations := []struct {
		name string
		run  func() error
		want error
	}{
		{"duplicate id", func() error { return a.AddNode([]byte("d"), []byte(RootID), true) }, ErrConflict},
		{"object parent", func() error { return a.AddNode([]byte("x"), []byte("f"), true) }, ErrConflict},
		{"missing parent", func() error { return a.AddNode([]byte("x"), []byte("missing"), true) }, ErrNotFound},
		{"depth limit", func() error { return a.AddNode([]byte("y"), []byte("c"), true) }, ErrLimitExceeded},
		{"acl limit", func() error {
			return a.SetACL([]byte("d"), []ACE{
				{Principal: []byte("p1"), Mask: 1},
				{Principal: []byte("p2"), Mask: 1},
				{Principal: []byte("p3"), Mask: 1},
			}, false)
		}, ErrLimitExceeded},
		{"move root", func() error { return a.Move([]byte(RootID), []byte("d")) }, ErrConflict},
		{"move same parent", func() error { return a.Move([]byte("f"), []byte("d")) }, ErrConflict},
	}

	for _, op := range operations {
		t.Run(op.name, func(t *testing.T) {
			before := a.Version()
			err := op.run()
			if !errors.Is(err, op.want) {
				t.Fatalf("error = %v, want %v", err, op.want)
			}
			if after := a.Version(); after != before {
				t.Fatalf("version changed from %d to %d", before, after)
			}
		})
	}
}
