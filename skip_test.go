package ontology

import (
	"math"
	"testing"
)

func TestNonNumericAndNaNScoresAreSkippedAndCounted(t *testing.T) {
	s, err := New(testConfig(3))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "t": "x"})            // missing score
	s.Add(map[string]any{"g": "a", "s": "hi", "t": "x"}) // non-numeric
	s.Add(map[string]any{"g": "a", "s": nil, "t": "x"})  // nil score
	s.Add(map[string]any{"g": "a", "s": math.NaN(), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": math.NaN(), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "x"}) // ranked

	nonNumeric, nan, ok := s.Skips(GroupKey{Kind: KeyValue, Value: "a"})
	if !ok {
		t.Fatal("group a must exist")
	}
	if nonNumeric != 3 {
		t.Fatalf("want 3 non-numeric skips, got %d", nonNumeric)
	}
	if nan != 2 {
		t.Fatalf("want 2 NaN skips, got %d", nan)
	}
	rows := s.Snapshot()[0].Rows
	if len(rows) != 1 || rows[0]["s"] != 1.0 {
		t.Fatalf("skipped rows must not pollute the group, got %v", rows)
	}
	if s.Processed() != 6 {
		t.Fatalf("want 6 processed, got %d", s.Processed())
	}
}

func TestSkipsArePerGroup(t *testing.T) {
	s, err := New(testConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "s": "bad"})
	s.Add(map[string]any{"g": "b", "s": math.NaN()})
	nnA, nanA, _ := s.Skips(GroupKey{Kind: KeyValue, Value: "a"})
	nnB, nanB, _ := s.Skips(GroupKey{Kind: KeyValue, Value: "b"})
	if nnA != 1 || nanA != 0 || nnB != 0 || nanB != 1 {
		t.Fatalf("per-group skip counts wrong: a=(%d,%d) b=(%d,%d)", nnA, nanA, nnB, nanB)
	}
	if _, _, ok := s.Skips(GroupKey{Kind: KeyValue, Value: "nope"}); ok {
		t.Fatal("unknown group must report ok=false")
	}
}

func TestInfinityIsAValidScore(t *testing.T) {
	s, err := New(testConfig(3))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "s": math.Inf(-1), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 0.0, "t": "x"})
	s.Add(map[string]any{"g": "a", "s": math.Inf(1), "t": "x"})
	rows := s.Snapshot()[0].Rows
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	want := []float64{math.Inf(1), 0, math.Inf(-1)}
	for i, w := range want {
		if rows[i]["s"] != w {
			t.Fatalf("row %d: want %v, got %v", i, w, rows[i]["s"])
		}
	}
	_, nan, _ := s.Skips(GroupKey{Kind: KeyValue, Value: "a"})
	if nan != 0 {
		t.Fatalf("infinities must not count as NaN, got %d", nan)
	}
}
