package change

import (
	"bytes"
	"math"
	"testing"
)

func strptr(s string) *string { return &s }

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		c    Change
	}{
		{"insert", Change{1, OpInsert, strptr("g"), 3.5, "id1", nil, 0}},
		{"insert-empty-group", Change{2, OpInsert, strptr(""), -1.25, "id2", nil, 0}},
		{"insert-nil-group", Change{3, OpInsert, nil, 7, "id3", nil, 0}},
		{"delete", Change{4, OpDelete, strptr("g"), 3.5, "id1", nil, 0}},
		{"update", Change{5, OpUpdate, strptr("b"), 9, "id9", strptr("a"), 1}},
		{"update-nan", Change{6, OpInsert, strptr("g"), math.NaN(), "n", nil, 0}},
		{"update-negzero", Change{7, OpUpdate, strptr(""), math.Copysign(0, -1), "z", strptr("x"), 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.c.Encode())
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if got.Ver != tc.c.Ver || got.Op != tc.c.Op || got.ID != tc.c.ID {
				t.Fatalf("scalar mismatch %+v vs %+v", got, tc.c)
			}
			if math.Float64bits(got.Val) != math.Float64bits(tc.c.Val) {
				t.Fatalf("val bits mismatch")
			}
			if (got.Group == nil) != (tc.c.Group == nil) {
				t.Fatalf("group presence mismatch: %v vs %v", got.Group, tc.c.Group)
			}
			if got.Group != nil && *got.Group != *tc.c.Group {
				t.Fatalf("group mismatch %q vs %q", *got.Group, *tc.c.Group)
			}
			if tc.c.Op == OpUpdate {
				if got.OldGroup == nil || *got.OldGroup != *tc.c.OldGroup {
					t.Fatalf("oldgroup mismatch")
				}
				if math.Float64bits(got.OldVal) != math.Float64bits(tc.c.OldVal) {
					t.Fatalf("oldval bits mismatch")
				}
			}
		})
	}
}

func TestDecodeMalformed(t *testing.T) {
	good := Change{10, OpUpdate, strptr("g"), 2, "id", strptr("h"), 4}.Encode()
	cases := []struct {
		name string
		buf  []byte
	}{
		{"nil", nil},
		{"short-header", good[:17]},
		{"truncated-group", good[:20]},
		{"truncated-tail", good[:len(good)-1]},
		{"trailing-bytes", append(append([]byte{}, good...), 0)},
		{"bad-op", func() []byte {
			b := Change{1, OpInsert, strptr("g"), 1, "i", nil, 0}.Encode()
			b[0] = 9
			return b
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.buf); err != ErrMalformed {
				t.Fatalf("want ErrMalformed, got %v", err)
			}
		})
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	c := Change{1, OpInsert, strptr("g"), 3.5, "id", nil, 0}
	if !bytes.Equal(c.Encode(), c.Encode()) {
		t.Fatal("Encode not deterministic")
	}
}
