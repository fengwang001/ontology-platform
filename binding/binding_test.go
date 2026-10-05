package binding

import "testing"

func TestNewValidation(t *testing.T) {
	if _, err := New(0); err != ErrInvalid {
		t.Fatalf("New(0): %v", err)
	}
	if _, err := New(MaxQ + 1); err != ErrInvalid {
		t.Fatalf("New(MaxQ+1): %v", err)
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1): %v", err)
	}
}

func TestTableOwnersAndCounts(t *testing.T) {
	tb, err := New(3)
	if err != nil {
		t.Fatal(err)
	}
	tb.Create("f1", 100)
	tb.AddOwner("f1", 1, Owner{})
	tb.AddOwner("f1", 2, Owner{})
	tb.Create("f2", 101)
	tb.AddOwner("f2", 1, Owner{})
	if got := tb.Count(1); got != 2 {
		t.Fatalf("Count(1)=%d, want 2", got)
	}
	// 陈旧标记与二次标记不延长截止。
	if !tb.MarkStale("f1", 1, 70) {
		t.Fatal("MarkStale should mark normal owner")
	}
	if tb.MarkStale("f1", 1, 90) {
		t.Fatal("MarkStale must not re-mark stale owner")
	}
	e, _ := tb.Get("f1")
	if o := e.Owners[1]; !o.Stale || o.Deadline != 70 {
		t.Fatalf("owner=%+v, want stale deadline 70", o)
	}
	tb.SetNormal("f1", 1)
	if o := e.Owners[1]; o.Stale {
		t.Fatal("SetNormal should clear stale")
	}
	// 占位属主不计数、不索引。
	tb.AddPlaceholder("f2", 110)
	if got := tb.Count(Placeholder); got != 0 {
		t.Fatalf("placeholder counted: %d", got)
	}
	if len(tb.FecsOf(Placeholder)) != 0 {
		t.Fatal("placeholder must not be indexed")
	}
	// 移除属主直到删空。
	if _, last, _ := tb.RemoveOwner("f1", 1); last {
		t.Fatal("f1 still has owner 2")
	}
	label, last, ok := tb.RemoveOwner("f1", 2)
	if !ok || !last || label != 100 {
		t.Fatalf("RemoveOwner last: (%d,%v,%v)", label, last, ok)
	}
	if _, ok := tb.Get("f1"); ok {
		t.Fatal("f1 should be released")
	}
	if got := tb.Count(1); got != 1 {
		t.Fatalf("Count(1)=%d, want 1", got)
	}
	// FecsOf 快照。
	fecs := tb.FecsOf(1)
	if len(fecs) != 1 || fecs[0] != "f2" {
		t.Fatalf("FecsOf(1)=%v", fecs)
	}
}
