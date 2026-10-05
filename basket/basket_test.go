package basket

import (
	"errors"
	"testing"
)

func TestNewValid(t *testing.T) {
	b, err := New([]Item{
		{Sym: "c", Qty: 1, Flag: FlagM, Fixed: 5000},
		{Sym: "a", Qty: 100, Flag: FlagN},
		{Sym: "b", Qty: 200, Flag: FlagA, Prem: 1050},
	}, -300, 60, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := []string{"a", "b", "c"}
	for i, it := range b.Items {
		if it.Sym != want[i] {
			t.Fatalf("Items[%d].Sym = %q, want %q（应按字节序）", i, it.Sym, want[i])
		}
	}
	if b.E != -300 || b.Rmax != 60 || b.Q != 10 {
		t.Fatalf("E/Rmax/Q = %d/%d/%d", b.E, b.Rmax, b.Q)
	}
}

func TestNewInvalid(t *testing.T) {
	ok := []Item{{Sym: "a", Qty: 1, Flag: FlagN}}
	cases := []struct {
		name  string
		items []Item
		e     int64
		rmax  int64
		q     int64
	}{
		{"空清单", nil, 0, 0, 1},
		{"超500项", make([]Item, 501), 0, 0, 1},
		{"空sym", []Item{{Sym: "", Qty: 1, Flag: FlagN}}, 0, 0, 1},
		{"sym重复", []Item{{Sym: "a", Qty: 1, Flag: FlagN}, {Sym: "a", Qty: 2, Flag: FlagN}}, 0, 0, 1},
		{"qty为0", []Item{{Sym: "a", Qty: 0, Flag: FlagN}}, 0, 0, 1},
		{"qty超上限", []Item{{Sym: "a", Qty: 1_000_001, Flag: FlagN}}, 0, 0, 1},
		{"非法flag", []Item{{Sym: "a", Qty: 1, Flag: 'X'}}, 0, 0, 1},
		{"prem超上限", []Item{{Sym: "a", Qty: 1, Flag: FlagA, Prem: 5001}}, 0, 0, 1},
		{"prem为负", []Item{{Sym: "a", Qty: 1, Flag: FlagA, Prem: -1}}, 0, 0, 1},
		{"fixed为0", []Item{{Sym: "a", Qty: 1, Flag: FlagM, Fixed: 0}}, 0, 0, 1},
		{"fixed超上限", []Item{{Sym: "a", Qty: 1, Flag: FlagM, Fixed: 1_000_000_000_001}}, 0, 0, 1},
		{"E超上限", ok, 1_000_000_001, 0, 1},
		{"E低于下限", ok, -1_000_000_001, 0, 1},
		{"Rmax超100", ok, 0, 101, 1},
		{"Rmax为负", ok, 0, -1, 1},
		{"Q为0", ok, 0, 0, 0},
		{"Q超上限", ok, 0, 0, 1_000_001},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for i := range c.items {
				if c.items[i].Sym == "" && c.name != "空sym" {
					c.items[i].Sym = "x"
				}
				if c.items[i].Qty == 0 && c.name != "qty为0" {
					c.items[i].Qty = 1
				}
				if c.items[i].Flag == 0 {
					c.items[i].Flag = FlagN
				}
			}
			_, err := New(c.items, c.e, c.rmax, c.q)
			if !errors.Is(err, ErrParam) {
				t.Fatalf("err = %v, want errors.Is ErrParam", err)
			}
		})
	}
}
