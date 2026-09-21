package mux

import "testing"

// Regression: Deliver used to hand the caller's slice to the waiter
// without copying, so post-Deliver mutation corrupted the payload.
func TestDeliverCopiesPayload(t *testing.T) {
	m := New(newFakeClock().Now)
	ch, err := m.Register("iso")
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("keep")
	m.Deliver("iso", payload)
	for i := range payload {
		payload[i] = 'z'
	}
	if got := <-ch; string(got) != "keep" {
		t.Fatalf("got %q, want %q: Deliver must copy the payload", got, "keep")
	}
}

// Regression: Deliver had the orphan/late branches swapped, counting
// never-registered ids as late and previously-seen ids as orphans.
func TestOrphanAndLateNotSwapped(t *testing.T) {
	m := New(newFakeClock().Now)
	m.Deliver("never-seen", []byte("x"))
	if s := m.Stats(); s.Orphans != 1 || s.Late != 0 {
		t.Fatalf("unknown id: stats %+v, want Orphans=1 Late=0", s)
	}
	if _, err := m.Register("gone"); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m.Deliver("gone", []byte("x"))
	if s := m.Stats(); s.Late != 1 || s.Orphans != 1 {
		t.Fatalf("closed id: stats %+v, want Late=1 Orphans=1", s)
	}
}
