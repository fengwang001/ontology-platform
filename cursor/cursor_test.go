package cursor

import (
	"errors"
	"testing"

	"ontology/row"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		k    row.Key
		dir  Direction
	}{
		{"simple", row.Key{V: 1.5, ID: "x"}, Forward},
		{"negative", row.Key{V: -3.25, ID: "id-9"}, Backward},
		{"zero empty id", row.Key{V: 0, ID: ""}, Forward},
		{"unicode id", row.Key{V: 42, ID: "行-🙂"}, Backward},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := Encode(tc.k, tc.dir)
			got, dir, err := Decode(raw, tc.dir)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if dir != tc.dir || !row.Equal(got, tc.k) {
				t.Fatalf("roundtrip got (%v,%d), want (%v,%d)", got, dir, tc.k, tc.dir)
			}
		})
	}
}

func TestEmptyCursor(t *testing.T) {
	if _, _, err := Decode(nil, Forward); !errors.Is(err, ErrEmpty) {
		t.Fatalf("nil cursor err = %v, want ErrEmpty", err)
	}
	if _, _, err := Decode([]byte{}, Backward); !errors.Is(err, ErrEmpty) {
		t.Fatalf("empty cursor err = %v, want ErrEmpty", err)
	}
}

func TestCrossDirection(t *testing.T) {
	if _, _, err := Decode(Encode(row.Key{V: 1, ID: "a"}, Forward), Backward); !errors.Is(err, ErrWrongDirection) {
		t.Fatalf("forward->backward err = %v, want ErrWrongDirection", err)
	}
	if _, _, err := Decode(Encode(row.Key{V: 1, ID: "a"}, Backward), Forward); !errors.Is(err, ErrWrongDirection) {
		t.Fatalf("backward->forward err = %v, want ErrWrongDirection", err)
	}
}

// TestBitFlip 逐比特翻转合法游标的每一位，任何变体都不得被接受。
func TestBitFlip(t *testing.T) {
	k := row.Key{V: 2.5, ID: "row-1"}
	raw := Encode(k, Forward)

	cases := []struct {
		name string
		want error
	}{
		{"checksum", ErrChecksum},
		{"incomplete", ErrIncomplete},
		{"direction", ErrDirection},
	}
	counts := map[string]int{}
	accepted := 0

	for bit := 0; bit < len(raw)*8; bit++ {
		mut := append([]byte(nil), raw...)
		mut[bit/8] ^= 1 << uint(7-bit%8)
		_, _, err := Decode(mut, Forward)
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrIncomplete):
			counts["incomplete"]++
		case errors.Is(err, ErrDirection):
			counts["direction"]++
		case errors.Is(err, ErrChecksum):
			counts["checksum"]++
		default:
			t.Fatalf("bit %d: unexpected error %v", bit, err)
		}
	}

	if accepted != 0 {
		t.Fatalf("%d mutated cursors were wrongly accepted", accepted)
	}
	total := 0
	for _, tc := range cases {
		n := counts[tc.name]
		total += n
		t.Logf("%s: %d variants", tc.name, n)
	}
	if total != len(raw)*8 {
		t.Fatalf("classified %d of %d bits", total, len(raw)*8)
	}
}

func TestDecodeAllocsConstant(t *testing.T) {
	raw := Encode(row.Key{V: 7.5, ID: "abc"}, Forward)
	allocs := testing.AllocsPerRun(100, func() {
		if _, _, err := Decode(raw, Forward); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	})
	if allocs > 2 {
		t.Fatalf("Decode allocs = %v, want <= 2 (constant)", allocs)
	}
}
