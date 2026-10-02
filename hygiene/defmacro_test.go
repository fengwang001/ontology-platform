package hygiene_test

import (
	"fmt"
	"testing"
)
import "ontology/hygiene"

func TestDefMacroErrorOrderAndLimits(t *testing.T) {
	session := hygiene.NewSession()
	if err := session.DefMacro("lam", nil, mustPublicTerm(t, "x")); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("reserved name: %v", err)
	}
	if err := session.DefMacro("ok", []string{"quote"}, mustPublicTerm(t, "x")); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("reserved parameter: %v", err)
	}
	if err := session.DefMacro("ok", []string{"a", "a"}, mustPublicTerm(t, "x")); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("duplicate parameter: %v", err)
	}
	if err := session.DefMacro("ok", []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}, mustPublicTerm(t, "x")); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("parameter limit: %v", err)
	}
	invalidList := &hygiene.Term{Kind: hygiene.List, Items: []*hygiene.Term{
		mustPublicTerm(t, "a"), mustPublicTerm(t, "b"), mustPublicTerm(t, "c"),
		mustPublicTerm(t, "d"), mustPublicTerm(t, "e"), mustPublicTerm(t, "f"),
		mustPublicTerm(t, "g"), mustPublicTerm(t, "h"), mustPublicTerm(t, "i"),
	}}
	if err := session.DefMacro("ok", nil, invalidList); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("invalid template list: %v", err)
	}
	if err := session.DefMacro("ok", nil, &hygiene.Term{Kind: 42}); err.(hygiene.DefError).Kind != hygiene.DefInvalid {
		t.Fatalf("invalid template kind: %v", err)
	}
	mustPublicDef(t, session, "dup", nil, mustPublicTerm(t, "x"))
	if err := session.DefMacro("dup", nil, mustPublicTerm(t, "y")); err.(hygiene.DefError).Kind != hygiene.DefDuplicate {
		t.Fatalf("duplicate macro: %v", err)
	}

	for index := range 99 {
		name := fmt.Sprintf("n%02d", index)
		mustPublicDef(t, session, name, nil, mustPublicTerm(t, "x"))
	}
	if err := session.DefMacro("one-too-many", nil, mustPublicTerm(t, "x")); err.(hygiene.DefError).Kind != hygiene.DefLimit {
		t.Fatalf("macro table limit: %v", err)
	}
}
