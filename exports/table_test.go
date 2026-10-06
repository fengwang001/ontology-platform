package exports

import "testing"

func mustTable(t *testing.T, entries ...Entry) *Table {
	t.Helper()
	tbl, err := NewTable(entries)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tbl
}

func TestTableValidation(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
	}{
		{"key not starting with dot", []Entry{{Key: "x", Target: StringTarget("./x.js")}}},
		{"key bare slash", []Entry{{Key: "/x", Target: StringTarget("./x.js")}}},
		{"key two stars", []Entry{{Key: "./a*b*c", Target: StringTarget("./x.js")}}},
		{"duplicate key", []Entry{
			{Key: "./a", Target: StringTarget("./x.js")},
			{Key: "./a", Target: StringTarget("./y.js")},
		}},
		{"empty condition name", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("", StringTarget("./x.js")))}}},
		{"all-digit condition name", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("123", StringTarget("./x.js")))}}},
		{"default not last", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("default", StringTarget("./x.js")),
			Cond("browser", StringTarget("./y.js")))}}},
		{"duplicate condition name", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("browser", StringTarget("./x.js")),
			Cond("browser", StringTarget("./y.js")))}}},
		{"empty conditions map", []Entry{{Key: "./a", Target: ConditionsTarget()}}},
		{"nested empty conditions map", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("browser", ConditionsTarget()))}}},
		{"nested all-digit condition", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("browser", ConditionsTarget(Cond("42", StringTarget("./x.js")))))}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewTable(tc.entries)
			if !IsKind(err, KindInvalidTable) {
				t.Fatalf("want KindInvalidTable, got %v", err)
			}
			t.Logf("entries=%+v => err=%v", tc.entries, err)
		})
	}
}

func TestValidTables(t *testing.T) {
	cases := []struct {
		name    string
		entries []Entry
	}{
		{"dot key", []Entry{{Key: ".", Target: StringTarget("./main.js")}}},
		{"default last", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("browser", StringTarget("./b.js")),
			Cond("default", StringTarget("./d.js")))}}},
		{"digit-prefixed condition", []Entry{{Key: "./a", Target: ConditionsTarget(
			Cond("1st", StringTarget("./x.js")))}}},
		{"star key", []Entry{{Key: "./*.js", Target: StringTarget("./dist/*.js")}}},
		{"string target not validated at construction", []Entry{
			{Key: "./a", Target: StringTarget("../escape.js")}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTable(tc.entries); err != nil {
				t.Fatalf("want valid table, got %v", err)
			}
		})
	}
}

func TestRequestValidation(t *testing.T) {
	tbl := mustTable(t, Entry{Key: "./a", Target: StringTarget("./a.js")})
	cases := []struct {
		name    string
		subpath string
		conds   []string
	}{
		{"no dot prefix", "a", nil},
		{"bare slash", "/a", nil},
		{"star in subpath", "./a*", nil},
		{"empty condition name", "./a", []string{"browser", ""}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tbl.Resolve(tc.subpath, tc.conds)
			if !IsKind(err, KindInvalidRequest) {
				t.Fatalf("want KindInvalidRequest, got %v", err)
			}
			t.Logf("subpath=%q conds=%v => err=%v", tc.subpath, tc.conds, err)
		})
	}
}

func TestInvalidRequestBeatsNotExported(t *testing.T) {
	tbl := mustTable(t, Entry{Key: "./a", Target: StringTarget("./a.js")})
	// 子路径既不合法也未导出：请求非法优先。
	_, err := tbl.Resolve("zzz", nil)
	if !IsKind(err, KindInvalidRequest) {
		t.Fatalf("want KindInvalidRequest, got %v", err)
	}
}
