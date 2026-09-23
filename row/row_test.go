package row

import (
	"math"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []Row{
		{Key: "", Val: 0},
		{Key: "a", Val: 1.5},
		{Key: "组键", Val: -42.25},
		{Key: "inf", Val: math.Inf(1)},
		{Key: "-inf", Val: math.Inf(-1)},
		{Key: "negzero", Val: math.Copysign(0, -1)},
		{Key: string(make([]byte, 300)), Val: 1e300},
	}
	for _, c := range cases {
		got, err := Decode(Encode(c))
		if err != nil {
			t.Fatalf("Decode(%q): %v", c.Key, err)
		}
		if got.Key != c.Key {
			t.Errorf("key: got %q want %q", got.Key, c.Key)
		}
		wantBits := math.Float64bits(c.Val)
		if c.Val == 0 {
			wantBits = math.Float64bits(0.0) // ±0 normalize to +0
		}
		if math.Float64bits(got.Val) != wantBits {
			t.Errorf("val bits: got %x want %x", math.Float64bits(got.Val), wantBits)
		}
	}
}

func TestDecodeMalformed(t *testing.T) {
	good := Encode(Row{Key: "k", Val: 3})
	cases := [][]byte{
		nil,
		{1, 2},
		good[:len(good)-1],   // truncated value
		good[:4],             // key length only
		append(good, 0),      // trailing garbage
		{5, 0, 0, 0, 'a', 0}, // key length beyond body
	}
	for i, c := range cases {
		if _, err := Decode(c); err == nil {
			t.Errorf("case %d: want error", i)
		}
	}
}
