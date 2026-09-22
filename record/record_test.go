package record

import (
	"errors"
	"testing"
)

func TestMarshalRoundtrip(t *testing.T) {
	cases := []struct {
		name string
		rec  Record
	}{
		{"normal", Record{Key: "k1", Value: []byte("v1"), Seq: 7}},
		{"empty key", Record{Key: "", Value: []byte("v"), Seq: 0}},
		{"empty value", Record{Key: "k", Value: []byte{}, Seq: 1}},
		{"both empty", Record{Key: "", Value: nil, Seq: 1<<63 + 3}},
		{"binary value", Record{Key: "k", Value: []byte{0x00, 0xff, 0x0a}, Seq: 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Unmarshal(tc.rec.Marshal())
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if got.Key != tc.rec.Key || got.Seq != tc.rec.Seq ||
				string(got.Value) != string(tc.rec.Value) {
				t.Fatalf("roundtrip mismatch: got %+v want %+v", got, tc.rec)
			}
			if tc.rec.Size() != len(tc.rec.Marshal()) {
				t.Fatalf("Size()=%d != encoded len %d", tc.rec.Size(), len(tc.rec.Marshal()))
			}
		})
	}
}

func TestUnmarshalMalformed(t *testing.T) {
	good := Record{Key: "k", Value: []byte("v"), Seq: 1}.Marshal()
	cases := []struct {
		name string
		buf  []byte
	}{
		{"empty", nil},
		{"short header", good[:10]},
		{"truncated body", good[:len(good)-1]},
		{"trailing garbage", append(append([]byte{}, good...), 0x00)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Unmarshal(tc.buf); !errors.Is(err, ErrMalformed) {
				t.Fatalf("want ErrMalformed, got %v", err)
			}
		})
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		name string
		a, b Record
		want bool
	}{
		{"key order", Record{Key: "a", Seq: 9}, Record{Key: "b", Seq: 0}, true},
		{"key order reverse", Record{Key: "b", Seq: 0}, Record{Key: "a", Seq: 9}, false},
		{"equal key by seq", Record{Key: "x", Seq: 1}, Record{Key: "x", Seq: 2}, true},
		{"equal key seq reverse", Record{Key: "x", Seq: 2}, Record{Key: "x", Seq: 1}, false},
		{"identical", Record{Key: "x", Seq: 1}, Record{Key: "x", Seq: 1}, false},
		{"empty key first", Record{Key: "", Seq: 5}, Record{Key: "a", Seq: 0}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Less(tc.a, tc.b); got != tc.want {
				t.Fatalf("Less(%+v,%+v)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
