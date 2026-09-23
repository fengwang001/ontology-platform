package change

import (
	"errors"
	"math"
	"testing"
)

func sp(s string) *string { return &s }

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
	name string
	c    Change
}{
	{"insert", Change{Insert, 1, "r1", sp("g"), 2.5, nil, 0}},
	{"delete", Change{Delete, 2, "r2", sp(""), -7, nil, 0}},
	{"missing-group", Change{Insert, 3, "r3", nil, 1, nil, 0}},
	{"nan", Change{Insert, 4, "r4", sp("g"), math.NaN(), nil, 0}},
	{"negzero", Change{Insert, 5, "r5", sp("g"), math.Copysign(0, -1), nil, 0}},
	{"update-same", Change{Update, 6, "r6", sp("a"), 1, sp("a"), 2}},
	{"update-move", Change{Update, 7, "r7", sp("a"), 1, sp("b"), 3}},
	{"update-nil-new", Change{Update, 8, "r8", sp("a"), 1, nil, 3}},
	{"unicode", Change{Insert, 9, "记录", sp("组😀"), 9, nil, 0}},
	{"vmax", Change{Insert, math.MaxUint64, "z", sp("z"), 1e308, nil, 0}},
}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodePayload(EncodePayload(tc.c))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !equal(got, tc.c) {
				t.Fatalf("round trip mismatch:\n got=%+v\nwant=%+v", got, tc.c)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	base := EncodePayload(Change{Insert, 1, "r", sp("g"), 1, nil, 0})
	cases := []struct {
	name string
	p    []byte
	err  error
}{
	{"empty", nil, ErrMalformed},
	{"bad-op", []byte{9, 0}, ErrUnknownOp},
	{"truncated-version", base[:3], ErrMalformed},
	{"truncated-id-len", base[:9], ErrMalformed},
	{"truncated-id-body", base[:11], ErrMalformed},
	{"truncated-group-flag", func() []byte { b := append([]byte{}, base...); return b[:11+len("r")] }(), ErrMalformed},
	{"trailing-insert", append(base, 0), ErrMalformed},
	{"update-missing-tail", func() []byte {
		u := EncodePayload(Change{Update, 1, "r", sp("g"), 1, sp("g"), 2})
		return u[:len(u)-1]
	}(), ErrMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodePayload(tc.p); !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
		})
	}
}

func equal(a, b Change) bool {
	if a.Op != b.Op || a.Version != b.Version || a.ID != b.ID {
		return false
	}
	if !spEqual(a.Group, b.Group) || !spEqual(a.NewGroup, b.NewGroup) {
		return false
	}
	return math.Float64bits(a.Value) == math.Float64bits(b.Value) &&
		math.Float64bits(a.NewValue) == math.Float64bits(b.NewValue)
}

func spEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
