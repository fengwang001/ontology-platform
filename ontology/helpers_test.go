package ontology

import "testing"

func mustType(t *testing.T, g *Gateway, typeID string, attrs []Attribute, policy WritePolicy) {
	t.Helper()
	if _, err := g.CreateType(typeID, attrs, policy); err != nil {
		t.Fatalf("create type: %v", err)
	}
}

func mustGrant(t *testing.T, g *Gateway, typeID, subject, attr string, op Op, from, to int) {
	t.Helper()
	if err := g.Grant(typeID, subject, attr, op, from, to); err != nil {
		t.Fatalf("grant: %v", err)
	}
}

func mustEvolve(t *testing.T, g *Gateway, typeID string, changes []AttrChange) int {
	t.Helper()
	v, err := g.Evolve(typeID, changes, "")
	if err != nil {
		t.Fatalf("evolve: %v", err)
	}
	return v
}

func currentVer(t *testing.T, g *Gateway, typeID string) int {
	t.Helper()
	v, err := g.CurrentVersion(typeID)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
