package ontology

import (
	"errors"
	"testing"
)

// TestValidateRequiredReportsAll proves requiredness is checked only at
// commit time and that every missing relation is reported in one error.
func TestValidateRequiredReportsAll(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "Person", "Company")
	mustLinkType(t, s, LinkType{
		Name:           "worksAt",
		Source:         "Person",
		Target:         "Company",
		Cardinality:    ManyToMany,
		SourceRequired: true,
		TargetRequired: true,
		OnDelete:       SetNull,
	})
	mustObjects(t, s, "Person", "p1", "p2", "p3")
	mustObjects(t, s, "Company", "c1", "c2")
	// Only p1-c1 exists; p2, p3 miss the source side, c2 misses target side.
	mustLink(t, s, "worksAt", "p1", "c1")
	err := s.ValidateRequired()
	var re *RequiredError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RequiredError, got %v", err)
	}
	if len(re.Missing) != 3 {
		t.Fatalf("expected 3 missing relations, got %d: %v", len(re.Missing), re.Missing)
	}
	want := map[MissingRequired]bool{
		{ObjectID: "p2", LinkType: "worksAt", Side: "source"}: true,
		{ObjectID: "p3", LinkType: "worksAt", Side: "source"}: true,
		{ObjectID: "c2", LinkType: "worksAt", Side: "target"}: true,
	}
	for _, m := range re.Missing {
		if !want[m] {
			t.Errorf("unexpected missing entry: %+v", m)
		}
		delete(want, m)
	}
	if len(want) > 0 {
		t.Errorf("missing entries not reported: %v", want)
	}
}

func TestValidateRequiredSatisfied(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "Person", "Company")
	mustLinkType(t, s, LinkType{
		Name:           "worksAt",
		Source:         "Person",
		Target:         "Company",
		Cardinality:    ManyToMany,
		SourceRequired: true,
		TargetRequired: true,
		OnDelete:       SetNull,
	})
	mustObjects(t, s, "Person", "p1")
	mustObjects(t, s, "Company", "c1")
	mustLink(t, s, "worksAt", "p1", "c1")
	if err := s.ValidateRequired(); err != nil {
		t.Fatalf("ValidateRequired: %v", err)
	}
}

// TestRequiredNotCheckedAtCreateLink: creating links must not enforce
// requiredness; only ValidateRequired reports deficits.
func TestRequiredNotCheckedAtCreateLink(t *testing.T) {
	s := NewStore()
	mustTypes(t, s, "Person", "Company")
	mustLinkType(t, s, LinkType{
		Name:           "worksAt",
		Source:         "Person",
		Target:         "Company",
		Cardinality:    ManyToMany,
		SourceRequired: true,
		OnDelete:       SetNull,
	})
	mustObjects(t, s, "Person", "p1")
	mustObjects(t, s, "Company", "c1")
	if err := s.CreateLink("worksAt", "p1", "c1"); err != nil {
		t.Fatalf("CreateLink: %v", err)
	}
	if err := s.ValidateRequired(); err != nil {
		t.Fatalf("ValidateRequired: %v", err)
	}
}
