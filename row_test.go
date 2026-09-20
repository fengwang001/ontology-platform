package ontology

import "testing"

// 同名非连接键属性：左表值不被覆盖，右表值落在 "right." 前缀下。
func TestSameNameAttrNotOverwritten(t *testing.T) {
	left := []map[string]any{{"id": 1, "name": "left-name", "lv": 10}}
	right := []map[string]any{{"id": 1, "name": "right-name", "rv": 20}}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1", len(out))
	}
	row := out[0]
	if row["name"] != "left-name" {
		t.Fatalf("left name overwritten: %v", row["name"])
	}
	if row["right.name"] != "right-name" {
		t.Fatalf("right name missing at right.name: %v", row)
	}
	if row["lv"] != 10 || row["right.rv"] != 20 {
		t.Fatalf("attrs wrong: %v", row)
	}
	// 连接键只出现一次（左侧原名），不生成 right.id。
	if _, ok := row["right.id"]; ok {
		t.Fatalf("join key leaked as right.id: %v", row)
	}
	if row["id"] != 1 {
		t.Fatalf("join key = %v", row["id"])
	}
}

// Left 未匹配行的右侧属性可辨认为"缺失"而非零值。
func TestLeftUnmatchedRightMissing(t *testing.T) {
	left := []map[string]any{{"id": 1, "name": "l"}}
	right := []map[string]any{{"id": 2, "name": "r", "score": 0}}
	out, _, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("rows = %d, want 1", len(out))
	}
	row := out[0]
	if _, ok := RightValue(row, "name"); ok {
		t.Fatalf("right.name should be missing: %v", row)
	}
	if _, ok := RightValue(row, "score"); ok {
		t.Fatalf("right.score should be missing: %v", row)
	}
	// 缺失不是零值：map 里根本没有这些键。
	for k := range row {
		if k == RightPrefix+"name" || k == RightPrefix+"score" {
			t.Fatalf("unexpected right attr %s in %v", k, row)
		}
	}
	// 匹配行则能用 RightValue 取到，包括零值 0。
	out, _, err = Join(
		[]map[string]any{{"id": 2}},
		[]map[string]any{{"id": 2, "score": 0}}, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	v, ok := RightValue(out[0], "score")
	if !ok || v != 0 {
		t.Fatalf("RightValue = %v,%v, want 0,true", v, ok)
	}
}

// 结果与输入双向隔离：改结果不影响输入，改输入不影响已产出结果。
func TestResultInputIsolation(t *testing.T) {
	left := []map[string]any{
		{"id": 1, "tags": []any{"a"}, "meta": map[string]any{"x": 1}},
	}
	right := []map[string]any{
		{"id": 1, "note": "n", "tags": []any{"b"}},
	}
	out, _, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	row := out[0]
	// 改结果（含嵌套结构）不影响输入。
	row["id"] = 999
	row["tags"].([]any)[0] = "mutated"
	row["meta"].(map[string]any)["x"] = -1
	row["right.tags"].([]any)[0] = "mutated"
	if left[0]["id"] != 1 || left[0]["tags"].([]any)[0] != "a" ||
		left[0]["meta"].(map[string]any)["x"] != 1 {
		t.Fatalf("left input mutated: %v", left[0])
	}
	if right[0]["tags"].([]any)[0] != "b" {
		t.Fatalf("right input mutated: %v", right[0])
	}
	// 改输入不影响已产出的结果。
	left[0]["id"] = -1
	left[0]["tags"].([]any)[0] = "changed"
	right[0]["note"] = "changed"
	if out[0]["id"] != 999 || out[0]["tags"].([]any)[0] != "mutated" ||
		out[0]["right.note"] != "n" {
		t.Fatalf("result changed after input mutation: %v", out[0])
	}
}

// 输入行本身不被 Join 修改。
func TestJoinDoesNotMutateInputs(t *testing.T) {
	left := []map[string]any{{"id": 1, "v": "a"}}
	right := []map[string]any{{"id": 1, "v": "b"}}
	if _, _, err := Join(left, right, []string{"id"}, Left); err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(left[0]) != 2 || len(right[0]) != 2 {
		t.Fatalf("inputs mutated: %v %v", left[0], right[0])
	}
	if _, ok := right[0][RightPrefix+"v"]; ok {
		t.Fatalf("right input gained prefixed key: %v", right[0])
	}
}
