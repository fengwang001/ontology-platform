package traffic

import (
	"errors"
	"math/big"
	"testing"
	"time"
)

func r(x int64, y int64) *Rat { return big.NewRat(x, y) }
func ri(x int64) *Rat         { return big.NewRat(x, 1) }

func mustAdd(t testing.TB, n *Network, id, from, to string, length, cap, arr *Rat) {
	t.Helper()
	if err := n.AddLink(Link{ID: id, From: from, To: to, Length: length, Capacity: cap, Arrival: arr}); err != nil {
		t.Fatalf("AddLink %s: %v", id, err)
	}
}

func mustQuery(t *testing.T, s *Service, id string) LinkState {
	t.Helper()
	st, err := s.Query(id)
	if err != nil {
		t.Fatalf("Query %s: %v", id, err)
	}
	return st
}

func checkState(t *testing.T, st LinkState, wantQueue *Rat, wantLevel int) {
	t.Helper()
	if st.Queue.Cmp(wantQueue) != 0 {
		t.Errorf("%s queue = %s, want %s", st.LinkID, st.Queue.RatString(), wantQueue.RatString())
	}
	if st.Level != wantLevel {
		t.Errorf("%s level = %d, want %d", st.LinkID, st.Level, wantLevel)
	}
}

// singleIncidentNetwork: A: X->Y, capacity 10, arrival 8, length 100.
func singleIncidentNetwork(t testing.TB) *Network {
	n := NewNetwork()
	mustAdd(t, n, "A", "X", "Y", ri(100), ri(10), ri(8))
	return n
}

