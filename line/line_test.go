package line

import "testing"

func co(m map[[2]string]int) ChangeoverFunc {
	return func(a, b string) int { return m[[2]string{a, b}] }
}

func TestRecalcBasic(t *testing.T) {
	ln := New(0, "A", co(map[[2]string]int{
		{"A", "B"}: 20,
		{"B", "A"}: 15,
	}))
	orders := []*Order{
		{ID: "W1", Family: "A", Duration: 50, Earliest: 30},
		{ID: "W2", Family: "B", Duration: 40, Earliest: 30},
		{ID: "W3", Family: "A", Duration: 30, Earliest: 30},
	}
	for _, o := range orders {
		ln.InsertAt(ln.Len(), o)
	}

	n := ln.RecalcFrom(0)
	if n != 3 {
		t.Fatalf("recalc count = %d, want 3", n)
	}
	want := []struct{ start, end int }{{30, 80}, {100, 140}, {155, 185}}
	for i, w := range want {
		o := ln.At(i)
		if o.Start != w.start || o.End != w.end {
			t.Errorf("%s = (%d,%d), want (%d,%d)", o.ID, o.Start, o.End, w.start, w.end)
		}
	}
}

func TestRecalcFromMiddle(t *testing.T) {
	ln := New(100, "A", co(map[[2]string]int{{"A", "B"}: 10}))
	o1 := &Order{ID: "X", Family: "A", Duration: 5, Earliest: 0}
	o2 := &Order{ID: "Y", Family: "A", Duration: 7, Earliest: 0}
	ln.InsertAt(0, o1)
	ln.InsertAt(1, o2)
	ln.RecalcFrom(0)

	// 在中间插入 B 族工单，只重算它自身与其后工单。
	nb := &Order{ID: "M", Family: "B", Duration: 3, Earliest: 0}
	ln.InsertAt(1, nb)
	if n := ln.RecalcFrom(1); n != 2 {
		t.Fatalf("recalc count = %d, want 2", n)
	}
	if o1.Start != 100 || o1.End != 105 {
		t.Errorf("frozen-prefix order changed: (%d,%d)", o1.Start, o1.End)
	}
	if nb.Start != 115 || nb.End != 118 { // 105 + 10
		t.Errorf("M = (%d,%d), want (115,118)", nb.Start, nb.End)
	}
	if o2.Start != 118 || o2.End != 125 {
		t.Errorf("Y = (%d,%d), want (118,125)", o2.Start, o2.End)
	}
}

func TestEarliestCapsStart(t *testing.T) {
	ln := New(0, "A", co(nil))
	o1 := &Order{ID: "A1", Family: "A", Duration: 10, Earliest: 0}
	o2 := &Order{ID: "A2", Family: "A", Duration: 10, Earliest: 100}
	ln.InsertAt(0, o1)
	ln.InsertAt(1, o2)
	ln.RecalcFrom(0)
	if o2.Start != 100 || o2.End != 110 {
		t.Fatalf("A2 = (%d,%d), want (100,110)", o2.Start, o2.End)
	}

	// Clone 深拷贝隔离。
	cp := ln.Clone()
	cp.At(0).Start = 999
	if ln.At(0).Start != 0 {
		t.Fatal("Clone did not isolate order data")
	}
}

func TestInsertRemoveAndIndex(t *testing.T) {
	ln := New(0, "A", co(nil))
	ln.InsertAt(0, &Order{ID: "a"})
	ln.InsertAt(1, &Order{ID: "b"})
	ln.InsertAt(1, &Order{ID: "c"})
	if got := []string{ln.At(0).ID, ln.At(1).ID, ln.At(2).ID}; got[0] != "a" || got[1] != "c" || got[2] != "b" {
		t.Fatalf("order = %v, want [a c b]", got)
	}
	if ln.IndexOf("c") != 1 || ln.IndexOf("z") != -1 {
		t.Fatal("IndexOf wrong")
	}
	ln.RemoveAt(1)
	if ln.Len() != 2 || ln.At(1).ID != "b" {
		t.Fatal("RemoveAt wrong")
	}
}
