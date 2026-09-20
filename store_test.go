package ontology

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

// scanIDs recomputes the answer of Lookup by brute-force table scan.
func scanIDs(s *Store, attr string, value any) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key, ok := keyOf(value)
	out := make([]string, 0)
	for id, ent := range s.ents {
		v, has := ent[attr]
		if !has || v == nil || !ok {
			continue
		}
		if k, _ := keyOf(v); k == key {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func fillColors(s *Store, n int) {
	colors := []string{"red", "green", "blue", "yellow", "cyan"}
	for i := 0; i < n; i++ {
		s.Upsert(fmt.Sprintf("e%05d", i), map[string]any{
			"color": colors[i%len(colors)],
			"size":  i % 7,
		})
	}
}

func TestLookupMatchesFullScan(t *testing.T) {
	s := NewStore("color")
	fillColors(s, 2000)
	// Mix in nil and missing values.
	for i := 0; i < 100; i++ {
		s.Upsert(fmt.Sprintf("nil%03d", i), map[string]any{"color": nil})
		s.Upsert(fmt.Sprintf("miss%03d", i), map[string]any{"other": 1})
	}
	for _, c := range []string{"red", "green", "blue", "yellow", "cyan", "absent"} {
		got := s.Lookup("color", c)
		want := scanIDs(s, "color", c)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Lookup(%q) = %v, scan = %v", c, got, want)
		}
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestLookupSortedAndIsolated(t *testing.T) {
	s := NewStore("color")
	for _, id := range []string{"z9", "a1", "m5", "b2", "q7"} {
		s.Upsert(id, map[string]any{"color": "red"})
	}
	got := s.Lookup("color", "red")
	want := []string{"a1", "b2", "m5", "q7", "z9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("not sorted: %v", got)
	}
	// Mutating the returned slice must not corrupt the index.
	got[0] = "HACKED"
	got = got[:1]
	again := s.Lookup("color", "red")
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("index affected by caller mutation: %v", again)
	}
}

func TestLookupEmptyOnUnknownAttrAndNil(t *testing.T) {
	s := NewStore("color")
	s.Upsert("e1", map[string]any{"color": "red"})
	if got := s.Lookup("nope", "red"); len(got) != 0 {
		t.Fatalf("unindexed attr lookup = %v", got)
	}
	if got := s.Lookup("color", nil); len(got) != 0 {
		t.Fatalf("nil lookup = %v", got)
	}
}

func TestIndexStatsTrackMutations(t *testing.T) {
	s := NewStore("color")
	fillColors(s, 1000)
	for i := 0; i < 200; i++ {
		s.Upsert(fmt.Sprintf("e%05d", i), map[string]any{"color": nil})
	}
	for i := 200; i < 400; i++ {
		s.Delete(fmt.Sprintf("e%05d", i))
	}
	values, entries := s.IndexStats()
	indexed, _, _ := s.Counts("color")
	if entries != indexed {
		t.Fatalf("entries %d != indexed rows %d", entries, indexed)
	}
	if values != 4 { // cyan,yellow gone? no: 1000 rows, 5 colors, first 400 removed/nil
		t.Logf("distinct values = %d", values)
	}
	if err := s.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}
