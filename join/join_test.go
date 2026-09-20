package join_test

import (
	"fmt"
	"testing"

	"ontology/join"
)

func TestInnerNullKeyBothSidesNoMatch(t *testing.T) {
	left := []join.Row{{"id": nil, "lv": "L"}}
	right := []join.Row{{"id": nil, "rv": "R"}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("nil key must not match nil key, got %v", res.Rows)
	}
	if res.Stats.LeftNullKeyRows != 1 || res.Stats.RightNullKeyRows != 1 {
		t.Fatalf("null key counts wrong: %+v", res.Stats)
	}
}

func TestMissingAndNilKeyBothNull(t *testing.T) {
	left := []join.Row{
		{"lv": "no-key"},       // 键属性缺失
		{"id": nil, "lv": "n"}, // 键为 nil
	}
	right := []join.Row{{"id": nil}, {"other": 1}}
	res, err := join.Join(left, right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.LeftNullKeyRows != 2 {
		t.Fatalf("missing and nil keys both count as null: %+v", res.Stats)
	}
	if res.Stats.OutputRows != 2 {
		t.Fatalf("left mode must keep null-key rows: %+v", res.Stats)
	}
}

func TestUnmatchedCountsSeparated(t *testing.T) {
	left := []join.Row{
		{"id": nil, "lv": "null-key"},
		{"id": int64(1), "lv": "no-match"},
		{"id": int64(2), "lv": "hit"},
	}
	right := []join.Row{{"id": int64(2), "rv": "R"}}
	res, err := join.Join(left, right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	s := res.Stats
	if s.LeftNullKeyRows != 1 || s.LeftUnmatchedRows != 1 || s.MatchedRows != 1 {
		t.Fatalf("counts not separated: %+v", s)
	}
	if s.OutputRows != 3 {
		t.Fatalf("left mode output = matched + null + unmatched, got %+v", s)
	}
}

func TestExpansionMN(t *testing.T) {
	// 三个键，规模 (m,n) = (100,20)、(50,30)、(7,11)，外加无匹配行。
	sizes := [][2]int{{100, 20}, {50, 30}, {7, 11}}
	var left, right []join.Row
	wantSum, wantMax := 0, 0
	for i, mn := range sizes {
		key := fmt.Sprintf("k%d", i)
		for j := 0; j < mn[0]; j++ {
			left = append(left, join.Row{"id": key, "lv": j})
		}
		for j := 0; j < mn[1]; j++ {
			right = append(right, join.Row{"id": key, "rv": j})
		}
		wantSum += mn[0] * mn[1]
		if mn[0]*mn[1] > wantMax {
			wantMax = mn[0] * mn[1]
		}
	}
	left = append(left, join.Row{"id": "orphan"})

	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	if res.Stats.MatchedRows != wantSum || res.Stats.OutputRows != wantSum {
		t.Fatalf("expansion sum: want %d, got %+v", wantSum, res.Stats)
	}
	if res.Stats.MaxKeyExpansion != wantMax {
		t.Fatalf("max expansion: want %d, got %d", wantMax, res.Stats.MaxKeyExpansion)
	}
	if res.Stats.LeftUnmatchedRows != 1 {
		t.Fatalf("orphan row: %+v", res.Stats)
	}
	// 逐键核对每个左行恰好展开 n 次。
	seen := map[string]int{}
	for _, r := range res.Rows {
		seen[fmt.Sprintf("%v/%v", r["id"], r["lv"])]++
	}
	for i, mn := range sizes {
		for j := 0; j < mn[0]; j++ {
			k := fmt.Sprintf("k%d/%d", i, j)
			if seen[k] != mn[1] {
				t.Fatalf("pair %s expanded %d times, want %d", k, seen[k], mn[1])
			}
		}
	}
}

func TestLeftUnmatchedRightSideMissing(t *testing.T) {
	left := []join.Row{{"id": int64(1), "lv": "L"}}
	right := []join.Row{{"id": int64(2), "rv": "R"}}
	res, err := join.Join(left, right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row, got %v", res.Rows)
	}
	row := res.Rows[0]
	if row["lv"] != "L" {
		t.Fatalf("left attribute lost: %v", row)
	}
	if join.Has(row, "rv") {
		t.Fatalf("right attribute must be missing, not zero: %v", row)
	}
	if v, ok := row["rv"]; ok || v != nil {
		t.Fatalf("comma-ok must report missing: v=%v ok=%v", v, ok)
	}
}

func TestSameNameNonKeyNotOverwritten(t *testing.T) {
	left := []join.Row{{"id": int64(1), "v": "left-v", "lo": 1}}
	right := []join.Row{{"id": int64(1), "v": "right-v", "ro": 2}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	if row["v"] != "left-v" {
		t.Fatalf("right side overwrote left: %v", row)
	}
	if row["right.v"] != "right-v" {
		t.Fatalf("conflicting right attr must be renamed right.v: %v", row)
	}
	if row["lo"] != 1 || row["ro"] != 2 {
		t.Fatalf("non-conflicting attrs must keep names: %v", row)
	}
	if row["id"] != int64(1) {
		t.Fatalf("join key kept once from left: %v", row)
	}
}

func TestResultInputIsolation(t *testing.T) {
	left := []join.Row{{
		"id":   int64(1),
		"nest": map[string]any{"x": "L"},
		"list": []any{"a"},
	}}
	right := []join.Row{{"id": int64(1), "nest": map[string]any{"x": "R"}}}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]

	// 改结果（含嵌套）不影响输入。
	row["nest"].(map[string]any)["x"] = "MUT"
	row["right.nest"].(map[string]any)["x"] = "MUT"
	row["list"].([]any)[0] = "MUT"
	row["new"] = 1
	if left[0]["nest"].(map[string]any)["x"] != "L" || right[0]["nest"].(map[string]any)["x"] != "R" {
		t.Fatal("mutating result leaked into input")
	}
	if left[0]["list"].([]any)[0] != "a" {
		t.Fatal("mutating result slice leaked into input")
	}

	// 改输入不影响已产出的结果。
	left[0]["nest"].(map[string]any)["x"] = "IN-MUT"
	right[0]["id"] = int64(99)
	if row["nest"].(map[string]any)["x"] != "MUT" || row["id"] != int64(1) {
		t.Fatal("mutating input leaked into result")
	}
}
