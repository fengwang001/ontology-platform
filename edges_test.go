package ontology

import (
	"errors"
	"math"
	"testing"
)

func testConfig(n int) Config {
	return Config{N: n, GroupColumn: "g", ScoreColumn: "s", TieColumn: "t"}
}

func TestInvalidN(t *testing.T) {
	for _, n := range []int{0, -1, -100} {
		_, err := New(testConfig(n))
		if !errors.Is(err, ErrInvalidN) {
			t.Fatalf("N=%d: want ErrInvalidN, got %v", n, err)
		}
	}
	if _, err := New(testConfig(1)); err != nil {
		t.Fatalf("N=1: unexpected error %v", err)
	}
}

func TestGroupSmallerThanNReturnsAll(t *testing.T) {
	s, err := New(testConfig(5))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.Add(map[string]any{"g": "a", "s": float64(i), "t": "x"})
	}
	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 group, got %d", len(snap))
	}
	if len(snap[0].Rows) != 3 {
		t.Fatalf("want all 3 rows, got %d", len(snap[0].Rows))
	}
	// 仍按复合次序：分数降序。
	for i := 0; i < 3; i++ {
		if snap[0].Rows[i].Score != float64(2-i) {
			t.Fatalf("row %d: want score %d, got %v", i, 2-i, snap[0].Rows[i].Score)
		}
	}
}

func TestThreeEmptyGroupKeysAreDistinct(t *testing.T) {
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"s": 1.0, "t": "a"})              // 缺失
	s.Add(map[string]any{"g": nil, "s": 2.0, "t": "b"})    // nil
	s.Add(map[string]any{"g": "", "s": 3.0, "t": "c"})     // 空字符串
	s.Add(map[string]any{"g": "real", "s": 4.0, "t": "d"}) // 普通

	snap := s.Snapshot()
	if len(snap) != 4 {
		t.Fatalf("want 4 groups, got %d", len(snap))
	}
	wantClass := []KeyClass{KeyMissing, KeyNil, KeyEmpty, KeyValue}
	for i, gs := range snap {
		if gs.Key.Class != wantClass[i] {
			t.Fatalf("group %d: want class %v, got %v", i, wantClass[i], gs.Key.Class)
		}
		if len(gs.Rows) != 1 {
			t.Fatalf("group %d: want 1 row, got %d", i, len(gs.Rows))
		}
	}
	// 三个空键组可分别辨认。
	if snap[0].Key.String() == snap[1].Key.String() ||
		snap[1].Key.String() == snap[2].Key.String() ||
		snap[0].Key.String() == snap[2].Key.String() {
		t.Fatal("empty-key groups are not distinguishable")
	}
}

func TestSkippedAndNaNCounts(t *testing.T) {
	s, err := New(testConfig(3))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "t": "x"})             // 分数缺失
	s.Add(map[string]any{"g": "a", "s": "bad", "t": "x"}) // 非数值
	s.Add(map[string]any{"g": "a", "s": nil, "t": "x"})   // nil 分数
	s.Add(map[string]any{"g": "a", "s": math.NaN(), "t": "x"})
	s.Add(map[string]any{"g": "a", "s": math.NaN(), "t": "y"})
	s.Add(map[string]any{"g": "a", "s": 1.5, "t": "z"}) // 正常行

	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 group, got %d", len(snap))
	}
	g := snap[0]
	if g.Skipped != 3 {
		t.Fatalf("want Skipped=3, got %d", g.Skipped)
	}
	if g.NaN != 2 {
		t.Fatalf("want NaN=2, got %d", g.NaN)
	}
	if len(g.Rows) != 1 || g.Rows[0].Score != 1.5 {
		t.Fatalf("NaN/skipped rows polluted ranking: %+v", g.Rows)
	}
	if s.Processed() != 6 {
		t.Fatalf("want Processed=6, got %d", s.Processed())
	}
}

func TestInfinityScoresAreValid(t *testing.T) {
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "s": math.Inf(-1), "t": "neg"})
	s.Add(map[string]any{"g": "a", "s": math.Inf(1), "t": "pos"})
	s.Add(map[string]any{"g": "a", "s": 1e300, "t": "big"})

	snap := s.Snapshot()
	rows := snap[0].Rows
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	if !math.IsInf(rows[0].Score, 1) {
		t.Fatalf("want +Inf first, got %v", rows[0].Score)
	}
	if rows[1].Score != 1e300 {
		t.Fatalf("want 1e300 second, got %v", rows[1].Score)
	}
}
