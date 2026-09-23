package row

import (
	"errors"
	"testing"
)

func TestEncodeDecode(t *testing.T) {
	cases := []struct {
	name string
	r    Row
	}{
		{"empty key/payload", Row{}},
		{"empty key", Row{Payload: "v"}},
		{"ascii", Row{Key: "k", Payload: "p", Seq: 3}},
		{"unicode", Row{Key: "键", Payload: "值🐻"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := tc.r.Encode(nil)
			if len(buf) != tc.r.EncodedLen() {
				t.Fatalf("encoded len = %d, want %d", len(buf), tc.r.EncodedLen())
			}
			got, n, err := Decode(buf)
			if err != nil || n != len(buf) {
				t.Fatalf("decode err=%v n=%d", err, n)
			}
			if got.Key != tc.r.Key || got.Payload != tc.r.Payload {
				t.Fatalf("round trip mismatch %+v vs %+v", got, tc.r)
			}
		})
	}
}

func TestDecodeTruncation(t *testing.T) {
	base := Row{Key: "abc", Payload: "xy"}.Encode(nil)
	// Every prefix shorter than a full frame must be ErrShort.
	for cut := 0; cut < len(base); cut++ {
		if _, _, err := Decode(base[:cut]); !errors.Is(err, ErrShort) {
			t.Fatalf("cut=%d err=%v, want ErrShort", cut, err)
		}
	}
	if _, _, err := Decode(nil); !errors.Is(err, ErrShort) {
		t.Fatalf("nil buffer err=%v", err)
	}
	// Two rows concatenated decode one at a time with correct offsets.
	two := Row{Key: "a", Payload: "1"}.Encode(nil)
	two = append(two, Row{Key: "bb", Payload: "22"}.Encode(nil)...)
	first, n1, err := Decode(two)
	if err != nil || first.Key != "a" {
		t.Fatalf("first decode: %+v %v", first, err)
	}
	second, n2, err := Decode(two[n1:])
	if err != nil || second.Key != "bb" || n1+n2 != len(two) {
		t.Fatalf("second decode: %+v %v", second, err)
	}
}
