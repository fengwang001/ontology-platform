package acl

import (
	"reflect"
	"testing"
)

func mustStore(t *testing.T, D, K int) *Store {
	t.Helper()
	s, err := NewStore(D, K)
	if err != nil {
		t.Fatalf("NewStore(%d, %d): %v", D, K, err)
	}
	return s
}

func mustAdd(t *testing.T, s *Store, id, parent string, container bool) {
	t.Helper()
	if err := s.AddNode(id, parent, container); err != nil {
		t.Fatalf("AddNode(%q, %q, %v): %v", id, parent, container, err)
	}
}

func mustSetACL(t *testing.T, s *Store, node string, aces []ACE, protected bool) {
	t.Helper()
	if err := s.SetACL(node, aces, protected); err != nil {
		t.Fatalf("SetACL(%q, %v, %v): %v", node, aces, protected, err)
	}
}

func mustEval(t *testing.T, s *Store, token []string, node string, R uint32) EvalResult {
	t.Helper()
	res, err := s.Eval(token, node, R)
	if err != nil {
		t.Fatalf("Eval(%v, %q, %#o): %v", token, node, R, err)
	}
	return res
}

func mustEffective(t *testing.T, s *Store, node string) []EffectiveEntry {
	t.Helper()
	eff, err := s.Effective(node)
	if err != nil {
		t.Fatalf("Effective(%q): %v", node, err)
	}
	return eff
}

func checkResult(t *testing.T, res EvalResult, kind ResultKind, idx int, source string, inherited bool, G uint32) {
	t.Helper()
	want := EvalResult{
		Allowed:           kind == ResultGranted,
		Kind:              kind,
		DecisiveIndex:     idx,
		DecisiveSource:    source,
		DecisiveInherited: inherited,
		Granted:           G,
	}
	if res != want {
		t.Fatalf("got %+v, want %+v", res, want)
	}
}

// TestSpecExample 完整复现题目中的示例。
func TestSpecExample(t *testing.T) {
	s := mustStore(t, 8, 8)
	mustAdd(t, s, "d", "/", true)
	mustAdd(t, s, "f", "d", false)
	mustAdd(t, s, "g", "d", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "grp", Mask: 0b0011, Flags: FlagOI | FlagCI},
		{Allow: false, Principal: "usr", Mask: 0b0010, Flags: FlagCI},
	}, false)
	mustSetACL(t, s, "d", []ACE{
		{Allow: true, Principal: "usr", Mask: 0b0010, Flags: 0},
		{Allow: false, Principal: "grp", Mask: 0b0100, Flags: FlagOI},
	}, false)
	token := []string{"usr", "grp"}

	wantRoot := []EffectiveEntry{
		{Allow: false, Principal: "usr", Mask: 0b0010, Flags: FlagCI, Source: "/"},
		{Allow: true, Principal: "grp", Mask: 0b0011, Flags: FlagOI | FlagCI, Source: "/"},
	}
	if got := mustEffective(t, s, "/"); !reflect.DeepEqual(got, wantRoot) {
		t.Fatalf("E(/) = %+v, want %+v", got, wantRoot)
	}

	wantD := []EffectiveEntry{
		{Allow: false, Principal: "grp", Mask: 0b0100, Flags: FlagOI, Source: "d"},
		{Allow: true, Principal: "usr", Mask: 0b0010, Flags: 0, Source: "d"},
		{Allow: false, Principal: "usr", Mask: 0b0010, Flags: FlagCI, Source: "/"},
		{Allow: true, Principal: "grp", Mask: 0b0011, Flags: FlagOI | FlagCI, Source: "/"},
	}
	if got := mustEffective(t, s, "d"); !reflect.DeepEqual(got, wantD) {
		t.Fatalf("E(d) = %+v, want %+v", got, wantD)
	}

	checkResult(t, mustEval(t, s, token, "d", 0b0010), ResultGranted, 1, "d", false, 0b0010)
	checkResult(t, mustEval(t, s, token, "d", 0b0011), ResultGranted, 3, "/", true, 0b0011)
	checkResult(t, mustEval(t, s, token, "d", 0b0111), ResultDeniedHit, 0, "d", false, 0)

	wantF := []EffectiveEntry{
		{Allow: false, Principal: "grp", Mask: 0b0100, Flags: 0, Source: "d"},
		{Allow: true, Principal: "grp", Mask: 0b0011, Flags: 0, Source: "/"},
	}
	if got := mustEffective(t, s, "f"); !reflect.DeepEqual(got, wantF) {
		t.Fatalf("E(f) = %+v, want %+v", got, wantF)
	}
	checkResult(t, mustEval(t, s, token, "f", 0b0110), ResultDeniedHit, 0, "d", true, 0)
	checkResult(t, mustEval(t, s, token, "f", 0b0010), ResultGranted, 1, "/", true, 0b0010)

	wantG := []EffectiveEntry{
		{Allow: false, Principal: "grp", Mask: 0b0100, Flags: FlagOI | FlagIO, Source: "d"},
		{Allow: false, Principal: "usr", Mask: 0b0010, Flags: FlagCI, Source: "/"},
		{Allow: true, Principal: "grp", Mask: 0b0011, Flags: FlagOI | FlagCI, Source: "/"},
	}
	if got := mustEffective(t, s, "g"); !reflect.DeepEqual(got, wantG) {
		t.Fatalf("E(g) = %+v, want %+v", got, wantG)
	}
	checkResult(t, mustEval(t, s, token, "g", 0b0010), ResultDeniedHit, 1, "/", true, 0)
	checkResult(t, mustEval(t, s, token, "g", 0b0001), ResultGranted, 2, "/", true, 0b0001)

	mustSetACL(t, s, "g", []ACE{{Allow: true, Principal: "usr", Mask: 0b0001, Flags: 0}}, true)
	wantG2 := []EffectiveEntry{
		{Allow: true, Principal: "usr", Mask: 0b0001, Flags: 0, Source: "g"},
	}
	if got := mustEffective(t, s, "g"); !reflect.DeepEqual(got, wantG2) {
		t.Fatalf("E(g) after protect = %+v, want %+v", got, wantG2)
	}
	checkResult(t, mustEval(t, s, token, "g", 0b0010), ResultImplicitDeny, -1, "", false, 0)
}
