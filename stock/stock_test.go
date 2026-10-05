package stock_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/stock"
)

func TestAddItemBoundaries(t *testing.T) {
	cases := []struct {
		name            string
		item            string
		onHand, ss      int64
		lead            int
		lotMin, lotMult int64
		wantErr         error
	}{
		{"ok-zero", "A", 0, 0, 0, 1, 1, nil},
		{"ok-max", strings.Repeat("x", 32), 1_000_000_000, 1_000_000_000, 52, 1_000_000, 1_000_000, nil},
		{"empty-name", "", 0, 0, 0, 1, 1, stock.ErrInvalid},
		{"long-name", strings.Repeat("y", 33), 0, 0, 0, 1, 1, stock.ErrInvalid},
		{"onhand-negative", "B", -1, 0, 0, 1, 1, stock.ErrInvalid},
		{"onhand-too-big", "B", 1_000_000_001, 0, 0, 1, 1, stock.ErrInvalid},
		{"ss-negative", "B", 0, -1, 0, 1, 1, stock.ErrInvalid},
		{"ss-too-big", "B", 0, 1_000_000_001, 0, 1, 1, stock.ErrInvalid},
		{"lead-negative", "B", 0, 0, -1, 1, 1, stock.ErrInvalid},
		{"lead-too-big", "B", 0, 0, 53, 1, 1, stock.ErrInvalid},
		{"lotmin-zero", "B", 0, 0, 0, 0, 1, stock.ErrInvalid},
		{"lotmin-too-big", "B", 0, 0, 0, 1_000_001, 1, stock.ErrInvalid},
		{"lotmult-zero", "B", 0, 0, 0, 1, 0, stock.ErrInvalid},
		{"lotmult-too-big", "B", 0, 0, 0, 1, 1_000_001, stock.ErrInvalid},
	}
	for _, c := range cases {
		r := stock.New()
		err := r.AddItem(c.item, c.onHand, c.ss, c.lead, c.lotMin, c.lotMult)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: got %v want %v", c.name, err, c.wantErr)
		}
		if c.wantErr != nil && r.Version() != 0 {
			t.Errorf("%s: rejected AddItem bumped version", c.name)
		}
	}
}

func TestAddItemConflictAndVersion(t *testing.T) {
	r := stock.New()
	if err := r.AddItem("A", 1, 2, 3, 4, 5); err != nil {
		t.Fatal(err)
	}
	if err := r.AddItem("A", 0, 0, 0, 1, 1); !errors.Is(err, stock.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	if r.Version() != 1 {
		t.Fatalf("rejected duplicate bumped version: %d", r.Version())
	}
	it, ok := r.Get("A")
	if !ok || it.OnHand != 1 || it.SS != 2 || it.Lead != 3 || it.LotMin != 4 || it.LotMult != 5 {
		t.Fatalf("conflict overwrote item: %+v", it)
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatal("unexpected hit")
	}
}

func TestAllSorted(t *testing.T) {
	r := stock.New()
	for _, n := range []string{"b", "A", "10", "a"} {
		if err := r.AddItem(n, 0, 0, 0, 1, 1); err != nil {
			t.Fatal(err)
		}
	}
	all := r.All()
	want := []string{"10", "A", "a", "b"}
	for i, it := range all {
		if it.Name != want[i] {
			t.Fatalf("All()[%d]=%q want %q", i, it.Name, want[i])
		}
	}
}

func TestLot(t *testing.T) {
	cases := []struct {
		net, lotMin, lotMult, want int64
	}{
		{1, 1, 1, 1},
		{5, 10, 4, 10},   // 倍数取整 8 低于 lotMin，lotMin 占优
		{25, 20, 20, 40}, // ceil(25/20)*20
		{40, 20, 20, 40}, // 恰为倍数
		{41, 20, 20, 60},
		{99, 1, 100, 100},
		{100, 1, 100, 100},
		{101, 1, 100, 200},
	}
	for _, c := range cases {
		it := stock.Item{LotMin: c.lotMin, LotMult: c.lotMult}
		if got := stock.Lot(it, c.net); got != c.want {
			t.Errorf("Lot(net=%d, min=%d, mult=%d)=%d want %d",
				c.net, c.lotMin, c.lotMult, got, c.want)
		}
	}
}
