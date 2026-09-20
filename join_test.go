package ontology

import "testing"

// 两侧同为 nil 的键在 Inner 下不匹配。
func TestInnerNullKeyNeverMatches(t *testing.T) {
	left := []map[string]any{
		{"id": nil, "v": "l-nil"},
		{"v": "l-missing"}, // 键属性不存在
		{"id": 1, "v": "l-one"},
	}
	right := []map[string]any{
		{"id": nil, "w": "r-nil"},
		{"w": "r-missing"},
		{"id": 1, "w": "r-one"},
	}
	out, stats, err := Join(left, right, []string{"id"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("Inner rows = %d, want 1 (only id=1 pair)", len(out))
	}
	if out[0]["v"] != "l-one" || out[0]["right.w"] != "r-one" {
		t.Fatalf("unexpected row: %v", out[0])
	}
	if stats.LeftNullKeyRows != 2 {
		t.Fatalf("LeftNullKeyRows = %d, want 2 (nil 与缺失都算空)", stats.LeftNullKeyRows)
	}
}

// Left 下空键左行照常输出，且右侧属性缺失。
func TestLeftKeepsNullKeyRows(t *testing.T) {
	left := []map[string]any{
		{"id": nil, "v": "l-nil"},
		{"v": "l-missing"},
		{"id": 7, "v": "l-seven"},
	}
	right := []map[string]any{{"id": 7, "w": "r-seven"}}
	out, stats, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("Left rows = %d, want 3", len(out))
	}
	// 空键左行排在非空键分组之后。
	if out[0]["v"] != "l-seven" {
		t.Fatalf("first row = %v, want matched l-seven", out[0])
	}
	nullRows := map[string]bool{out[1]["v"].(string): true, out[2]["v"].(string): true}
	if !nullRows["l-nil"] || !nullRows["l-missing"] {
		t.Fatalf("null-key rows not preserved: %v, %v", out[1], out[2])
	}
	if stats.LeftNullKeyRows != 2 || stats.LeftUnmatchedRows != 0 {
		t.Fatalf("stats = %+v, want null=2 unmatched=0", stats)
	}
}

// 两类未匹配计数分开：空键一类、有值无对应一类。
func TestUnmatchedCountersSeparated(t *testing.T) {
	left := []map[string]any{
		{"id": nil},       // 空键
		{"name": "no-id"}, // 空键（缺失）
		{"id": 1},         // 有值，右表没有
		{"id": 2},         // 有值，右表没有
		{"id": 3},         // 有值，能匹配
	}
	right := []map[string]any{{"id": 3}, {"id": 4}}
	_, stats, err := Join(left, right, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if stats.LeftNullKeyRows != 2 {
		t.Fatalf("LeftNullKeyRows = %d, want 2", stats.LeftNullKeyRows)
	}
	if stats.LeftUnmatchedRows != 2 {
		t.Fatalf("LeftUnmatchedRows = %d, want 2", stats.LeftUnmatchedRows)
	}
	if stats.MatchedPairs != 1 || stats.OutputRows != 5 {
		t.Fatalf("stats = %+v, want pairs=1 output=5", stats)
	}
	if stats.RightUnmatchedRows != 1 {
		t.Fatalf("RightUnmatchedRows = %d, want 1", stats.RightUnmatchedRows)
	}
}

// 多键场景：任一键为空即整行键为空。
func TestMultiKeyNull(t *testing.T) {
	left := []map[string]any{
		{"a": 1, "b": "x"},
		{"a": 1}, // b 缺失
		{"a": nil, "b": "x"},
	}
	right := []map[string]any{{"a": 1, "b": "x"}}
	out, stats, err := Join(left, right, []string{"a", "b"}, Inner)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 1 || stats.LeftNullKeyRows != 2 {
		t.Fatalf("rows=%d null=%d, want 1/2", len(out), stats.LeftNullKeyRows)
	}
}

// 空表与全空键的边界。
func TestEmptyAndAllNull(t *testing.T) {
	out, stats, err := Join(nil, nil, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 0 || stats.OutputRows != 0 {
		t.Fatalf("empty join produced %d rows", len(out))
	}
	left := []map[string]any{{"id": nil}, {"id": nil}}
	out, stats, err = Join(left, nil, []string{"id"}, Left)
	if err != nil {
		t.Fatalf("Join: %v", err)
	}
	if len(out) != 2 || stats.LeftNullKeyRows != 2 || stats.LeftUnmatchedRows != 0 {
		t.Fatalf("rows=%d stats=%+v", len(out), stats)
	}
}
