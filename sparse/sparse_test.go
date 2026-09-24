package sparse

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func testIndex() Index {
	return Index{Every: 4, Anchors: []Anchor{
		{Seq: 100, Offset: 26},
		{Seq: 104, Offset: 150},
		{Seq: 108, Offset: 274},
	}}
}

func TestLocate(t *testing.T) {
	idx := testIndex()
	cases := []struct {
		name    string
		from    uint64
		want    Anchor
		wantHit bool
	}{
		{"exactly on anchor", 104, Anchor{Seq: 104, Offset: 150}, true},
		{"between anchors", 106, Anchor{Seq: 104, Offset: 150}, true},
		{"before first anchor", 99, Anchor{}, false},
		{"at first anchor", 100, Anchor{Seq: 100, Offset: 26}, true},
		{"past last anchor", 500, Anchor{Seq: 108, Offset: 274}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, hit := idx.Locate(tc.from)
			if hit != tc.wantHit || (hit && got != tc.want) {
				t.Fatalf("Locate(%d) = %+v,%v want %+v,%v", tc.from, got, hit, tc.want, tc.wantHit)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		idx  Index
	}{
		{"empty", Index{Every: 1}},
		{"anchors", testIndex()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decode(Encode(tc.idx))
			if err != nil {
				t.Fatal(err)
			}
			if got.Every != tc.idx.Every || len(got.Anchors) != len(tc.idx.Anchors) {
				t.Fatalf("mismatch: %+v", got)
			}
			for i := range got.Anchors {
				if got.Anchors[i] != tc.idx.Anchors[i] {
					t.Fatalf("anchor %d mismatch", i)
				}
			}
		})
	}
}

func TestFileRoundTripAndBad(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.osi")
	if err := WriteFile(p, testIndex()); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw1, _ := os.ReadFile(p)
	if !bytes.Equal(raw1, Encode(got)) {
		t.Fatal("encode not deterministic")
	}
	if _, err := Decode([]byte("junk")); err == nil {
		t.Fatal("want error for junk")
	}
}
