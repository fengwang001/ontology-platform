package record

import "testing"

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
	}{
		{"plain", Record{Key: "alpha", Value: []byte("v1"), Seq: 1}},
		{"empty key", Record{Key: "", Value: []byte("v"), Seq: 2}},
		{"empty value", Record{Key: "k", Value: []byte{}, Seq: 3}},
		{"both empty", Record{Key: "", Value: nil, Seq: 4}},
		{"large seq", Record{Key: "k", Value: []byte{0, 1, 2}, Seq: 1 << 60}},
		{"binary value", Record{Key: "k", Value: []byte{0xff, 0x00, 0x7f}, Seq: 5}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := tc.rec.Encode(nil)
			if got := len(buf); got != tc.rec.EncodedLen() {
				t.Fatalf("EncodedLen=%d want %d", tc.rec.EncodedLen(), got)
			}
			got, n, err := Decode(buf)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if n != len(buf) {
				t.Fatalf("consumed %d of %d bytes", n, len(buf))
			}
			if got.Key != tc.rec.Key || got.Seq != tc.rec.Seq ||
				string(got.Value) != string(tc.rec.Value) {
				t.Fatalf("got %+v want %+v", got, tc.rec)
			}
		})
	}
}

func TestLessTotalOrder(t *testing.T) {
	cases := []struct {
		name string
		a, b Record
		want bool
	}{
		{"key order", Record{Key: "a", Seq: 9}, Record{Key: "b", Seq: 1}, true},
		{"equal key by seq", Record{Key: "x", Seq: 1}, Record{Key: "x", Seq: 2}, true},
		{"equal key later seq", Record{Key: "x", Seq: 3}, Record{Key: "x", Seq: 2}, false},
		{"identical", Record{Key: "x", Seq: 2}, Record{Key: "x", Seq: 2}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Less(tc.a, tc.b); got != tc.want {
				t.Fatalf("Less=%v want %v", got, tc.want)
			}
		})
	}
}

func TestDecodeTruncated(t *testing.T) {
	full := Record{Key: "key", Value: []byte("value"), Seq: 42}.Encode(nil)
	for cut := 0; cut < len(full); cut++ {
		if _, _, err := Decode(full[:cut]); err == nil {
			t.Fatalf("cut=%d: expected error", cut)
		}
	}
}
