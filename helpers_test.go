package ontology

import "testing"

// must registers object types, failing the test on error.
func mustTypes(t *testing.T, s *Store, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := s.RegisterObjectType(n); err != nil {
			t.Fatalf("RegisterObjectType(%q): %v", n, err)
		}
	}
}

// mustLinkType registers a link type, failing the test on error.
func mustLinkType(t *testing.T, s *Store, lt LinkType) {
	t.Helper()
	if err := s.RegisterLinkType(lt); err != nil {
		t.Fatalf("RegisterLinkType(%q): %v", lt.Name, err)
	}
}

// mustObjects adds objects of one type, failing the test on error.
func mustObjects(t *testing.T, s *Store, objectType string, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := s.AddObject(objectType, id); err != nil {
			t.Fatalf("AddObject(%q, %q): %v", objectType, id, err)
		}
	}
}

// mustLink creates a link, failing the test on error.
func mustLink(t *testing.T, s *Store, lt, src, dst string) {
	t.Helper()
	if err := s.CreateLink(lt, src, dst); err != nil {
		t.Fatalf("CreateLink(%q, %q, %q): %v", lt, src, dst, err)
	}
}

// newGraphStore returns a store with Person and Company types plus a
// MANY_TO_MANY/SET_NULL "knows" link type, a common base for tests.
func newGraphStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustTypes(t, s, "Person", "Company")
	mustLinkType(t, s, LinkType{
		Name: "knows", Source: "Person", Target: "Person",
		Cardinality: ManyToMany, OnDelete: SetNull,
	})
	return s
}

func equalStrings(a, b []string) bool {
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
