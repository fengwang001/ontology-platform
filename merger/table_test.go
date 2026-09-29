package merger

import (
	"bytes"
	"testing"
)

func TestNewTableRejectsBadSchema(t *testing.T) {
	for _, cols := range [][]string{
		{"a", ""},
		{"a", "a"},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("schema %v should panic", cols)
				}
			}()
			NewTable(cols, nil)
		}()
	}
}

func TestSelfCheckDetectsCorruption(t *testing.T) {
	tbl := NewTable([]string{"a", "b"}, nil)
	tbl.rows = map[string]map[string]ColumnValue{
		"bad": {"a": String("1")}, // 缺列
	}
	if err := tbl.SelfCheck(); err == nil {
		t.Fatal("self-check should flag missing column")
	}

	tbl.rows = map[string]map[string]ColumnValue{
		"bad": {"a": String("1"), "b": Absent()}, // 列存在但值为缺席
	}
	if err := tbl.SelfCheck(); err == nil {
		t.Fatal("self-check should flag absent stored value")
	}

	tbl2 := NewTable([]string{"a", "b"}, &bytes.Buffer{})
	if err := tbl2.SelfCheck(); err != nil {
		t.Fatalf("empty table self-check: %v", err)
	}
	if _, ok := tbl2.Get("nope"); ok {
		t.Fatal("missing key should return false")
	}

	if _, err := tbl2.Commit(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
}
