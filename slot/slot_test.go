package slot

import (
	"errors"
	"testing"

	"ontology"
)

func TestSetPallet(t *testing.T) {
	cases := []struct {
		name string
		sku  ontology.ID
		p    int64
		err  error
	}{
		{"ok", "s1", 10, nil},
		{"empty sku", "", 10, ontology.ErrArgument},
		{"too long sku", ontology.ID(make([]byte, 33)), 10, ontology.ErrArgument},
		{"zero p", "s2", 0, ontology.ErrArgument},
		{"p too big", "s2", 1_000_001, ontology.ErrArgument},
		{"duplicate conflict", "s1", 20, ontology.ErrConflict},
	}
	s := NewStore()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !errors.Is(s.SetPallet(c.sku, c.p), c.err) {
				t.Fatalf("SetPallet(%q,%d) want %v", c.sku, c.p, c.err)
			}
		})
	}
}

func TestPutStock(t *testing.T) {
	s := NewStore()
	if err := s.SetPallet("s1", 10); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name        string
		loc         ontology.ID
		kind        Kind
		sku         ontology.ID
		qty         int64
		err         error
		onHandAfter int64
	}{
		{"new bulk", "B1", Bulk, "s1", 25, nil, 25},
		{"append same", "B1", Bulk, "s1", 5, nil, 30},
		{"new pick", "K1", Pick, "s1", 4, nil, 4},
		{"bad kind", "X", Kind(0), "s1", 4, ontology.ErrArgument, 0},
		{"bad qty", "Y", Bulk, "s1", 0, ontology.ErrArgument, 0},
		{"unknown sku is NotFound", "Z", Bulk, "nope", 4, ontology.ErrNotFound, 0},
		{"conflict sku", "B1", Bulk, "s1x", 1, ontology.ErrConflict, 30},
		{"conflict kind", "B1", Pick, "s1", 1, ontology.ErrConflict, 30},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.PutStock(c.loc, c.kind, c.sku, c.qty)
			if !errors.Is(err, c.err) {
				t.Fatalf("PutStock got %v want %v", err, c.err)
			}
			if c.err == nil {
				if l, _ := s.Get(c.loc); l.OnHand != c.onHandAfter {
					t.Fatalf("onHand=%d want %d", l.OnHand, c.onHandAfter)
				}
			}
		})
	}
	// 锁定后：状态不符优先于冲突。
	s.Reserve("K1", 4)
	s.ShortPick("K1", 1, 4) // 实拣1，锁
	if err := s.PutStock("K1", Pick, "s1", 5); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("locked put got %v want ErrState", err)
	}
	if err := s.PutStock("K1", Bulk, "s9", 5); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("locked+conflict put got %v want ErrState (state before conflict)", err)
	}
	if err := s.Unlock("K1", 7); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	l, _ := s.Get("K1")
	if l.Locked || l.OnHand != 7 || l.Reserved != 0 {
		t.Fatalf("after unlock: %+v", l)
	}
	if err := s.Unlock("K1", 7); !errors.Is(err, ontology.ErrState) {
		t.Fatalf("unlock unlocked got %v want ErrState", err)
	}
	if err := s.Unlock("nope", 7); !errors.Is(err, ontology.ErrNotFound) {
		t.Fatalf("unlock missing got %v want ErrNotFound", err)
	}
	if err := s.Unlock("B1", -1); !errors.Is(err, ontology.ErrArgument) {
		t.Fatalf("unlock bad counted got non-ErrArgument")
	}
}

func TestAvailableAndOrdering(t *testing.T) {
	s := NewStore()
	if err := s.SetPallet("s1", 10); err != nil {
		t.Fatal(err)
	}
	for _, loc := range []ontology.ID{"B3", "B1", "B2"} {
		if err := s.PutStock(loc, Bulk, "s1", 10); err != nil {
			t.Fatal(err)
		}
	}
	ids := s.BulkIDs("s1")
	want := []ontology.ID{"B1", "B2", "B3"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("BulkIDs=%v want %v", ids, want)
		}
	}
	s.Reserve("B1", 3)
	l, _ := s.Get("B1")
	if l.Available() != 7 {
		t.Fatalf("avail=%d want 7", l.Available())
	}
	s.ShortPick("B1", 7, 3)
	if l.Available() != 0 {
		t.Fatalf("locked avail=%d want 0", l.Available())
	}
}
