package search

import (
	"errors"
	"fmt"
	"testing"
)

func dd(id string, v int64) Doc { return Doc{ID: id, SortVal: v} }

func newExampleEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(2)
	if _, err := e.AddSegment(0, []Doc{dd("a", 5), dd("b", 7)}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AddSegment(0, []Doc{dd("c", 5), dd("d", 9)}); err != nil {
		t.Fatal(err)
	}
	return e
}

func allPages(t *testing.T, e *Engine, pid, size int) []Entry {
	t.Helper()
	var out []Entry
	var after *Key
	for {
		page, err := e.Search(e.Manager().Now(), pid, size, after, 0)
		if err != nil {
			t.Fatalf("page: %v", err)
		}
		out = append(out, page...)
		if len(page) < size {
			break
		}
		last := page[len(page)-1].Key
		after = &last
	}
	return out
}

func entryIDs(es []Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

func TestExampleFromSpec(t *testing.T) {
	e := newExampleEngine(t)
	p, err := e.Open(10, 20)
	if err != nil || p.Exp != 30 {
		t.Fatalf("open=%+v err=%v", p, err)
	}
	if err := e.Delete(11, "b"); err != nil {
		t.Fatal(err)
	}
	if id, err := e.Merge(12, []int{1, 2}); err != nil || id != 3 {
		t.Fatalf("merge=%d err=%v", id, err)
	}

	page, err := e.Search(15, 1, 2, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryIDs(page); fmt.Sprint(got) != fmt.Sprint([]string{"a", "c"}) {
		t.Fatalf("page1=%v", got)
	}
	if page[0].Key != (Key{SortVal: 5, Seg: 1, Idx: 0}) ||
		page[1].Key != (Key{SortVal: 5, Seg: 2, Idx: 0}) {
		t.Fatalf("page1 keys=%+v", page)
	}
	last := page[1].Key
	page2, err := e.Search(15, 1, 2, &last, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryIDs(page2); fmt.Sprint(got) != fmt.Sprint([]string{"b", "d"}) {
		t.Fatalf("page2=%v", got)
	}

	cur, err := e.Search(15, 0, 10, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := entryIDs(cur); fmt.Sprint(got) != fmt.Sprint([]string{"a", "c", "d"}) {
		t.Fatalf("current=%v", got)
	}
	wantKeys := []Key{{SortVal: 5, Seg: 3, Idx: 0}, {SortVal: 5, Seg: 3, Idx: 1}, {SortVal: 9, Seg: 3, Idx: 2}}
	for i := range cur {
		if cur[i].Key != wantKeys[i] {
			t.Fatalf("current key[%d]=%+v want %+v", i, cur[i].Key, wantKeys[i])
		}
	}
}

func TestPaginationEquivalence(t *testing.T) {
	e := newExampleEngine(t)
	p, _ := e.Open(0, 100)
	_ = e.Delete(1, "b")
	_, _ = e.Merge(2, []int{1, 2})

	full, err := e.Search(50, p.ID, 1000, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{1, 2, 3, 4, 7} {
		got := allPages(t, e, p.ID, size)
		if len(got) != len(full) {
			t.Fatalf("size=%d len=%d want %d", size, len(got), len(full))
		}
		for i := range got {
			if got[i] != full[i] {
				t.Fatalf("size=%d [%d] got=%+v want=%+v", size, i, got[i], full[i])
			}
		}
	}

	if _, err := e.Search(50, 0, 10, &Key{SortVal: 1, Seg: 1, Idx: 0}, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("after with pid=0 got %v", err)
	}
	if _, err := e.Search(50, 0, 10, nil, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ka with pid=0 got %v", err)
	}
	if _, err := e.Search(50, p.ID, 0, nil, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size=0 got %v", err)
	}
	if _, err := e.Search(50, p.ID, 1001, nil, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size=1001 got %v", err)
	}
}

func TestExpiryAndCloseRelease(t *testing.T) {
	e := newExampleEngine(t)
	_, _ = e.Open(10, 20)
	_ = e.Delete(11, "b")
	if _, err := e.Merge(12, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Search(29, 1, 1, nil, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Search(38, 1, 1, nil, 0); err != nil {
		t.Fatalf("alive at 38: %v", err)
	}
	if _, err := e.Search(39, 1, 1, nil, 0); !errors.Is(err, ErrPITNotFound) {
		t.Fatalf("at exp: %v", err)
	}
	if rel := e.Released(); len(rel) != 2 {
		t.Fatalf("released on expiry: %v", rel)
	}

	e2 := newExampleEngine(t)
	_, _ = e2.Open(10, 20)
	_ = e2.Delete(11, "b")
	if _, err := e2.Merge(12, []int{1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := e2.Close(20, 1); err != nil {
		t.Fatal(err)
	}
	if rel := e2.Released(); len(rel) != 2 || rel[0] != 1 || rel[1] != 2 {
		t.Fatalf("close release: %v", rel)
	}
}

func TestRenewalOnlyExtends(t *testing.T) {
	e := newExampleEngine(t)
	p, _ := e.Open(10, 20)                                   // exp 30
	if _, err := e.Search(11, p.ID, 1, nil, 5); err != nil { // exp 30
		t.Fatal(err)
	}
	if _, err := e.Search(12, p.ID, 1, nil, 100); err != nil { // exp 112
		t.Fatal(err)
	}
	if _, err := e.Search(20, p.ID, 1, nil, 1); err != nil { // max(112,21)=112
		t.Fatal(err)
	}
	if _, err := e.Search(112, p.ID, 1, nil, 0); !errors.Is(err, ErrPITNotFound) {
		t.Fatalf("exp 112 boundary: %v", err)
	}
}
