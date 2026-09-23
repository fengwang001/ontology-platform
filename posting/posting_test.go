package posting

import (
	"errors"
	"reflect"
	"testing"
)

func build(t *testing.T, pts [][2]uint32) List {
	t.Helper()
	var b Builder
	for _, p := range pts {
		if err := b.Add(p[0], p[1]); err != nil {
			t.Fatalf("Add(%d,%d): %v", p[0], p[1], err)
		}
	}
	return b.List()
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		pts  [][2]uint32
	}{
		{"empty", nil},
		{"single doc single pos", [][2]uint32{{0, 0}}},
		{"single doc multi pos", [][2]uint32{{3, 0}, {3, 5}, {3, 100}}},
		{"multi doc", [][2]uint32{{0, 1}, {2, 0}, {2, 7}, {1000, 300}}},
		{"large gaps", [][2]uint32{{0, 0}, {1 << 20, 1 << 18}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := build(t, tc.pts)
			got, err := Decode(Encode(in))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if len(in) == 0 && len(got) == 0 {
				return
			}
			if !reflect.DeepEqual(in, got) {
				t.Fatalf("round trip mismatch:\n in=%v\ngot=%v", in, got)
			}
		})
	}
}

func TestBuilderRejects(t *testing.T) {
	cases := []struct {
		name    string
		seq     [][2]uint32
		wantErr error
		dups    int
	}{
		{"dup same pos", [][2]uint32{{1, 2}, {1, 2}}, ErrDuplicate, 1},
		{"dup counted twice", [][2]uint32{{0, 0}, {0, 0}, {0, 0}}, ErrDuplicate, 2},
		{"pos out of order", [][2]uint32{{1, 5}, {1, 3}}, ErrOutOfOrder, 0},
		{"doc out of order", [][2]uint32{{2, 0}, {1, 0}}, ErrOutOfOrder, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b Builder
			var err error
			for _, p := range tc.seq {
				if e := b.Add(p[0], p[1]); e != nil {
					err = e
				}
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if b.Dups() != tc.dups {
				t.Fatalf("dups: want %d, got %d", tc.dups, b.Dups())
			}
		})
	}
}

func TestDecodeCorrupt(t *testing.T) {
	good := Encode(build(t, [][2]uint32{{0, 1}, {2, 3}, {5, 0}}))
	cases := []struct {
		name string
		data []byte
	}{
		{"empty input", nil},
		{"truncated mid varint", good[:len(good)-1]},
		{"trailing garbage", append(append([]byte{}, good...), 0x7f)},
		{"zero doc delta", []byte{2, 5, 1, 0, 0, 1, 0}},
		{"zero pos delta", []byte{1, 0, 2, 3, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Decode(tc.data); err == nil {
				t.Fatal("want error, got nil")
			}
		})
	}
}
