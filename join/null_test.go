package join

import (
	"math"
	"testing"
)

func TestInnerNilKeyNeverMatches(t *testing.T) {
	left := []Row{
		{"id": nil, "v": "l-nil"},
		{"v": "l-missing"}, // 键属性不存在
		{"id": 1, "v": "l-1"},
	}
	right := []Row{
		{"id": nil, "v": "r-nil"},
		{"v": "r-missing"},
		{"id": 1, "v": "r-1"},
	}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("inner rows = %d, want 1 (only id=1 pair)", len(res.Rows))
	}
	if res.Rows[0]["v"] != "l-1" || res.Rows[0]["right.v"] != "r-1" {
		t.Fatalf("unexpected matched row: %v", res.Rows[0])
	}
	if res.Stats.LeftUnmatchedNull != 2 {
		t.Fatalf("LeftUnmatchedNull = %d, want 2", res.Stats.LeftUnmatchedNull)
	}
}

func TestLeftKeepsNullKeyRows(t *testing.T) {
	left := []Row{
		{"id": nil, "v": "l-nil"},
		{"v": "l-missing"},
		{"id": 1, "v": "l-1"},
		{"id": 2, "v": "l-2-nomatch"},
	}
	right := []Row{{"id": 1, "v": "r-1"}}
	res, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 4 {
		t.Fatalf("left rows = %d, want 4", len(res.Rows))
	}
	if res.Stats.LeftUnmatchedNull != 2 {
		t.Fatalf("LeftUnmatchedNull = %d, want 2", res.Stats.LeftUnmatchedNull)
	}
	if res.Stats.LeftUnmatchedNoMatch != 1 {
		t.Fatalf("LeftUnmatchedNoMatch = %d, want 1", res.Stats.LeftUnmatchedNoMatch)
	}
	if res.Stats.OutputRows != 4 || res.Stats.MatchedPairs != 1 {
		t.Fatalf("stats = %+v", res.Stats)
	}
}

func TestUnmatchedCountsAreSeparate(t *testing.T) {
	left := []Row{
		{"id": nil},        // 空键
		{"name": "x"},      // 空键（缺失）
		{"id": math.NaN()}, // 空键（NaN）
		{"id": 10},         // 有值无匹配
		{"id": 20},         // 有值无匹配
		{"id": 1},          // 匹配
	}
	right := []Row{{"id": 1}, {"id": nil}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.LeftUnmatchedNull != 3 {
		t.Fatalf("LeftUnmatchedNull = %d, want 3", res.Stats.LeftUnmatchedNull)
	}
	if res.Stats.LeftUnmatchedNoMatch != 2 {
		t.Fatalf("LeftUnmatchedNoMatch = %d, want 2", res.Stats.LeftUnmatchedNoMatch)
	}
	if res.Stats.MatchedPairs != 1 {
		t.Fatalf("MatchedPairs = %d, want 1", res.Stats.MatchedPairs)
	}
}

func TestNaNKeyCountsAsNull(t *testing.T) {
	left := []Row{{"id": math.NaN(), "v": "nan"}}
	right := []Row{{"id": math.NaN()}, {"id": 1.5}}
	res, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.LeftUnmatchedNull != 1 || res.Stats.MatchedPairs != 0 {
		t.Fatalf("stats = %+v, want 1 null-unmatched and 0 pairs", res.Stats)
	}
}

func TestMultiKeyAnyNullFailsMatch(t *testing.T) {
	left := []Row{{"a": 1, "b": nil, "v": "l"}}
	right := []Row{{"a": 1, "b": nil, "v": "r"}}
	res, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || res.Stats.LeftUnmatchedNull != 1 {
		t.Fatalf("rows=%d stats=%+v, want 0 rows and 1 null-unmatched", len(res.Rows), res.Stats)
	}
}
