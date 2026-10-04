package basket

import (
	"errors"
	"testing"
)

func TestBasketTable(t *testing.T) {
	tests := []struct {
		name    string
		items   []Item
		e       int64
		ratio   int64
		units   int64
		wantErr error
		first   string
	}{
		{
			name: "three flags valid and sorted",
			items: []Item{
				{Sym: "c", Qty: 1, Flag: Mandatory, Fixed: 5000},
				{Sym: "b", Qty: 200, Flag: Allowed, Prem: 1050},
				{Sym: "a", Qty: 100, Flag: Forbidden},
			},
			e:     -300,
			ratio: 60,
			units: 1,
			first: "a",
		},
		{"empty", nil, 0, 0, 1, ErrInvalidArgument, ""},
		{"too many", make([]Item, 501), 0, 0, 1, ErrInvalidArgument, ""},
		{"empty symbol", []Item{{Qty: 1, Flag: Forbidden}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"duplicate", []Item{{Sym: "a", Qty: 1, Flag: Forbidden}, {Sym: "a", Qty: 1, Flag: Forbidden}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"bad qty", []Item{{Sym: "a", Flag: Forbidden}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"bad flag", []Item{{Sym: "a", Qty: 1, Flag: "X"}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"bad premium", []Item{{Sym: "a", Qty: 1, Flag: Allowed, Prem: 5001}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"bad fixed", []Item{{Sym: "a", Qty: 1, Flag: Mandatory}}, 0, 0, 1, ErrInvalidArgument, ""},
		{"cash diff range", []Item{{Sym: "a", Qty: 1, Flag: Forbidden}}, 1_000_000_001, 0, 1, ErrInvalidArgument, ""},
		{"ratio range", []Item{{Sym: "a", Qty: 1, Flag: Forbidden}}, 0, 101, 1, ErrInvalidArgument, ""},
		{"daily units range", []Item{{Sym: "a", Qty: 1, Flag: Forbidden}}, 0, 0, 0, ErrInvalidArgument, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(tt.items, tt.e, tt.ratio, tt.units)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() err = %v, want %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got.Items()[0].Sym != tt.first {
				t.Fatalf("first item = %q, want %q", got.Items()[0].Sym, tt.first)
			}
		})
	}
}
