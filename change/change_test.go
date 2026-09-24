package change

import (
	"bytes"
	"math"
	"testing"
)

func TestCodec(t *testing.T) {
	cases := []struct {
		name string
		c    Change
	}{
		{"insert", Change{Insert, 7, "g", true, "", false, 3.5, 1}},
		{"delete", Change{Delete, 9, "", true, "", false, -0.0, 2}},
		{"update-move", Change{Update, 3, "old", true, "new", true, math.NaN(), 4}},
		{"empty-group", Change{Insert, 1, "", true, "", false, 0, 5}},
		{"missing-group", Change{Insert, 2, "", false, "", false, 1, 6}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(tc.c.Encode())
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Op != tc.c.Op || got.ID != tc.c.ID || got.Ver != tc.c.Ver ||
				got.Group != tc.c.Group || got.HasGroup != tc.c.HasGroup ||
				got.NewGroup != tc.c.NewGroup || got.HasNewGroup != tc.c.HasNewGroup {
				t.Fatalf("field mismatch %+v vs %+v", got, tc.c)
			}
			if math.Float64bits(got.Value) != math.Float64bits(tc.c.Value) {
				t.Fatalf("value bits mismatch: %v vs %v", got.Value, tc.c.Value)
			}
		})
	}
}

func TestDecodeMalformed(t *testing.T) {
	good := Change{Insert, 1, "abc", true, "", false, 2, 1}.Encode()
	cases := [][]byte{
		nil,
		good[:5],
		good[:len(good)-1],
		append(append([]byte{}, good...), 9),
		bytes.Repeat([]byte{0xff}, 30),
	}
	for i, p := range cases {
		if _, err := Decode(p); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}
