package row

import (
	"strconv"
	"testing"

	"ontology/cell"
)

// TestCellTie：cell 层 LWW 与平局裁定、冲突检测（表驱动）。
func TestCellTie(t *testing.T) {
	val := cell.Cell{Set: true, TS: 5, Val: "a"}
	tom := cell.Cell{Set: true, Tomb: true, TS: 5}
	cases := []struct {
		name            string
		cur             cell.Cell
		ts              int64
		v               string
		del             bool
		wantV           string
		wantTom, wantCf bool
	}{
		{"newer wins", val, 6, "b", false, "b", false, false},
		{"older ignored", val, 4, "b", false, "a", false, false},
		{"tie larger val", val, 5, "b", false, "b", false, true},
		{"tie smaller val", val, 5, "0", false, "a", false, true},
		{"tie same val idempotent", val, 5, "a", false, "a", false, false},
		{"tie tomb beats val", val, 5, "", true, "", true, true},
		{"tie val loses to tomb", tom, 5, "x", false, "", true, true},
		{"tie tombs equivalent", tom, 5, "", true, "", true, false},
		{"tomb hides older", tom, 4, "x", false, "", true, false},
		{"newer val beats tomb", tom, 6, "x", false, "x", false, false},
	}
	for _, c := range cases {
		got, cf := cell.Apply(c.cur, c.ts, c.v, c.del)
		if got.Val != c.wantV || got.Tomb != c.wantTom || cf != c.wantCf {
			t.Errorf("%s: got (%q,tomb=%v,conf=%v)", c.name, got.Val, got.Tomb, cf)
		}
	}
}

// TestColumnIsolation：对一列的写/删只影响该列，兄弟列不变。
func TestColumnIsolation(t *testing.T) {
	r := New()
	r.Put("a", 1, "x")
	r.Put("b", 2, "y")
	r.Del("a", 3)
	v := r.View()
	if _, ok := v["a"]; ok {
		t.Fatalf("a should be absent after delete, got %v", v)
	}
	if v["b"] != "y" {
		t.Fatalf("sibling b mutated: got %q", v["b"])
	}
	r.Put("a", 4, "z") // 墓碑 TS=3 不压 TS=4 的新值
	if v := r.View(); v["a"] != "z" || v["b"] != "y" {
		t.Fatalf("post-tombstone put wrong: %v", v)
	}
}

// TestLocateConstantInM：建 m 列后再写其中一列，定位检查个数必须是与 m 无关的小常数。
func TestLocateConstantInM(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			r.Put("c"+strconv.Itoa(i), 1, "v")
		}
		r.checked = 0
		r.Put("c0", 2, "w")
		if r.checked > 2 || r.checked < 1 {
			t.Fatalf("m=%d: checked %d, want constant in [1,2]", m, r.checked)
		}
		r.Del("c"+strconv.Itoa(m-1), 3)
		if r.checked > 2 {
			t.Fatalf("m=%d: Del checked %d, want constant", m, r.checked)
		}
	}
}
