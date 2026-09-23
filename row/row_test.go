package row

import (
	"math"
	"testing"
)

func TestCodec(t *testing.T) {
	cases := []struct {
		name string
		in   Row
	}{
		{"plain", Row{Key: "g1", V: 3.5}},
		{"empty key", Row{Key: "", V: -2}},
		{"zero", Row{Key: "z", V: 0}},
		{"neg zero", Row{Key: "z", V: math.Copysign(0, -1)}},
		{"plus inf", Row{Key: "i", V: math.Inf(1)}},
		{"minus inf", Row{Key: "i", V: math.Inf(-1)}},
		{"integer exact", Row{Key: "大键/abc", V: 1 << 40}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeBody(tc.in.Encode())
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Key != tc.in.Key ||
				math.Float64bits(got.V) != math.Float64bits(tc.in.V) {
				t.Fatalf("round trip %+v -> %+v", tc.in, got)
			}
		})
	}
}

func TestValidAndBadBody(t *testing.T) {
	valid := []struct {
		r    Row
		want bool
	}{
		{Row{V: math.NaN()}, false},
		{Row{V: math.Inf(1)}, true},
		{Row{V: math.Inf(-1)}, true},
		{Row{Key: "", V: 0}, true},
	}
	for _, tc := range valid {
		if got := tc.r.Valid(); got != tc.want {
			t.Fatalf("Valid(%v)=%v want %v", tc.r.V, got, tc.want)
		}
	}
	bad := [][]byte{nil, {}, {0, 0}, {0, 0, 0, 1}, {0, 0, 0, 1, 'x'},
		{0, 0, 0, 1, 'x', 0, 0, 0, 0, 0, 0, 0}}
	for i, b := range bad {
		if _, err := DecodeBody(b); err == nil {
			t.Fatalf("bad body #%d unexpectedly decoded", i)
		}
	}
}
