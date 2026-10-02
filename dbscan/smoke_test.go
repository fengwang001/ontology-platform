package dbscan

import (
	"reflect"
	"testing"
)

func chgs(c ...Change) []Change { return c }

func TestSpecExample(t *testing.T) {
	s, err := New(2, 3, 10, 1000)
	if err != nil {
		t.Fatal(err)
	}
	must := func(r *Result, e error, want []Change, wantEvents []Event) {
		t.Helper()
		if e != nil {
			t.Fatalf("unexpected error: %v", e)
		}
		if !reflect.DeepEqual(r.Changes, want) {
			t.Fatalf("changes=%v want %v", r.Changes, want)
		}
		if !reflect.DeepEqual(r.Events, wantEvents) {
			t.Fatalf("events=%v want %v", r.Events, wantEvents)
		}
	}
	r, _ := s.Insert(11, 0, 0)
	must(r, nil, chgs(Change{11, -1, 0}), nil)
	r, _ = s.Insert(12, 2, 0)
	must(r, nil, chgs(Change{12, -1, 0}), nil)
	r, _ = s.Insert(13, 4, 0)
	must(r, nil,
		chgs(Change{11, 0, 12}, Change{12, 0, 12}, Change{13, -1, 12}),
		[]Event{{Type: EventBirth, OldLabels: nil, NewLabels: []int64{12}}})
	for _, id := range []int64{14, 15, 16, 17} {
		r, _ = s.Insert(id, (id-11)*2, 0)
		must(r, nil, chgs(Change{id, -1, 12}), nil)
	}
	r, _ = s.Remove(14)
	must(r, nil,
		chgs(Change{14, 12, -1}, Change{15, 12, 16}, Change{16, 12, 16}, Change{17, 12, 16}),
		[]Event{{Type: EventSplit, OldLabels: []int64{12}, NewLabels: []int64{12, 16}}})
	r, _ = s.Tick(4)
	must(r, nil, []Change{}, nil)
	r, _ = s.Insert(18, 6, 0)
	must(r, nil,
		chgs(Change{15, 16, 12}, Change{16, 16, 12}, Change{17, 16, 12}, Change{18, -1, 12}),
		[]Event{{Type: EventMerge, OldLabels: []int64{12, 16}, NewLabels: []int64{12}}})
	r, _ = s.Tick(10)
	must(r, nil,
		chgs(
			Change{11, 12, -1}, Change{12, 12, -1}, Change{13, 12, -1},
			Change{15, 12, -1}, Change{16, 12, -1}, Change{17, 12, -1},
			Change{18, 12, 0},
		),
		[]Event{{Type: EventDeath, OldLabels: []int64{12}, NewLabels: nil}})
	r, _ = s.Tick(13)
	must(r, nil, []Change{}, nil)
	r, _ = s.Tick(14)
	must(r, nil, chgs(Change{18, 0, -1}), nil)
	if s.Alive() != 0 {
		t.Fatalf("alive=%d", s.Alive())
	}
}
