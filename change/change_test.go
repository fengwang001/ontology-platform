package change

import (
	"errors"
	"math"
	"testing"
)

func strptr(s string) *string { return &s }

func TestChangeTable(t *testing.T) {
	nan := math.NaN()
	cases := []struct {
	name string
	c    Change
	want error
		rt   bool // 是否期望编解码往返一致
	}{
		{"insert", Change{1, Insert, 7, strptr("g"), 3.5, nil, 0}, nil, true},
		{"empty group legal", Change{2, Insert, 8, strptr(""), 0, nil, 0}, nil, true},
		{"delete negzero", Change{3, Delete, 9, strptr("g"), math.Copysign(0, -1), nil, 0}, nil, true},
		{"update move group", Change{4, Update, 7, strptr("b"), 2, strptr("a"), 1}, nil, true},
		{"bad op", Change{5, Op(9), 1, strptr("g"), 1, nil, 0}, ErrBadOp, false},
		{"zero version", Change{0, Insert, 1, strptr("g"), 1, nil, 0}, ErrBadVer, false},
		{"missing group", Change{6, Insert, 1, nil, 1, nil, 0}, ErrNoGroup, false},
		{"nan value", Change{7, Insert, 1, strptr("g"), nan, nil, 0}, ErrNaNValue, false},
		{"update missing oldgroup", Change{8, Update, 1, strptr("b"), 1, nil, 0}, ErrNoGroup, false},
		{"update nan oldval", Change{9, Update, 1, strptr("b"), 1, strptr("a"), nan}, ErrNaNValue, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.c.Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate = %v, want %v", err, tc.want)
			}
			if !tc.rt {
				return
			}
			b := tc.c.Encode()
			got, err := Decode(b)
			if err != nil || !sameChange(got, tc.c) {
				t.Fatalf("roundtrip err=%v got=%+v want=%+v", err, got, tc.c)
			}
			for k := 0; k < len(b); k++ {
				if _, err := Decode(b[:k]); !errors.Is(err, ErrDecode) {
					t.Fatalf("truncate %d: err=%v, want ErrDecode", k, err)
				}
			}
		})
	}
}

func sameChange(a, b Change) bool {
	if a.Ver != b.Ver || a.Op != b.Op || a.ID != b.ID {
		return false
	}
	if !ptrEq(a.Group, b.Group) || !ptrEq(a.OldGroup, b.OldGroup) {
		return false
	}
	return math.Float64bits(a.Val) == math.Float64bits(b.Val) &&
		math.Float64bits(a.OldVal) == math.Float64bits(b.OldVal)
}

func ptrEq(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
