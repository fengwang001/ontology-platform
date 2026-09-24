package change

import (
	"errors"
	"math"
	"testing"
)

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		c    Change
		want error
	}{
		{"insert ok", Change{1, Insert, "", false, "g", true, 0, 3}, nil},
		{"insert empty group ok", Change{1, Insert, "", false, "", true, 0, 0}, nil},
		{"insert missing group", Change{1, Insert, "", false, "", false, 0, 1}, ErrMissingGroup},
		{"insert nan", Change{1, Insert, "", false, "g", true, 0, math.NaN()}, ErrNaN},
		{"delete ok", Change{2, Delete, "g", true, "", false, 4, 0}, nil},
		{"delete missing group", Change{2, Delete, "", false, "", false, 4, 0}, ErrMissingGroup},
		{"delete nan", Change{2, Delete, "g", true, "", false, math.NaN(), 0}, ErrNaN},
		{"update same group ok", Change{3, Update, "g", true, "g", true, 1, 2}, nil},
		{"update move group ok", Change{3, Update, "a", true, "b", true, 1, 2}, nil},
		{"update missing old", Change{3, Update, "", false, "b", true, 1, 2}, ErrMissingGroup},
		{"update missing new", Change{3, Update, "a", true, "", false, 1, 2}, ErrMissingGroup},
		{"update nan old", Change{3, Update, "a", true, "b", true, math.NaN(), 2}, ErrNaN},
		{"update nan new", Change{3, Update, "a", true, "b", true, 1, math.NaN()}, ErrNaN},
		{"bad op", Change{1, Op(9), "a", true, "b", true, 1, 2}, ErrBadOp},
		{"zero version", Change{0, Insert, "", false, "g", false, 0, 1}, ErrBadVersion},
		{"negative version", Change{-1, Insert, "", false, "g", false, 0, 1}, ErrBadVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.c.Valid(), tc.want) {
				t.Fatalf("Valid() = %v, want %v", tc.c.Valid(), tc.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []Change{
		{1, Insert, "", false, "g", true, 0, 3.5},
		{2, Insert, "", false, "", true, 0, -0.0},
		{3, Delete, "g", true, "", false, 2, 0},
		{4, Update, "a", true, "b", true, -1, 2},
		{5, Update, "", true, "g", true, 0, 0},
	}
	for i, want := range cases {
		b, err := want.Encode()
		if err != nil {
			t.Fatalf("case %d encode: %v", i, err)
		}
		got, err := Decode(b)
		if err != nil {
			t.Fatalf("case %d decode: %v", i, err)
		}
		if got != want {
			t.Fatalf("case %d roundtrip: got %+v want %+v", i, got, want)
		}
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  []byte
		want error
	}{
		{"garbage", []byte("{not json"), ErrBadEncoding},
		{"unknown op", []byte(`{"v":1,"o":9,"ng":"g","nv":1}`), ErrBadOp},
		{"missing group pointer", []byte(`{"v":1,"o":1}`), ErrMissingGroup},
		{"group without value", []byte(`{"v":1,"o":1,"ng":"g"}`), ErrBadEncoding},
		{"nan payload", []byte(`{"v":1,"o":1,"ng":"g","nv":NaN}`), ErrBadEncoding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.raw); !errors.Is(err, tc.want) {
				t.Fatalf("Decode = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestNormalizeZero(t *testing.T) {
	if math.Float64bits(NormalizeZero(math.Copysign(0, -1))) != math.Float64bits(0) {
		t.Fatal("-0 not normalized to +0")
	}
	if math.Float64bits(NormalizeZero(0)) != math.Float64bits(0) {
		t.Fatal("+0 not preserved")
	}
	if NormalizeZero(-1.5) != -1.5 {
		t.Fatal("nonzero value altered")
	}
}

func TestOpString(t *testing.T) {
	cases := map[Op]string{Insert: "insert", Delete: "delete", Update: "update", Op(7): "unknown"}
	for op, want := range cases {
		if op.String() != want {
			t.Fatalf("%d: got %q want %q", op, op.String(), want)
		}
	}
}
