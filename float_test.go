package ontology

import (
	"math"
	"reflect"
	"testing"
)

// NaN 必须被拒绝：不参与排名、计入跳过计数、不影响容量。
func TestNaNRejectedAndCounted(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 10)
	sel.Push("nan-1", math.NaN())
	sel.Push("nan-2", math.NaN())
	sel.Push("b", 5)
	// NaN 即使"分数看起来极小"也不能顶掉正常元素：
	// 先喂两个 NaN 占名义序列，再来正常元素仍应正常入选。
	sel.Push("c", 7)

	if got, want := sel.Skipped(), 2; got != want {
		t.Fatalf("skipped: got %d want %d", got, want)
	}
	if got := ids(sel.Snapshot()); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("snapshot: got %v want [a c]", got)
	}
	if sel.Len() != 2 {
		t.Fatalf("len: got %d want 2", sel.Len())
	}
}

// NaN 覆盖已有 ID 时必须被整体拒绝：旧分数保留，跳过计数 +1。
func TestNaNOverwriteKeepsOldScore(t *testing.T) {
	sel, _ := New(2, Desc)
	sel.Push("a", 9)
	sel.Push("b", 4)
	sel.Push("a", math.NaN())

	if sel.Skipped() != 1 {
		t.Fatalf("skipped: got %d want 1", sel.Skipped())
	}
	snap := sel.Snapshot()
	if len(snap) != 2 || snap[0].ID != "a" || snap[0].Score != 9 {
		t.Fatalf("old score must survive NaN overwrite: %+v", snap)
	}
}

// +0.0 与 -0.0 视为相等，走 ID 升序；正负无穷正常参与排名。
func TestSignedZeroTiesAndInfinities(t *testing.T) {
	sel, _ := New(4, Desc)
	sel.Push("negzero", math.Copysign(0, -1))
	sel.Push("poszero", 0)
	sel.Push("posinf", math.Inf(1))
	sel.Push("neginf", math.Inf(-1))
	got := ids(sel.Snapshot())
	// Desc: +Inf 最前；两个 0 并列按 ID（negzero < poszero）；-Inf 最后。
	want := []string{"posinf", "negzero", "poszero", "neginf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("signed-zero order: got %v want %v", got, want)
	}
}

// Asc 方向下 ±0 同样视为相等并列；负无穷排在最前。
func TestSignedZeroTiesAscending(t *testing.T) {
	sel, _ := New(4, Asc)
	sel.Push("zpos", 0)
	sel.Push("aneg", math.Copysign(0, -1))
	sel.Push("posinf", math.Inf(1))
	sel.Push("neginf", math.Inf(-1))
	got := ids(sel.Snapshot())
	want := []string{"neginf", "aneg", "zpos", "posinf"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("asc signed-zero order: got %v want %v", got, want)
	}
}