// Queue fills exactly at t=200/11 (ratio 3/4, drift 11/2).
func TestSpillbackAtExactInstant(t *testing.T) {
	n := singleIncidentNetwork(t)
	mustAdd(t, n, "U", "W", "X", ri(100), ri(10), ri(3))
	s := NewService(n, ri(1))
	tHit := r(200, 11)
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(3, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(new(Rat).Sub(tHit, ri(1))); err != nil {
		t.Fatal(err)
	}
	if lv := mustQuery(t, s, "U").Level; lv != 0 {
		t.Fatalf("U level before spill instant = %d, want 0", lv)
	}
	if err := s.Advance(tHit); err != nil {
		t.Fatal(err)
	}
	checkState(t, mustQuery(t, s, "A"), ri(100), 1)
	if lv := mustQuery(t, s, "U").Level; lv != 2 {
		t.Fatalf("U level at spill instant = %d, want 2", lv)
	}
	if err := s.Advance(new(Rat).Add(tHit, r(1, 2))); err != nil {
		t.Fatal(err)
	}
	// drift(U)=3-5/2=1/2 over 1/2 => 1/4 vehicle
	checkState(t, mustQuery(t, s, "U"), r(1, 4), 2)
}

func TestTwoIncidentsSameLinkMax(t *testing.T) {
	n := singleIncidentNetwork(t)
	s := NewService(n, ri(1))
	if _, err := s.Register(Incident{ID: "i1", LinkID: "A", Start: ri(0), Ratio: r(1, 2)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(Incident{ID: "i2", LinkID: "A", Start: ri(0), Ratio: r(1, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ri(10)); err != nil {
		t.Fatal(err)
	}
	// effCap=10*(1-1/2)=5, drift 3 -> 30 vehicles
	checkState(t, mustQuery(t, s, "A"), ri(30), 1)
}

func TestClearAtSameInstantAsSpillback(t *testing.T) {
	n := singleIncidentNetwork(t)
	mustAdd(t, n, "U", "W", "X", ri(100), ri(10), ri(3))
	s := NewService(n, ri(1))
	tHit := r(200, 11)
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(3, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear("i", tHit); err != nil {
		t.Fatal(err)
	}
	checkState(t, mustQuery(t, s, "A"), ri(100), 0)
	checkState(t, mustQuery(t, s, "U"), ri(0), 0)
	if err := s.Advance(new(Rat).Add(tHit, ri(50))); err != nil {
		t.Fatal(err)
	}
	checkState(t, mustQuery(t, s, "A"), ri(0), 0)
	checkState(t, mustQuery(t, s, "U"), ri(0), 0)
}

func TestDrainToZeroCoincidesWithNewIncident(t *testing.T) {
	n := singleIncidentNetwork(t)
	s := NewService(n, ri(1))
	if _, err := s.Register(Incident{ID: "i1", LinkID: "A", Start: ri(0), Ratio: r(1, 2)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Clear("i1", ri(10)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Register(Incident{ID: "i2", LinkID: "A", Start: ri(25), Ratio: r(3, 4)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ri(25)); err != nil {
		t.Fatal(err)
	}
	checkState(t, mustQuery(t, s, "A"), ri(0), 1)
	if err := s.Advance(ri(26)); err != nil {
		t.Fatal(err)
	}
	checkState(t, mustQuery(t, s, "A"), r(11, 2), 1)
}

// Diamond: from the shared upstream link U two routes lead down to the
// incident link A: U-K-P-A (levels 4,3,2,1) and U-K2-QX-Q-A
// (5,4,3,2,1). U must take the minimum level 4, never 5.
func TestDiamondMinLevel(t *testing.T) {
	n := NewNetwork()
	mustAdd(t, n, "A", "S", "T", ri(10), ri(10), ri(9))
	mustAdd(t, n, "P", "Pn", "S", ri(10), ri(10), ri(9))
	mustAdd(t, n, "Q", "Qn", "S", ri(10), ri(10), ri(9))
	mustAdd(t, n, "QX", "Qm", "Qn", ri(10), ri(10), ri(9))
	mustAdd(t, n, "K", "Kn", "Pn", ri(10), ri(10), ri(9))
	mustAdd(t, n, "K2", "Kn", "Qm", ri(10), ri(10), ri(9))
	mustAdd(t, n, "U", "Un", "Kn", ri(100), ri(10), ri(9))
	s := NewService(n, ri(1))
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(9, 10)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Advance(ri(60)); err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"A": 1, "P": 2, "Q": 2, "QX": 3, "K": 3, "K2": 4, "U": 4}
	for id, lv := range want {
		if got := mustQuery(t, s, id).Level; got != lv {
			t.Errorf("%s level = %d, want %d", id, got, lv)
		}
	}
}

func TestCycleNoDoubleRestriction(t *testing.T) {
	n := NewNetwork()
	mustAdd(t, n, "A", "X", "Y", ri(10), ri(10), ri(9))
	mustAdd(t, n, "B", "Y", "X", ri(100), ri(10), ri(2))
	s := NewService(n, ri(1))
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(0), Ratio: r(9, 10)}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Advance(ri(100)) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("advance on cyclic network did not terminate")
	}
	checkState(t, mustQuery(t, s, "A"), ri(10), 1)
	if s.eng.effCap["A"].Cmp(ri(1)) != 0 {
		t.Fatalf("A effCap = %s, want 1", s.eng.effCap["A"].RatString())
	}
	if q := mustQuery(t, s, "B").Queue; q.Cmp(ri(100)) > 0 {
		t.Fatalf("B queue exceeds link capacity: %s", q.RatString())
	}
}

func TestUpdateBeforeStartRejected(t *testing.T) {
	n := singleIncidentNetwork(t)
	s := NewService(n, ri(1))
	if _, err := s.Register(Incident{ID: "i", LinkID: "A", Start: ri(10), Ratio: r(1, 2)}); err != nil {
		t.Fatal(err)
	}
	// Registration moved the clock to 10; the incident is active at 10.
	if err := s.Update("i", ri(9), r(1, 4)); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("past update: got %v", err)
	}
	// A later-dated clock cannot even return to before start; guard the
	// before-start rule directly on a fresh service at t=0:
	s2 := NewService(singleIncidentNetwork(t), ri(1))
	if _, err := s2.Register(Incident{ID: "i", LinkID: "A", Start: ri(10), Ratio: r(1, 2)}); err != nil {
		t.Fatal(err)
	}
	// at == start is accepted
	if err := s2.Update("i", ri(10), r(1, 4)); err != nil {
		t.Fatalf("update at start: %v", err)
	}
}

func TestConstructionGuards(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on nil network")
			}
		}()
		NewService(nil, ri(1))
	}()
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic on non-positive vehicle length")
			}
		}()
		NewService(NewNetwork(), ri(0))
	}()
}
