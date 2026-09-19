package ontology

import "testing"

// newCardinalityStore builds Person/Company with:
//   - "spouse": ONE_TO_ONE Person->Person
//   - "employs": ONE_TO_MANY Company->Person
func newCardinalityStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustTypes(t, s, "Person", "Company")
	mustLinkType(t, s, LinkType{
		Name: "spouse", Source: "Person", Target: "Person",
		Cardinality: OneToOne, OnDelete: SetNull,
	})
	mustLinkType(t, s, LinkType{
		Name: "employs", Source: "Company", Target: "Person",
		Cardinality: OneToMany, OnDelete: SetNull,
	})
	mustObjects(t, s, "Person", "p1", "p2", "p3")
	mustObjects(t, s, "Company", "c1", "c2")
	return s
}

func TestOneToOneSourceOccupied(t *testing.T) {
	s := newCardinalityStore(t)
	mustLink(t, s, "spouse", "p1", "p2")
	err := s.CreateLink("spouse", "p1", "p3")
	if !IsViolation(err, ViolationOneToOne) {
		t.Fatalf("want ViolationOneToOne, got %v", err)
	}
}

func TestOneToOneTargetOccupied(t *testing.T) {
	s := newCardinalityStore(t)
	mustLink(t, s, "spouse", "p1", "p2")
	err := s.CreateLink("spouse", "p3", "p2")
	if !IsViolation(err, ViolationOneToOne) {
		t.Fatalf("want ViolationOneToOne, got %v", err)
	}
}

func TestOneToManyTargetOwned(t *testing.T) {
	s := newCardinalityStore(t)
	mustLink(t, s, "employs", "c1", "p1")
	// Same source may add more targets.
	mustLink(t, s, "employs", "c1", "p2")
	// Another source may not steal an owned target.
	err := s.CreateLink("employs", "c2", "p1")
	if !IsViolation(err, ViolationOneToMany) {
		t.Fatalf("want ViolationOneToMany, got %v", err)
	}
}

func TestManyToManyUnrestricted(t *testing.T) {
	s := newGraphStore(t)
	mustObjects(t, s, "Person", "p1", "p2", "p3")
	mustLink(t, s, "knows", "p1", "p2")
	mustLink(t, s, "knows", "p1", "p3")
	mustLink(t, s, "knows", "p2", "p2")
	mustLink(t, s, "knows", "p3", "p2")
}

// TestViolationKindsDistinguishable proves the five categories are
// separately detectable and mutually exclusive.
func TestViolationKindsDistinguishable(t *testing.T) {
	setup := func() *Store {
		s := newCardinalityStore(t)
		mustLink(t, s, "spouse", "p1", "p2")
		mustLink(t, s, "employs", "c1", "p1")
		return s
	}
	cases := []struct {
		name string
		run  func(s *Store) error
		want ViolationKind
	}{
		{"one_to_one", func(s *Store) error {
			return s.CreateLink("spouse", "p3", "p2")
		}, ViolationOneToOne},
		{"one_to_many", func(s *Store) error {
			return s.CreateLink("employs", "c2", "p1")
		}, ViolationOneToMany},
		{"endpoint_type", func(s *Store) error {
			return s.CreateLink("employs", "p3", "p2")
		}, ViolationEndpointType},
		{"endpoint_not_found", func(s *Store) error {
			return s.CreateLink("employs", "c2", "ghost")
		}, ViolationEndpointNotFound},
		{"duplicate", func(s *Store) error {
			return s.CreateLink("spouse", "p1", "p2")
		}, ViolationDuplicateLink},
	}
	all := []ViolationKind{ViolationOneToOne, ViolationOneToMany,
		ViolationEndpointType, ViolationEndpointNotFound, ViolationDuplicateLink}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(setup())
			if err == nil {
				t.Fatal("expected error")
			}
			for _, k := range all {
				got := IsViolation(err, k)
				if k == tc.want && !got {
					t.Errorf("IsViolation(%v) = false, want true", k)
				}
				if k != tc.want && got {
					t.Errorf("IsViolation(%v) = true, want false", k)
				}
			}
			le, ok := err.(*LinkError)
			if !ok {
				t.Fatalf("expected *LinkError, got %T", err)
			}
			if le.LinkType == "" || le.Source == "" || le.Target == "" {
				t.Errorf("error missing context: %+v", le)
			}
		})
	}
}
