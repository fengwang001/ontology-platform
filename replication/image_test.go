package replication

import "testing"

func TestRowsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b Row
		want bool
	}{
		{"both nil", nil, nil, true},
		{"nil vs empty", nil, Row{}, true}, // 都是空列集合；行是否存在由 map 查找而非镜像判等决定
		{"empty vs nil", Row{}, nil, true},
		{"identical", row("a", "1", "b", "2"), row("b", "2", "a", "1"), true},
		{"missing column vs empty string", row("a", "1"), row("a", "1", "b", ""), false},
		{"empty string equal", row("a", ""), row("a", ""), true},
		{"value differs", row("a", "1"), row("a", "2"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rowsEqual(tc.a, tc.b); got != tc.want {
				t.Fatalf("rowsEqual(%v,%v)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// firstDiff 必须按列名字典序稳定地报告第一处差异，且区分缺列与空串。
func TestFirstDiffStableAndDistinguishesMissing(t *testing.T) {
	d := firstDiff(row("z", "1", "a", ""), row("z", "1", "a", "x"))
	if d == nil || d.column != "a" || d.kind != diffValueMismatch {
		t.Fatalf("want value mismatch on a, got %+v", d)
	}

	d = firstDiff(row("a", "1"), row("a", "1", "b", ""))
	if d == nil || d.column != "b" || d.kind != diffMissingInBefore || d.before != missingMarker {
		t.Fatalf("want missing-in-before on b, got %+v", d)
	}

	d = firstDiff(row("a", "1", "b", ""), row("a", "1"))
	if d == nil || d.column != "b" || d.kind != diffMissingInCurrent || d.current != missingMarker {
		t.Fatalf("want missing-in-current on b, got %+v", d)
	}

	if firstDiff(row("a", ""), row("a", "")) != nil {
		t.Fatalf("empty-string columns should be equal")
	}

	// 差异描述中空串显示为 ""，缺列显示为 <missing>。
	desc := describeDiff(firstDiff(row("a", "1"), row("a", "1", "b", "")))
	if !contains(desc, `""`) || !contains(desc, missingMarker) {
		t.Fatalf("basis must distinguish missing from empty string, got: %s", desc)
	}
}

func TestValidateEventTable(t *testing.T) {
	valid := []Event{
		{Seq: 1, Op: OpInsert, Key: "k", Before: nil, After: row("a", "1")},
		{Seq: 2, Op: OpUpdate, Key: "k", Before: row("a", "1"), After: row("a", "2")},
		{Seq: 3, Op: OpDelete, Key: "k", Before: row("a", "2"), After: nil},
	}
	for _, e := range valid {
		if r := validateEvent(e); r != "" {
			t.Fatalf("event should be valid: %+v reason=%s", e, r)
		}
	}
	invalid := []Event{
		{Seq: 0, Op: OpInsert, Key: "k", After: row("a", "1")},
		{Seq: 1, Op: OpInsert, Key: "", After: row("a", "1")},
		{Seq: 1, Op: "X", Key: "k", After: row("a", "1")},
		{Seq: 1, Op: OpInsert, Key: "k", Before: Row{}, After: row("a", "1")}, // Before 非 nil
		{Seq: 1, Op: OpInsert, Key: "k", After: nil},
		{Seq: 1, Op: OpUpdate, Key: "k", Before: nil, After: row("a", "1")},
		{Seq: 1, Op: OpUpdate, Key: "k", Before: row("a", "1"), After: nil},
		{Seq: 1, Op: OpDelete, Key: "k", Before: nil, After: nil},
		{Seq: 1, Op: OpDelete, Key: "k", Before: row("a", "1"), After: Row{}}, // After 非 nil
		{Seq: 1, Op: OpInsert, Key: "k", After: row("", "1")},                 // 空列名
	}
	for i, e := range invalid {
		if r := validateEvent(e); r == "" {
			t.Fatalf("invalid case %d should be rejected: %+v", i, e)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
