package pcol

import (
	"maps"
	"strconv"
	"testing"
)

func TestCheckFormat(t *testing.T) {
	cols := map[string]bool{"a": true, "b": true}
	full := map[string]Value{"a": Str("1"), "b": Null()}
	upd := func(set, before map[string]Value) Event {
		return Event{Kind: Update, Key: "k", Set: set, Before: before}
	}
	cases := []struct {
		name string
		ev   Event
		want error
	}{
		{"insert ok", Event{Kind: Insert, Key: "k", Set: full}, nil},
		{"insert missing col", Event{Kind: Insert, Key: "k", Set: map[string]Value{"a": Str("1")}}, ErrBadColumn},
		{"insert with before", Event{Kind: Insert, Key: "k", Set: full, Before: map[string]Value{"a": Str("1")}}, ErrBadColumn},
		{"insert unknown col", Event{Kind: Insert, Key: "k", Set: map[string]Value{"a": Str("1"), "b": Null(), "zz": Null()}}, ErrBadColumn},
		{"update ok", upd(map[string]Value{"a": Str("2")}, map[string]Value{"a": Str("1")}), nil},
		{"update empty set", upd(map[string]Value{}, map[string]Value{}), ErrBadColumn},
		{"update set/before cols differ", upd(map[string]Value{"a": Str("2")}, map[string]Value{"b": Str("1")}), ErrBadColumn},
		{"update unknown col", upd(map[string]Value{"zz": Str("2")}, map[string]Value{"zz": Str("1")}), ErrBadColumn},
	}
	for _, c := range cases {
		if got := CheckFormat(c.ev, cols); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMergeSemantics(t *testing.T) {
	upd := func(set, before map[string]Value) Event {
		return Event{Kind: Update, Key: "k", Set: set, Before: before}
	}
	cases := []struct {
		name       string
		acc, ev    Event
		wantSet    map[string]Value
		wantBefore map[string]Value
	}{
		{"union, later value wins, before keeps first",
			upd(map[string]Value{"a": Str("1")}, map[string]Value{"a": Str("0")}),
			upd(map[string]Value{"a": Str("2"), "b": Null()}, map[string]Value{"a": Str("1"), "b": Str("x")}),
			map[string]Value{"a": Str("2"), "b": Null()}, map[string]Value{"a": Str("0"), "b": Str("x")}},
		{"insert absorbs update, stays insert",
			Event{Kind: Insert, Key: "k", Set: map[string]Value{"a": Str("1"), "b": Str("x")}},
			upd(map[string]Value{"b": Null()}, map[string]Value{"b": Str("x")}),
			map[string]Value{"a": Str("1"), "b": Null()}, nil},
		{"value returns to before: column eliminated",
			upd(map[string]Value{"a": Str("1")}, map[string]Value{"a": Str("0")}),
			upd(map[string]Value{"a": Str("0")}, map[string]Value{"a": Str("1")}),
			map[string]Value{}, map[string]Value{}},
		{"null equals null: eliminated",
			upd(map[string]Value{"a": Str("1")}, map[string]Value{"a": Null()}),
			upd(map[string]Value{"a": Null()}, map[string]Value{"a": Str("1")}),
			map[string]Value{}, map[string]Value{}},
		{"null differs from empty string: kept",
			upd(map[string]Value{"a": Str("1")}, map[string]Value{"a": Str("")}),
			upd(map[string]Value{"a": Null()}, map[string]Value{"a": Str("1")}),
			map[string]Value{"a": Null()}, map[string]Value{"a": Str("")}},
	}
	for _, c := range cases {
		var mg Merger
		got := mg.Merge(c.acc, c.ev)
		if !maps.Equal(got.Set, c.wantSet) || !maps.Equal(got.Before, c.wantBefore) {
			t.Errorf("%s: got %v/%v, want %v/%v", c.name, got.Set, got.Before, c.wantSet, c.wantBefore)
		}
		if c.acc.Kind == Insert && got.Kind != Insert {
			t.Errorf("%s: insert kind not preserved", c.name)
		}
	}
}

// 第二次合并只触及本次事件的列：检查列数不随 m 增长。
func TestMergeCheckedColumns(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		acc := Event{Kind: Update, Key: "k", Set: map[string]Value{}, Before: map[string]Value{}}
		for i := 0; i < m; i++ {
			c := "c" + strconv.Itoa(i)
			acc.Set[c], acc.Before[c] = Str("1"), Str("0")
		}
		ev := Event{Kind: Update, Key: "k", Set: map[string]Value{"c0": Str("2")}, Before: map[string]Value{"c0": Str("1")}}
		var mg Merger
		mg.Merge(acc, ev)
		if mg.checked > 4 { // 1 列事件：合并检查 1 次 + 剔除检查 1 次，与 m 无关
			t.Errorf("m=%d: checked %d columns, want <= 4", m, mg.checked)
		}
	}
}
