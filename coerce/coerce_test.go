package coerce

import (
	"errors"
	"math"
	"testing"
)

func TestCoerce(t *testing.T) {
	cases := []struct {
		name string
		typ  Type
		in   any
		want any
		ok   bool
	}{
		{"long int64", Long, int64(-7), int64(-7), true},
		{"long float 2.0", Long, float64(2), int64(2), true},
		{"long float 2.5 reject", Long, 2.5, nil, false},
		{"long float nan", Long, math.NaN(), nil, false},
		{"long float inf", Long, math.Inf(1), nil, false},
		{"long float min out of range", Long, float64(math.MinInt64) * 2, nil, false},
		{"long canonical -7", Long, "-7", int64(-7), true},
		{"long canonical 0", Long, "0", int64(0), true},
		{"long leading zero", Long, "07", nil, false},
		{"long minus zero", Long, "-0", nil, false},
		{"long plus sign", Long, "+1", nil, false},
		{"long whitespace", Long, " 1", nil, false},
		{"long empty", Long, "", nil, false},
		{"long text", Long, "abc", nil, false},
		{"long min", Long, "-9223372036854775808", int64(math.MinInt64), true},
		{"long overflow", Long, "9223372036854775808", nil, false},
		{"long underflow", Long, "-9223372036854775809", nil, false},
		{"long bool reject", Long, true, nil, false},

		{"double float", Double, 1.5, 1.5, true},
		{"double nan", Double, math.NaN(), nil, false},
		{"double inf", Double, math.Inf(-1), nil, false},
		{"double int 2^53", Double, int64(1) << 53, float64(int64(1) << 53), true},
		{"double int -(2^53)", Double, -(int64(1) << 53), -float64(int64(1) << 53), true},
		{"double int 2^53+1 reject", Double, (int64(1) << 53) + 1, nil, false},
		{"double string reject", Double, "1.5", nil, false},

		{"keyword string", Keyword, "abc", "abc", true},
		{"keyword int64", Keyword, int64(42), "42", true},
		{"keyword bool", Keyword, true, "true", true},
		{"keyword float reject", Keyword, 1.0, nil, false},

		{"bool bool", Bool, false, false, true},
		{"bool string true", Bool, "true", true, true},
		{"bool string false", Bool, "false", false, true},
		{"bool string other", Bool, "TRUE", nil, false},
		{"bool int reject", Bool, int64(1), nil, false},

		{"object map", Object, map[string]any{"k": int64(1)}, map[string]any{"k": int64(1)}, true},
		{"object scalar reject", Object, int64(1), nil, false},
		{"leaf rejects object", Long, map[string]any{}, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Coerce(c.typ, c.in)
			if c.ok {
				if err != nil {
					t.Fatalf("unexpected err %v", err)
				}
				if !equalValue(got, c.want) {
					t.Fatalf("got %#v, want %#v", got, c.want)
				}
			} else if !errors.Is(err, ErrTypeConflict) {
				t.Fatalf("got %v err %v, want type conflict", got, err)
			}
		})
	}
}

func equalValue(a, b any) bool {
	if am, ok := a.(map[string]any); ok {
		bm, ok := b.(map[string]any)
		return ok && mapsEqual(am, bm)
	}
	return a == b
}

func mapsEqual(am, bm map[string]any) bool {
	if len(am) != len(bm) {
		return false
	}
	for k, v := range am {
		if bm[k] != v {
			return false
		}
	}
	return true
}

func TestInfer(t *testing.T) {
	cases := []struct {
		in   any
		want Type
		ok   bool
	}{
		{true, Bool, true},
		{int64(1), Long, true},
		{1.0, Double, true},
		{"s", Keyword, true},
		{map[string]any{}, Object, true},
		{nil, "", false},
		{[]any{1}, "", false},
	}
	for _, c := range cases {
		got, ok := Infer(c.in)
		if got != c.want || ok != c.ok {
			t.Fatalf("Infer(%#v) = %q,%v want %q,%v", c.in, got, ok, c.want, c.ok)
		}
	}
}
