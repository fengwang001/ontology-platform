package ontology

import (
	"math/rand"
	"testing"
)

func shuffled(rows []Row, seed int64) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	rand.New(rand.NewSource(seed)).Shuffle(len(out), func(i, j int) {
		out[i], out[j] = out[j], out[i]
	})
	return out
}

func canonicalAll(rows []Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = canonicalRow(r)
	}
	return out
}

// Shuffling either input table (with several seeds) must not change the
// result sequence element by element.
func TestOutputOrderIndependentOfInputOrder(t *testing.T) {
	var left, right []Row
	for i := 0; i < 60; i++ {
		left = append(left, Row{"k": i % 7, "li": i, "payload": i * 3})
	}
	for j := 0; j < 40; j++ {
		right = append(right, Row{"k": j % 7, "rj": j})
	}
	base, _, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	want := canonicalAll(base)
	for seed := int64(1); seed <= 5; seed++ {
		got, _, err := Join(shuffled(left, seed), shuffled(right, seed*7), []string{"k"}, Inner)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		gotCanon := canonicalAll(got)
		if len(gotCanon) != len(want) {
			t.Fatalf("seed %d: got %d rows, want %d", seed, len(gotCanon), len(want))
		}
		for i := range want {
			if gotCanon[i] != want[i] {
				t.Fatalf("seed %d: row %d differs:\n got %s\nwant %s", seed, i, gotCanon[i], want[i])
			}
		}
	}
}

// Same determinism requirement in Left mode, including unmatched rows.
func TestLeftModeOrderDeterministic(t *testing.T) {
	left := []Row{
		{"k": 3, "li": 0}, {"k": 1, "li": 1}, {"k": 9, "li": 2},
		{"k": nil, "li": 3}, {"k": 1, "li": 4},
	}
	right := []Row{{"k": 1, "rj": 0}, {"k": 3, "rj": 1}, {"k": 1, "rj": 2}}
	base, _, err := Join(left, right, []string{"k"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	want := canonicalAll(base)
	for seed := int64(1); seed <= 4; seed++ {
		got, _, err := Join(shuffled(left, seed), shuffled(right, seed+100), []string{"k"}, Left)
		if err != nil {
			t.Fatalf("Join: %v", err)
		}
		gotCanon := canonicalAll(got)
		for i := range want {
			if gotCanon[i] != want[i] {
				t.Fatalf("seed %d: row %d differs:\n got %s\nwant %s", seed, i, gotCanon[i], want[i])
			}
		}
	}
}

// Matched output is sorted by key tuple ascending, column by column.
func TestOutputSortedByKeyTuple(t *testing.T) {
	left := []Row{
		{"a": 2, "b": 1}, {"a": 1, "b": 2}, {"a": 1, "b": 1}, {"a": 2, "b": 0},
	}
	right := []Row{
		{"a": 2, "b": 1}, {"a": 1, "b": 2}, {"a": 1, "b": 1}, {"a": 2, "b": 0},
	}
	out, _, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 4 {
		t.Fatalf("got %d rows, want 4", len(out))
	}
	wantOrder := [][2]int{{1, 1}, {1, 2}, {2, 0}, {2, 1}}
	for i, want := range wantOrder {
		if out[i]["a"] != want[0] || out[i]["b"] != want[1] {
			t.Fatalf("row %d = (%v,%v), want (%d,%d)",
				i, out[i]["a"], out[i]["b"], want[0], want[1])
		}
	}
}

// Within one key, rows are ordered by content-derived identity, not by
// input position.
func TestWithinKeyOrderIsContentDerived(t *testing.T) {
	left := []Row{
		{"k": 1, "tag": "b"}, {"k": 1, "tag": "a"}, {"k": 1, "tag": "c"},
	}
	right := []Row{{"k": 1, "w": "R"}}
	out, _, err := Join(left, right, []string{"k"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	got := []string{out[0]["tag"].(string), out[1]["tag"].(string), out[2]["tag"].(string)}
	want := []string{"a", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("within-key order = %v, want %v", got, want)
		}
	}
}
