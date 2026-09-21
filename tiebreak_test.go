package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

func TestScoreTieBrokenByTieColumn(t *testing.T) {
	s, err := New(testConfig(3))
	if err != nil {
		t.Fatal(err)
	}
	// Same score; tie column decides, ascending.
	for _, tie := range []string{"c", "a", "b"} {
		s.Add(map[string]any{"g": "a", "s": 1.0, "t": tie})
	}
	rows := s.Snapshot()[0].Rows
	for i, want := range []string{"a", "b", "c"} {
		if rows[i]["t"] != want {
			t.Fatalf("row %d: want tie %q, got %v", i, want, rows[i]["t"])
		}
	}
}

// TestFullTieBrokenByContentID feeds rows whose score AND tie column are
// all identical; the content-derived identity must decide, so shuffling
// the input must yield element-wise identical results.
func TestFullTieBrokenByContentID(t *testing.T) {
	base := make([]map[string]any, 0, 20)
	for i := 0; i < 20; i++ {
		base = append(base, map[string]any{
			"g":  "a",
			"s":  7.5,
			"t":  "same",
			"id": i, // distinguishes content, hence the derived identity
		})
	}

	run := func(rows []map[string]any) []map[string]any {
		s, err := New(testConfig(5))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			s.Add(r)
		}
		return s.Snapshot()[0].Rows
	}

	want := run(base)
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 10; trial++ {
		shuffled := make([]map[string]any, len(base))
		perm := rng.Perm(len(base))
		for i, j := range perm {
			shuffled[i] = base[j]
		}
		got := run(shuffled)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: shuffled input changed the Top-N result", trial)
		}
	}
	// The retained set must be the 5 smallest content identities, which for
	// these rows is deterministic; verify stability of a repeated run too.
	if again := run(base); !reflect.DeepEqual(again, want) {
		t.Fatal("repeated run over same input differs")
	}
}

func TestContentIDIgnoresMapIterationOrder(t *testing.T) {
	a := map[string]any{"x": 1, "y": "a", "z": true}
	b := map[string]any{"z": true, "y": "a", "x": 1}
	ha, ca := contentID(a)
	hb, cb := contentID(b)
	if ha != hb || ca != cb {
		t.Fatal("content identity must not depend on insertion/iteration order")
	}
}
