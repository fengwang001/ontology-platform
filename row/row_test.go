package row

import (
	"bytes"
	"math"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
		name string
		row  Row
	}{
		{"empty key", Row{"", 3.5}},
		{"normal", Row{"group-A", -12.25}},
		{"plus zero", Row{"z", 0}},
		{"minus zero", Row{"z", math.Copysign(0, -1)}},
		{"plus inf", Row{"z", math.Inf(1)}},
		{"minus inf", Row{"z", math.Inf(-1)}},
		{"nan bits", Row{"z", math.Float64frombits(math.Float64bits(math.NaN()))}},
		{"unicode key", Row{"键/名 🚀", 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf, err := Encode(nil, tc.row)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if len(buf) != EncodedLen(tc.row) {
				t.Fatalf("len = %d, want %d", len(buf), EncodedLen(tc.row))
			}
			got, rest, err := Decode(buf)
			if err != nil || len(rest) != 0 {
				t.Fatalf("decode err=%v rest=%d", err, len(rest))
			}
			if got.Key != tc.row.Key || math.Float64bits(got.V) != math.Float64bits(tc.row.V) {
				t.Fatalf("got %+v, want %+v", got, tc.row)
			}
		})
	}
}

func TestDecodeShort(t *testing.T) {
	base, _ := Encode(nil, Row{"abcd", 1})
	cases := []int{0, 1, 2, 3, 5, len(base) - 1}
	for _, n := range cases {
		if _, _, err := Decode(base[:n]); err != ErrShort {
			t.Fatalf("cut=%d err=%v, want ErrShort", n, err)
		}
	}
}

func TestStream(t *testing.T) {
	rows := []Row{{"a", 1}, {"", 2}, {"bb", -3}}
	var buf []byte
	for _, r := range rows {
		var err error
		buf, err = Encode(buf, r)
		if err != nil {
			t.Fatal(err)
		}
	}
	got := 0
	for len(buf) > 0 {
		r, rest, err := Decode(buf)
		if err != nil {
			t.Fatal(err)
		}
		if r != rows[got] {
			t.Fatalf("#%d got %+v", got, r)
		}
		buf = rest
		got++
	}
	if !bytes.Equal(buf, []byte{}) && len(buf) != 0 {
		t.Fatalf("rest %v", buf)
}

func TestIsNaN(t *testing.T) {
	if !IsNaN(math.NaN()) || IsNaN(1) || IsNaN(math.Inf(1)) || IsNaN(0) {
		t.Fatal("IsNaN wrong")
	}
}
