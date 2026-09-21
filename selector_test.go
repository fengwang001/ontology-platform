package ontology

import (
	"errors"
	"testing"
)

func testConfig(n int) Config {
	return Config{GroupKey: "g", ScoreKey: "s", TieKey: "t", N: n}
}

func TestNewRejectsNonPositiveN(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		s, err := New(testConfig(n))
		if !errors.Is(err, ErrNonPositiveN) {
			t.Fatalf("N=%d: want ErrNonPositiveN, got %v", n, err)
		}
		if s != nil {
			t.Fatalf("N=%d: selector must be nil on error", n)
		}
	}
	if _, err := New(testConfig(1)); err != nil {
		t.Fatalf("N=1 must be accepted, got %v", err)
	}
}

func TestGroupSmallerThanNReturnsAll(t *testing.T) {
	s, err := New(testConfig(5))
	if err != nil {
		t.Fatal(err)
	}
	for _, score := range []float64{3, 1, 2} {
		s.Add(map[string]any{"g": "a", "s": score, "t": "x"})
	}
	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 group, got %d", len(snap))
	}
	rows := snap[0].Rows
	if len(rows) != 3 {
		t.Fatalf("want all 3 rows, got %d", len(rows))
	}
	for i, want := range []float64{3, 2, 1} {
		if rows[i]["s"] != want {
			t.Fatalf("row %d: want score %v, got %v", i, want, rows[i]["s"])
		}
	}
}

func TestKeepsTopNByScoreDescending(t *testing.T) {
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	for _, score := range []float64{1, 9, 5, 3, 7} {
		s.Add(map[string]any{"g": "a", "s": score, "t": "x"})
	}
	rows := s.Snapshot()[0].Rows
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if rows[0]["s"] != 9.0 || rows[1]["s"] != 7.0 {
		t.Fatalf("want [9 7], got [%v %v]", rows[0]["s"], rows[1]["s"])
	}
	held, groups := s.Stats()
	if held != 2 || groups != 1 {
		t.Fatalf("Stats: want (2,1), got (%d,%d)", held, groups)
	}
	if s.Processed() != 5 {
		t.Fatalf("Processed: want 5, got %d", s.Processed())
	}
}
