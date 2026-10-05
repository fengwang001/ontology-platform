package lotstock

import (
	"errors"
	"testing"
)

func TestAddAndValidation(t *testing.T) {
	bad := []Lot{
		{Lot: "", Series: "M", Exp: 10, Qty: 1},
		{Lot: "L", Series: "", Exp: 10, Qty: 1},
		{Lot: "L", Series: "M", Exp: -1, Qty: 1},
		{Lot: "L", Series: "M", Exp: 1_000_001, Qty: 1},
		{Lot: "L", Series: "M", Exp: 10, Qty: -1},
	}
	for _, l := range bad {
		if err := New().Add(l); err != ErrInvalidLot {
			t.Fatalf("%+v: want ErrInvalidLot got %v", l, err)
		}
	}
	s := New()
	if err := s.Add(Lot{Lot: "L", Series: "M", Exp: 10, Qty: 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Lot{Lot: "L", Series: "M", Exp: 11, Qty: 2}); !errors.Is(err, ErrLotExists) {
		t.Fatalf("dup add: want ErrLotExists got %v", err)
	}
	if err := s.Quarantine("nope", true); !errors.Is(err, ErrNoLot) {
		t.Fatalf("quarantine missing: want ErrNoLot got %v", err)
	}
}

func TestPickFEFOExpiryQuarantine(t *testing.T) {
	s := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Add(Lot{Lot: "late", Series: "M", Exp: 100, Qty: 5}))
	must(s.Add(Lot{Lot: "early", Series: "M", Exp: 50, Qty: 5}))
	must(s.Add(Lot{Lot: "otherSeries", Series: "V", Exp: 10, Qty: 5}))
	must(s.Add(Lot{Lot: "quar", Series: "M", Exp: 20, Qty: 5, Quar: true}))
	must(s.Add(Lot{Lot: "z1", Series: "M", Exp: 50, Qty: 5}))

	// FEFO: earliest exp wins.
	if lot, err := s.Pick("M", 0); err != nil || lot != "early" {
		t.Fatalf("pick1=%q err=%v want early", lot, err)
	}
	if s.Qty("early") != 4 {
		t.Fatalf("qty early=%d want 4", s.Qty("early"))
	}
	// Tie on exp=50 between early(4 left) and z1: still earliest exp;
	// byte order tie break prefers "early" over "z1".
	if lot, _ := s.Pick("M", 0); lot != "early" {
		t.Fatalf("pick2=%q want early", lot)
	}
	// Exhaust early (2 left): pick4 removes 2nd, pick5 3rd -> now z1.
	if lot, _ := s.Pick("M", 0); lot != "early" {
		t.Fatalf("pick3=%q want early", lot)
	}
	if lot, _ := s.Pick("M", 0); lot != "early" {
		t.Fatalf("pick4=%q want early", lot)
	}
	if lot, _ := s.Pick("M", 0); lot != "early" {
		t.Fatalf("pick5a=%q want early", lot)
	}
	if s.Qty("early") != 0 {
		t.Fatalf("early qty=%d want 0", s.Qty("early"))
	}
	if lot, _ := s.Pick("M", 0); lot != "z1" {
		t.Fatalf("pick6=%q want z1", lot)
	}

	// now == exp means expired: late exp=100 unusable at 100.
	if _, err := s.Pick("M", 99); err != nil {
		t.Fatalf("at 99 late should be usable: %v", err)
	}
	// quarantine release moves "quar" ahead (exp 20).
	must(s.Quarantine("quar", false))
	if lot, _ := s.Pick("M", 0); lot != "quar" {
		t.Fatalf("released quar not chosen: %q", lot)
	}
	must(s.Quarantine("quar", true))

	// Drain everything usable for M and verify ErrNoStock.
	for _, id := range []string{"z1", "late"} {
		for s.Qty(id) > 0 {
			if _, err := s.Pick("M", 0); err != nil {
				t.Fatalf("draining %s: %v", id, err)
			}
		}
	}
	if _, err := s.Pick("M", 100); !errors.Is(err, ErrNoStock) {
		t.Fatalf("all expired/drained at 100: want ErrNoStock got %v", err)
	}
	if _, err := s.Pick("ZZZ", 0); !errors.Is(err, ErrNoStock) {
		t.Fatalf("unknown series: want ErrNoStock got %v", err)
	}
}

func TestPickTieByteOrder(t *testing.T) {
	s := New()
	if err := s.Add(Lot{Lot: "b", Series: "M", Exp: 30, Qty: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Lot{Lot: "a", Series: "M", Exp: 30, Qty: 1}); err != nil {
		t.Fatal(err)
	}
	if lot, _ := s.Pick("M", 0); lot != "a" {
		t.Fatalf("tie break=%q want a", lot)
	}
}
