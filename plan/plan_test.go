package plan

import (
	"errors"
	"testing"
)

func exampleRanges() []Range {
	return []Range{
		{Lo: 51, Hi: 150, Normal: Scheme{13, 1, 2}, Tightened: Scheme{20, 1, 2}, Reduced: Scheme{5, 0, 2}},
	}
}

func TestNewValidation(t *testing.T) {
	bad := []struct {
		name   string
		ranges []Range
		lr     int
	}{
		{"empty", nil, 2},
		{"bad bounds", []Range{{Lo: 0, Hi: 10, Normal: Scheme{2, 0, 1}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"n zero", []Range{{Lo: 1, Hi: 10, Normal: Scheme{0, 0, 1}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"ac neg", []Range{{Lo: 1, Hi: 10, Normal: Scheme{2, -1, 0}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"ac ge re", []Range{{Lo: 1, Hi: 10, Normal: Scheme{2, 1, 1}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"normal gap", []Range{{Lo: 1, Hi: 10, Normal: Scheme{2, 0, 2}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"tight gap", []Range{{Lo: 1, Hi: 10, Normal: Scheme{2, 0, 1}, Tightened: Scheme{2, 0, 3}, Reduced: Scheme{2, 0, 1}}}, 2},
		{"overlap", []Range{
			{Lo: 1, Hi: 10, Normal: Scheme{2, 0, 1}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 2}},
			{Lo: 5, Hi: 20, Normal: Scheme{2, 0, 1}, Tightened: Scheme{2, 0, 1}, Reduced: Scheme{2, 0, 2}},
		}, 2},
		{"lr neg", exampleRanges(), -1},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.ranges, tc.lr); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
		})
	}
}

func TestReducedGapAllowed(t *testing.T) {
	if _, err := New(exampleRanges(), 2); err != nil {
		t.Fatalf("reduced Re>Ac+1 must be allowed: %v", err)
	}
}

func TestSchemeFor(t *testing.T) {
	tab, err := New(exampleRanges(), 2)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		n       int
		s       Severity
		wantN   int
		wantErr error
	}{
		{60, Normal, 13, nil},
		{51, Tightened, 20, nil},
		{150, Reduced, 5, nil},
		{10, Normal, 0, ErrNoRange},
		{151, Normal, 0, ErrNoRange},
		{0, Normal, 0, ErrInvalidArgument},
		{1_000_001, Normal, 0, ErrInvalidArgument},
		{60, Suspended, 0, ErrInvalidArgument},
	}
	for _, tc := range cases {
		sc, err := tab.SchemeFor(tc.n, tc.s)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("N=%d s=%d: want err %v, got %v", tc.n, tc.s, tc.wantErr, err)
		}
		if tc.wantErr == nil && sc.N != tc.wantN {
			t.Fatalf("N=%d s=%d: want n=%d, got %d", tc.n, tc.s, tc.wantN, sc.N)
		}
	}
}

func TestBoundaryAcRe(t *testing.T) {
	tab, _ := New(exampleRanges(), 2)
	sc, _ := tab.SchemeFor(60, Normal) // (13,1,2)
	if !(1 <= sc.Ac) || !(2 >= sc.Re) {
		t.Fatal("boundary values wrong")
	}
}

func TestSchemeClampSmallLot(t *testing.T) {
	ranges := []Range{{Lo: 8, Hi: 150, Normal: Scheme{13, 1, 2}, Tightened: Scheme{20, 1, 2}, Reduced: Scheme{5, 0, 2}}}
	tab, err := New(ranges, 2)
	if err != nil {
		t.Fatal(err)
	}
	sc, err := tab.SchemeFor(10, Tightened)
	if err != nil {
		t.Fatal(err)
	}
	if sc.N != 10 || sc.Ac != 1 || sc.Re != 2 {
		t.Fatalf("clamp: got %+v", sc)
	}
}
