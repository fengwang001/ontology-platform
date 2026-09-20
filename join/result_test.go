package join

import (
	"testing"
)

func TestSameNameNonKeyAttrNotOverwritten(t *testing.T) {
	left := []Row{{"id": 1, "name": "left-name", "only_l": true}}
	right := []Row{{"id": 1, "name": "right-name", "only_r": true}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	row := res.Rows[0]
	if row["name"] != "left-name" {
		t.Fatalf("left attr overwritten: name = %v", row["name"])
	}
	if row["right.name"] != "right-name" {
		t.Fatalf("right attr missing: right.name = %v", row["right.name"])
	}
	if row["only_l"] != true || row["right.only_r"] != true {
		t.Fatalf("one-sided attrs wrong: %v", row)
	}
	if row["id"] != 1 {
		t.Fatalf("key attr = %v, want 1", row["id"])
	}
}

func TestLeftUnmatchedRightSideIsMissing(t *testing.T) {
	left := []Row{{"id": 9, "name": "l"}}
	right := []Row{{"id": 1, "name": "r", "extra": 5}}
	res, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(res.Rows))
	}
	row := res.Rows[0]
	if _, ok := row["right.name"]; ok {
		t.Fatalf("right.name present in unmatched row: %v", row)
	}
	if _, ok := row["right.extra"]; ok {
		t.Fatalf("right.extra present in unmatched row: %v", row)
	}
	if v, ok := row["name"]; !ok || v != "l" {
		t.Fatalf("left attr wrong: %v", row)
	}
	if len(row) != 2 { // 只有 id 和 name，绝不含右半侧零值
		t.Fatalf("unmatched row has %d attrs, want 2: %v", len(row), row)
	}
}

func TestResultDoesNotAliasInput(t *testing.T) {
	left := []Row{{
		"id":     1,
		"nested": map[string]any{"x": "lx"},
		"list":   []any{1, 2},
	}}
	right := []Row{{
		"id":     1,
		"nested": map[string]any{"y": "ry"},
	}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	// 修改结果（含嵌套结构）不得影响输入
	row["nested"].(map[string]any)["x"] = "MUTATED"
	row["list"].([]any)[0] = 999
	row["right.nested"].(map[string]any)["y"] = "MUTATED"
	row["id"] = 100
	if left[0]["nested"].(map[string]any)["x"] != "lx" {
		t.Fatal("mutating result nested map changed left input")
	}
	if left[0]["list"].([]any)[0] != 1 {
		t.Fatal("mutating result slice changed left input")
	}
	if right[0]["nested"].(map[string]any)["y"] != "ry" {
		t.Fatal("mutating result changed right input")
	}
	if left[0]["id"] != 1 {
		t.Fatal("mutating result key changed left input")
	}
}

func TestInputMutationAfterJoinDoesNotAffectResult(t *testing.T) {
	left := []Row{{"id": 1, "nested": map[string]any{"x": "lx"}}}
	right := []Row{{"id": 1, "v": "rv"}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	// 连接完成后修改输入，结果不得变化
	left[0]["nested"].(map[string]any)["x"] = "CHANGED"
	left[0]["id"] = 555
	right[0]["v"] = "CHANGED"
	row := res.Rows[0]
	if row["nested"].(map[string]any)["x"] != "lx" {
		t.Fatal("mutating input after join changed result nested value")
	}
	if row["id"] != 1 {
		t.Fatal("mutating input after join changed result key")
	}
	if row["right.v"] != "rv" {
		t.Fatal("mutating right input after join changed result")
	}
}

func TestRightPrefixCollisionResolution(t *testing.T) {
	// 左表已含 "right.x" 时，右表属性继续加前缀直到不冲突。
	left := []Row{{"id": 1, "x": "lx", "right.x": "taken"}}
	right := []Row{{"id": 1, "x": "rx"}}
	res, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatal(err)
	}
	row := res.Rows[0]
	if row["x"] != "lx" || row["right.x"] != "taken" || row["right.right.x"] != "rx" {
		t.Fatalf("prefix collision not resolved: %v", row)
	}
}
