package vv

import (
	"errors"
	"math"
	"testing"
)

func vec(pairs ...any) Vector {
	v := Vector{}
	for i := 0; i < len(pairs); i += 2 {
		v[pairs[i].(string)] = uint64(pairs[i+1].(int))
	}
	return v
}

func TestCompare(t *testing.T) {
	cases := []struct {
	name string
	a, b Vector
	want Relation
	}{
		{"equal-empty", Vector{}, Vector{}, Equal},
		{"equal-explicit-zero", vec("A", 1), vec("A", 1, "B", 0), Equal},
		{"equal-same", vec("A", 2, "B", 3), vec("B", 3, "A", 2), Equal},
		{"less-same-keys", vec("A", 1), vec("A", 2), Less},
		{"less-multi", vec("A", 1, "B", 2), vec("A", 2, "B", 2), Less},
		{"greater-same-keys", vec("A", 2), vec("A", 1), Greater},
		{"greater-multi", vec("A", 3, "B", 2), vec("A", 2, "B", 2), Greater},
		{"concurrent-cross", vec("A", 1, "B", 0), vec("A", 0, "B", 1), Concurrent},
		{"concurrent-two-keys", vec("A", 2, "B", 1), vec("A", 1, "B", 2), Concurrent},
		{"concurrent-extra-key", vec("A", 1), vec("B", 1), Concurrent},
		{"concurrent-three", vec("A", 3, "B", 1, "C", 0), vec("A", 2, "B", 2, "C", 1), Concurrent},
		{"zero-vs-nonzero", Vector{}, vec("A", 1), Less},
		{"single-replica-chain", vec("A", 5), vec("A", 7), Less},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Compare(c.a, c.b); got != c.want {
				t.Fatalf("Compare = %s, want %s", got, c.want)
			}
			if rev := Compare(c.b, c.a); rev != wantReverse(c.want) {
				t.Fatalf("reverse = %s, want %s", rev, wantReverse(c.want))
			}
		})
	}
}

func wantReverse(r Relation) Relation {
	switch r {
	case Less:
		return Greater
	case Greater:
		return Less
	default:
		return r
	}
}

func TestCompareCost(t *testing.T) {
	cases := []struct{ a, b Vector }{
		{vec("A", 1, "B", 2, "C", 3), vec("B", 5, "C", 1, "D", 9)},
		{vec("A", 1), vec("A", 1, "B", 0, "C", 0)},
		{Vector{}, Vector{}},
	}
	for _, c := range cases {
		rel, n := CompareCounted(c.a, c.b)
		union := map[string]struct{}{}
		for k := range c.a {
			union[k] = struct{}{}
		}
		for k := range c.b {
			union[k] = struct{}{}
		}
		if n != len(union) || n > 2*len(union) {
			t.Fatalf("rel=%s reads=%d union=%d: cost violation", rel, n, len(union))
		}
	}
}

func TestIncJoinAndFaults(t *testing.T) {
	v, err := Inc(Vector{}, "A")
	if err != nil || Compare(v, vec("A", 1)) != Equal {
		t.Fatalf("inc = %v,%v", v, err)
	}
	if _, err := Inc(Vector{"A": math.MaxUint64}, "A"); !errors.Is(err, ErrOverflow) {
		t.Fatalf("overflow err = %v", err)
	}
	known := map[string]struct{}{"A": {}, "B": {}}
	if err := vec("A", 1, "Z", 2).CheckKnown(known); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("unknown err = %v", err)
	}
	if err := vec("A", 1, "B", 0).CheckKnown(known); err != nil {
		t.Fatalf("known err = %v", err)
	}
	if Compare(Join(vec("A", 3, "B", 1), vec("A", 2, "C", 5)),
		vec("A", 3, "B", 1, "C", 5)) != Equal {
		t.Fatal("join mismatch")
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	cases := []Vector{
		{},
		vec("A", 1),
		vec("A", 1, "B", 0),
		vec("B", 2, "A", 7, "C", 0),
		Vector{"X": math.MaxUint64},
	}
	for _, c := range cases {
		raw := c.Encode()
		got, err := Decode(raw)
		if err != nil || Compare(got, c) != Equal {
			t.Fatalf("roundtrip %v -> %v, %v", c, got, err)
		}
	}
}

func TestDecodeTruncation(t *testing.T) {
	raw := vec("A", 1, "B", 2).Encode()
	want := func(i int) error {
		switch {
		case i < 4:
			return ErrHeader
		case i < len(raw)-4:
			return ErrEntry
		default:
			return ErrCRC
		}
	}
	for i := 0; i < len(raw); i++ {
		_, err := Decode(raw[:i])
		if !errors.Is(err, want(i)) {
			t.Fatalf("cut=%d/%d err=%v want=%v", i, len(raw), err, want(i))
		}
	}
	if _, err := Decode(flipByte(raw)); !errors.Is(err, ErrCRC) {
		t.Fatalf("corrupt err=%v", err)
	}
}

func flipByte(b []byte) []byte {
	c := append([]byte(nil), b...)
	c[len(c)-5] ^= 0xFF
	return c
}
