package ontology

import (
	"errors"
	"testing"
)

func findRecord(t *testing.T, result SettleResult, id int64) ReservationSettlement {
	t.Helper()
	for _, record := range result.Records {
		if record.ID == id {
			return record
		}
	}
	t.Fatalf("booking %d not in settle result: %+v", id, result)
	return ReservationSettlement{}
}

func bookMany(t *testing.T, b *CapacityBook, books [][5]int64) {
	t.Helper()
	for _, input := range books {
		if err := b.Book(input[0], input[1], input[2], input[3], int(input[4])); err != nil {
			t.Fatalf("Book(%v): %v", input, err)
		}
	}
}

func TestSpecExample(t *testing.T) {
	b, err := NewCapacityBook(10, 2, 3000, 50)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Book(1, 10, 1, 6, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Book(2, 20, 1, 4, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Book(3, 30, 1, 1, 0); !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("error = %v, want ErrCapacityLimit", err)
	}

	first, err := b.Settle(1, []Arrival{{ID: 1, Amount: 3}, {ID: 2, Amount: 4}})
	if err != nil {
		t.Fatal(err)
	}
	if b.CurrentOversell() != 3000 || b.CurrentLimit() != 13 {
		t.Fatalf("O/limit = %d/%d, want 3000/13", b.CurrentOversell(), b.CurrentLimit())
	}
	if first.BumpedTotal != 0 || first.TotalCompensation != 0 {
		t.Fatalf("first settle = %+v, want no bump", first)
	}

	if err := b.Book(4, 30, 2, 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := b.Book(5, 10, 2, 6, 0); err != nil {
		t.Fatal(err)
	}
	if err := b.Book(6, 10, 2, 1, 0); !errors.Is(err, ErrCapacityLimit) {
		t.Fatalf("error = %v, want capacity", err)
	}

	second, err := b.Settle(2, []Arrival{{ID: 4, Amount: 7}, {ID: 5, Amount: 6}})
	if err != nil {
		t.Fatal(err)
	}
	d := findRecord(t, second, 4)
	e := findRecord(t, second, 5)
	if d.Served != 4 || d.Bumped != 3 || d.Compensation != 150 {
		t.Fatalf("d = %+v, want served 4 bumped 3 compensation 150", d)
	}
	if e.Served != 6 || e.Bumped != 0 || e.Compensation != 0 {
		t.Fatalf("e = %+v, want unchanged", e)
	}
	if second.TotalCompensation != 150 || b.BumpCount(30) != 1 {
		t.Fatalf("second compensation = %d, k(Z)=%d", second.TotalCompensation, b.BumpCount(30))
	}
	if b.CurrentOversell() != 1304 || b.CurrentLimit() != 11 {
		t.Fatalf("O/limit = %d/%d, want 1304/11", b.CurrentOversell(), b.CurrentLimit())
	}

	if err := b.Book(7, 30, 3, 11, 1); err != nil {
		t.Fatal(err)
	}
	third, err := b.Settle(3, []Arrival{{ID: 7, Amount: 11}})
	if err != nil {
		t.Fatal(err)
	}
	g := findRecord(t, third, 7)
	if g.Served != 10 || g.Bumped != 1 || g.Compensation != 100 {
		t.Fatalf("g = %+v, want served 10 bumped 1 compensation 100", g)
	}
}
