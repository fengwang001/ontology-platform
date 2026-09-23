package verify

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/posting"
	"ontology/segment"
)

func writeFile(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.seg")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckFileClassification(t *testing.T) {
	lists := map[string]posting.List{}
	for i := 0; i < 30; i++ {
		var b posting.Builder
		_ = b.Add(0, uint32(i))
		_ = b.Add(1, uint32(i))
		lists[string(rune('a'+i%26))+string(rune('a'+i/26))] = b.List()
	}
	good := writeFile(t, mustEncode(t, segment.New(lists)))
	data, _ := os.ReadFile(good)
	cases := []struct {
		name string
		cut  int
		want error
	}{
		{"intact", len(data), nil},
		{"header", 5, segment.ErrHeaderIncomplete},
		{"dictionary", 20, segment.ErrDictIncomplete},
		{"postings", len(data) - 10, segment.ErrPostingsIncomplete},
		{"crc", len(data) - 2, segment.ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckFile(writeFile(t, data[:tc.cut]))
			if !errors.Is(err, tc.want) {
				t.Fatalf("cut %d: got %v, want %v", tc.cut, err, tc.want)
			}
		})
	}
}

func mustEncode(t *testing.T, s *segment.Segment) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "full.seg")
	if err := segment.Write(p, s); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestCheckInvariants(t *testing.T) {
	good := segment.New(map[string]posting.List{
		"a": {{Doc: 0, Pos: []uint32{1, 3}}, {Doc: 2, Pos: []uint32{0}}},
	})
	unsorted := &segment.Segment{Terms: []string{"b", "a"}, Lists: map[string]posting.List{
		"a": {}, "b": {},
	}}
	badDocs := &segment.Segment{Terms: []string{"a"}, Lists: map[string]posting.List{
		"a": {{Doc: 2, Pos: []uint32{0}}, {Doc: 1, Pos: []uint32{0}}},
	}}
	badPos := &segment.Segment{Terms: []string{"a"}, Lists: map[string]posting.List{
		"a": {{Doc: 0, Pos: []uint32{5, 2}}},
	}}
	dangling := &segment.Segment{Terms: []string{"ghost"}, Lists: map[string]posting.List{}}
	cases := []struct {
		name string
		seg  *segment.Segment
		bad  bool
	}{
		{"good", good, false},
		{"unsorted dictionary", unsorted, true},
		{"docs not ascending", badDocs, true},
		{"positions not ascending", badPos, true},
		{"dangling term", dangling, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckInvariants(tc.seg)
			if tc.bad && err == nil {
				t.Fatal("want violation, got nil")
			}
			if !tc.bad && err != nil {
				t.Fatalf("want clean, got %v", err)
			}
		})
	}
}
