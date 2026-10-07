package ontology

import "testing"

const (
	linkParent = "Parent"
	typeChild  = "Child"
)

func mustRule(t *testing.T, st *Store, id string, eff int64, retroactive bool, req map[string][]string) {
	t.Helper()
	seq := uint64(0)
	if h := st.HeadRuleVersion(); h != nil {
		seq = h.EffectiveFromSeq
	}
	err := st.AdjustRule(RuleVersion{
		ID:               id,
		EffectiveFrom:    eff,
		EffectiveFromSeq: seq + 1,
		Retroactive:      retroactive,
		Requirement:      req,
	})
	if err != nil {
		t.Fatalf("AdjustRule(%s): %v", id, err)
	}
}

func mustDetermine(t *testing.T, st *Store, id string, at int64) *DetermineResult {
	t.Helper()
	res, err := st.Determine(DetermineRequest{ObjectID: id, AtTime: at, SkipCrossCheck: true})
	if err != nil {
		t.Fatalf("Determine(%s,%d): %v", id, at, err)
	}
	return res
}

func errKind(t *testing.T, err error) DeterminationErrorKind {
	t.Helper()
	de, ok := AsDeterminationError(err)
	if !ok {
		t.Fatalf("not a DeterminationError: %v", err)
	}
	return de.Kind
}

// buildChildStream creates: Parent type declared, parent p and child c
// created, then a single required Parent edge p->c established at est
// and revoked at rev.
func buildChildStream(st *Store, est, rev int64) {
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "ParentObj"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	st.Append(EventInput{Kind: EvLinkEstablished, Time: est, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	if rev > 0 {
		st.Append(EventInput{Kind: EvLinkRevoked, Time: rev, ObjectID: "p", PeerID: "c", TypeID: linkParent})
	}
}
