package ontology

import (
	"path/filepath"
	"testing"
)

func TestSnapshotRoundTrip(t *testing.T) {
	s := newCardinalityStore(t)
	mustLink(t, s, "spouse", "p1", "p2")
	mustLink(t, s, "employs", "c1", "p3")
	path := filepath.Join(t.TempDir(), "snap.json")
	if err := s.SaveToFile(path); err != nil {
		t.Fatalf("SaveToFile: %v", err)
	}
	loaded, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile: %v", err)
	}
	if !loaded.HasObject("p1") || !loaded.HasObject("c2") {
		t.Fatal("objects missing after load")
	}
	if !loaded.HasLink("spouse", "p1", "p2") || !loaded.HasLink("employs", "c1", "p3") {
		t.Fatal("links missing after load")
	}
	lt, ok := loaded.LinkTypeOf("spouse")
	if !ok || lt.Cardinality != OneToOne {
		t.Fatalf("link type metadata lost: %+v", lt)
	}
	if err := loaded.CheckInvariant(); err != nil {
		t.Fatalf("CheckInvariant: %v", err)
	}
	// Cardinality rules still enforced on the loaded store.
	if err := loaded.CreateLink("spouse", "p3", "p2"); !IsViolation(err, ViolationOneToOne) {
		t.Fatalf("expected ONE_TO_ONE violation after load, got %v", err)
	}
}

func TestLoadFromMissingFile(t *testing.T) {
	if _, err := LoadFromFile(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
