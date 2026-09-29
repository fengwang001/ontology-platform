package merger

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutputOrderIsFirstAppearance(t *testing.T) {
	cols := []string{"a", "b"}
	tbl := NewTable(cols, &bytes.Buffer{})
	_, err := tbl.Commit([]Event{
		{Type: Insert, Key: "b", Columns: map[string]ColumnValue{"a": String("1"), "b": String("1")}},
		{Type: Insert, Key: "a", Columns: map[string]ColumnValue{"a": String("1"), "b": String("1")}},
		{Type: Insert, Key: "c", Columns: map[string]ColumnValue{"a": String("1"), "b": String("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tbl.Commit([]Event{
		{Type: Update, Key: "c", Columns: map[string]ColumnValue{"a": String("2")},
			Before: map[string]ColumnValue{"a": String("1")}},
		{Type: Update, Key: "a", Columns: map[string]ColumnValue{"a": String("2")},
			Before: map[string]ColumnValue{"a": String("1")}},
		{Type: Update, Key: "c", Columns: map[string]ColumnValue{"b": String("2")},
			Before: map[string]ColumnValue{"b": String("1")}},
		{Type: Update, Key: "b", Columns: map[string]ColumnValue{"a": String("2")},
			Before: map[string]ColumnValue{"a": String("1")}},
		{Type: Update, Key: "a", Columns: map[string]ColumnValue{"b": String("2")},
			Before: map[string]ColumnValue{"b": String("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got := []string{}
	for _, o := range res.Outputs {
		got = append(got, o.Key)
	}
	want := []string{"c", "a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// 首次出现顺序在同一批内以插入开始时也成立。
	tbl2 := NewTable(cols, &bytes.Buffer{})
	res2, err := tbl2.Commit([]Event{
		{Type: Insert, Key: "x", Columns: map[string]ColumnValue{"a": String("1"), "b": String("1")}},
		{Type: Insert, Key: "y", Columns: map[string]ColumnValue{"a": String("1"), "b": String("1")}},
		{Type: Update, Key: "x", Columns: map[string]ColumnValue{"a": String("2")},
			Before: map[string]ColumnValue{"a": String("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if k0, k1 := res2.Outputs[0].Key, res2.Outputs[1].Key; k0 != "x" || k1 != "y" {
		t.Fatalf("order = %s,%s want x,y", k0, k1)
	}
	for i := range res2.Outputs {
		if res2.Outputs[i].Order != i {
			t.Fatalf("order field = %d want %d", res2.Outputs[i].Order, i)
		}
	}
}

func TestLogShowsInputsMergesAndDecisions(t *testing.T) {
	var log bytes.Buffer
	tbl := NewTable([]string{"a", "b"}, &log)
	_, err := tbl.Commit([]Event{
		{Type: Insert, Key: "k", Columns: map[string]ColumnValue{"a": String("1"), "b": Null()}},
		{Type: Update, Key: "k",
			Columns: map[string]ColumnValue{"a": String("2")},
			Before:  map[string]ColumnValue{"a": String("1")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	body := log.String()
	for _, want := range []string{
		"event[0]", "event[1]", // 每步输入
		"merged key=",           // 合并结果
		"=> insert",             // 插入接更新判定
		"first update: before=", // 判定依据
		"committed",
		`<null>`, // 三态渲染（空串在后续用例验证）
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("log missing %q:\n%s", want, body)
		}
	}

	var log2 bytes.Buffer
	tbl2 := NewTable([]string{"a"}, &log2)
	tbl2.rows = map[string]map[string]ColumnValue{"k": {"a": String("0")}}
	_, err = tbl2.Commit([]Event{{
		Type:    Update,
		Key:     "k",
		Columns: map[string]ColumnValue{"a": String("0")},
		Before:  map[string]ColumnValue{"a": String("0")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log2.String(), "dropped") || !strings.Contains(log2.String(), "emptied") {
		t.Fatalf("log must explain dropping:\n%s", log2.String())
	}

	var log3 bytes.Buffer
	tbl3 := NewTable([]string{"a"}, &log3)
	_, err = tbl3.Commit([]Event{{
		Type:    Insert,
		Key:     "k",
		Columns: map[string]ColumnValue{"a": String("")},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log3.String(), `a=""`) {
		t.Fatalf("empty string must render distinctly from null:\n%s", log3.String())
	}

	_, err = tbl2.Commit([]Event{{
		Type:    Update,
		Key:     "ghost",
		Columns: map[string]ColumnValue{"a": String("0")},
		Before:  map[string]ColumnValue{"a": String("0")},
	}})
	if err == nil || !strings.Contains(log2.String(), "REJECTED") {
		t.Fatalf("rejection must be logged: %v", err)
	}
}
