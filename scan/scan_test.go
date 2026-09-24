package scan

import (
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/dict"
	"ontology/lookup"
)

func TestPrefix(t *testing.T) {
	numEntries := make([]string, 5000)
	for i := range numEntries {
		numEntries[i] = fmt.Sprintf("%05d", i)
	}
	numDict, err := dict.Build(numEntries, 50, 16) // 100 块
	if err != nil {
		t.Fatal(err)
	}
	utf8Dict, err := dict.Build([]string{"café", "cafés", "naïve", "日本", "日本語"}, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name       string
		d          *dict.Dict
		prefix     string
		wantFirst  string
		wantLen    int
		wantBlocks int64
	}{
		{"two-blocks", numDict, "049", "04900", 100, 2},
		{"exact-entry", numDict, "04900", "04900", 1, 1},
		{"empty-prefix", numDict, "", "00000", 5000, 100},
		{"no-match", numDict, "zzz", "", 0, 0},
		{"utf8-café", utf8Dict, "café", "café", 2, 1},
		{"utf8-日本", utf8Dict, "日本", "日本", 2, 2},
	}
	for _, tc := range cases {
		dict.ResetBlockDecodes()
		got, err := Prefix(tc.d, tc.prefix)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != tc.wantLen || (tc.wantLen > 0 && got[0] != tc.wantFirst) {
			t.Fatalf("%s: got %d results, want %d (first %q)", tc.name, len(got), tc.wantLen, tc.wantFirst)
		}
		if !slices.IsSorted(got) {
			t.Fatalf("%s: results not sorted", tc.name)
		}
		if n := dict.BlockDecodes(); n != tc.wantBlocks {
			t.Fatalf("%s: decoded %d blocks, want %d", tc.name, n, tc.wantBlocks)
		}
	}
}

func TestConcurrentScanAndLookup(t *testing.T) {
	entries := make([]string, 5000)
	for i := range entries {
		entries[i] = fmt.Sprintf("%06d", i)
	}
	d, err := dict.Build(entries, 50, 16)
	if err != nil {
		t.Fatal(err)
	}
	want, err := Prefix(d, "009")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 200 {
				got, err := Prefix(d, "009")
				if err != nil || !slices.Equal(got, want) {
					t.Errorf("scan mismatch: %v %d", err, len(got))
					return
				}
				i := (g * 617) % len(entries)
				if s, _ := lookup.Get(d, i); s != entries[i] {
					t.Errorf("Get(%d) = %q", i, s)
					return
				}
			}
		}()
	}
	wg.Wait()
}
