package line_test

import "testing"

import "ontology/line"

func coOf(table map[[2]string]int64) line.ChangeoverFunc {
	return func(a, b string) int64 { return table[[2]string{a, b}] }
}

func TestInsertOrderAndTimes(t *testing.T) {
	table := map[[2]string]int64{{"A", "B"}: 20, {"B", "A"}: 15}
	ln := line.New(0, "A", coOf(table))

	w1, p1 := ln.Insert("W1", "A", 50, 100, 0, 30)
	if p1 != 0 || w1.Start != 30 || w1.End != 80 {
		t.Fatalf("W1 pos=%d start=%d end=%d, want 0/30/80", p1, w1.Start, w1.End)
	}
	w2, p2 := ln.Insert("W2", "B", 40, 200, 0, 30)
	if p2 != 1 || w2.Start != 100 || w2.End != 140 {
		t.Fatalf("W2 pos=%d start=%d end=%d, want 1/100/140", p2, w2.Start, w2.End)
	}
	// due 相等：接受序号靠后，排在 W2 之前。
	w3, p3 := ln.Insert("W3", "A", 30, 150, 0, 30)
	if p3 != 1 || w3.Start != 80 || w3.End != 110 {
		t.Fatalf("W3 pos=%d start=%d end=%d, want 1/80/110", p3, w3.Start, w3.End)
	}
	if got := ln.List()[2]; got.ID != "W2" || got.Start != 130 || got.End != 170 {
		t.Fatalf("W2 after push: %+v, want start=130 end=170", got)
	}
	if got := ln.RecalcCount(); got != 2 {
		t.Fatalf("recalc=%d, want 2 (W3 and W2 only)", got)
	}
}

func TestEarliestCapsStart(t *testing.T) {
	ln := line.New(0, "A", coOf(nil))
	_, _ = ln.Insert("W1", "A", 10, 100, 0, 0) // 0..10
	w2, _ := ln.Insert("W2", "A", 10, 100, 0, 50)
	if w2.Start != 50 || w2.End != 60 {
		t.Fatalf("W2 start=%d end=%d, want 50/60", w2.Start, w2.End)
	}
	// 前驱改变后重算仍受自身 earliest 限制。
	ln.RemoveAt(0)
	if got, _ := ln.Get("W2"); got.Start != 50 || got.End != 60 {
		t.Fatalf("W2 after remove: start=%d end=%d, want 50/60", got.Start, got.End)
	}
}

func TestFreezePrefix(t *testing.T) {
	ln := line.New(0, "A", coOf(nil))
	ln.Insert("W1", "A", 50, 100, 0, 30) // 30..80
	ln.Insert("W2", "A", 20, 100, 0, 30) // 80..100

	ln.AdvanceFrozen(80) // now+F=80：start 恰等于 80 不冻结
	if ln.Frozen() != 1 {
		t.Fatalf("frozen=%d, want 1", ln.Frozen())
	}
	ln.AdvanceFrozen(81) // 小 1 的边界推进即冻结
	if ln.Frozen() != 2 {
		t.Fatalf("frozen=%d, want 2", ln.Frozen())
	}
}

func TestRemoveAndRecalc(t *testing.T) {
	table := map[[2]string]int64{{"A", "B"}: 20, {"B", "A"}: 15}
	ln := line.New(0, "A", coOf(table))
	ln.Insert("W1", "A", 50, 500, 0, 0) // 0..50
	ln.Insert("W2", "B", 10, 500, 0, 0) // 70..80
	ln.Insert("W3", "A", 10, 500, 0, 0) // 95..105
	ln.RemoveAt(1)                      // 移除 W2：W3 的换型变成 A->A=0，且 earliest=0
	if ids := []string{ln.List()[0].ID, ln.List()[1].ID}; ids[0] != "W1" || ids[1] != "W3" {
		t.Fatalf("ids=%v, want [W1 W3]", ids)
	}
	w3, _ := ln.Get("W3")
	if w3.Start != 50 || w3.End != 60 {
		t.Fatalf("W3 start=%d end=%d, want 50/60", w3.Start, w3.End)
	}
	if ln.RecalcCount() != 1 {
		t.Fatalf("recalc=%d, want 1 (only W3)", ln.RecalcCount())
	}
}

func TestCloneIsolation(t *testing.T) {
	ln := line.New(0, "A", coOf(nil))
	ln.Insert("W1", "A", 10, 100, 0, 0)
	cp := ln.Clone()
	cp.Insert("WX", "A", 10, 100, 0, 0)
	if cp.Len() != 2 || ln.Len() != 1 {
		t.Fatalf("clone isolation broken: cp=%d ln=%d", cp.Len(), ln.Len())
	}
	if cp.Has("WX") == ln.Has("WX") {
		t.Fatal("trial insert leaked into original")
	}
}
