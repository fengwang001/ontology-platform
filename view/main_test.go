package view_test

import (
	"errors"
	"math"
	"testing"

	"ontology/change"
)

func TestChangeCodec(t *testing.T) {
	empty, nan := "", math.NaN()
	cases := []struct {
		name    string
		c       change.Change
		wantErr error
	}{
		{"insert empty group", change.Change{Version: 1, Op: change.Insert, Group: change.G(empty), Value: 1.5, RecID: "r1"}, nil},
		{"update moves groups", change.Change{Version: 2, Op: change.Update, Group: change.G("b"), Value: 2, RecID: "r1", OldGroup: change.G("a"), OldValue: 1}, nil},
		{"negative zero", change.Change{Version: 3, Op: change.Insert, Group: change.G("z"), Value: math.Copysign(0, -1), RecID: "r2"}, nil},
		{"zero version", change.Change{Version: 0, Op: change.Insert, Group: change.G("a"), RecID: "r3"}, change.ErrBadVersion},
		{"missing group", change.Change{Version: 4, Op: change.Insert, Value: 1, RecID: "r4"}, change.ErrMissingGrp},
		{"missing old group on update", change.Change{Version: 5, Op: change.Update, Group: change.G("a"), RecID: "r5"}, change.ErrMissingGrp},
		{"missing id", change.Change{Version: 6, Op: change.Insert, Group: change.G("a")}, change.ErrMissingID},
		{"nan value", change.Change{Version: 7, Op: change.Insert, Group: change.G("a"), Value: nan, RecID: "r6"}, change.ErrNaNValue},
		{"nan old value", change.Change{Version: 8, Op: change.Update, Group: change.G("a"), RecID: "r7", OldGroup: change.G("b"), OldValue: nan}, change.ErrNaNValue},
		{"bad op", change.Change{Version: 9, Op: change.Op(99), Group: change.G("a"), RecID: "r8"}, change.ErrBadOp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.c.Encode()
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("encode err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			got, err := change.Decode(b)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Key() != tc.c.Key() || got.RecID != tc.c.RecID ||
				math.Float64bits(got.Value) != math.Float64bits(tc.c.Value) ||
				got.Version != tc.c.Version || got.Op != tc.c.Op {
				t.Fatalf("round trip mismatch: %+v vs %+v", got, tc.c)
			}
			if tc.c.Op == change.Update && got.OldKey() != tc.c.OldKey() {
				t.Fatalf("old group mismatch: %q vs %q", got.OldKey(), tc.c.OldKey())
			}
			if _, err := change.Decode(append(append([]byte{}, b...), '{')); err == nil {
				t.Fatal("decoding trailing garbage should fail")
			}
		})
	}
	raw := `{"v":10,"op":1,"val":1,"id":"rx"}`
	if _, err := change.Decode([]byte(raw)); !errors.Is(err, change.ErrMissingGrp) {
		t.Fatalf("absent JSON group: err = %v, want ErrMissingGrp", err)
	}
}
