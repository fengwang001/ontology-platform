package mux

import "testing"

// Regression: Deliver's not-pending branch had the two counters
// swapped, so a never-registered id bumped Late and a previously
// registered id bumped Orphans. The classification key is whether the
// id was ever registered (present in seen).
func TestOrphanAndLateNotSwapped(t *testing.T) {
	m := New(newFakeClock().Now)
	ch, err := m.Register("known")
	if err != nil {
		t.Fatal(err)
	}
	m.Deliver("known", []byte("first"))
	<-ch
	m.Deliver("known", []byte("again"))  // registered before -> Late
	m.Deliver("stranger", []byte("boo")) // never registered -> Orphans
	s := m.Stats()
	if s.Orphans != 1 || s.Late != 1 {
		t.Fatalf("stats %+v, want Orphans=1 Late=1", s)
	}
}
