package dbscan

import (
	"reflect"
	"testing"
)

func ev(t EventType, olds, news []int) Event {
	return Event{Type: t, OldLabels: olds, NewLabels: news}
}

func normalizeChanges(c []Change) []Change {
	if c == nil {
		return []Change{}
	}
	return c
}

func normalizeEvents(evs []Event) []Event {
	if evs == nil {
		return []Event{}
	}
	out := make([]Event, len(evs))
	for i, e := range evs {
		if e.OldLabels == nil {
			e.OldLabels = []int{}
		}
		if e.NewLabels == nil {
			e.NewLabels = []int{}
		}
		out[i] = e
	}
	return out
}

func assertChanges(t *testing.T, got, want []Change) {
	t.Helper()
	if !reflect.DeepEqual(normalizeChanges(got), normalizeChanges(want)) {
		t.Fatalf("changes mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func assertEvents(t *testing.T, got, want []Event) {
	t.Helper()
	if !reflect.DeepEqual(normalizeEvents(got), normalizeEvents(want)) {
		t.Fatalf("events mismatch:\n got: %+v\nwant: %+v", got, want)
	}
}

func mustInsert(t *testing.T, d *DBSCAN, id, x, y int) Result {
	t.Helper()
	res, err := d.Insert(id, x, y)
	if err != nil {
		t.Fatalf("Insert(%d,%d,%d) unexpected error: %v", id, x, y, err)
	}
	return res
}

func mustRemove(t *testing.T, d *DBSCAN, id int) Result {
	t.Helper()
	res, err := d.Remove(id)
	if err != nil {
		t.Fatalf("Remove(%d) unexpected error: %v", id, err)
	}
	return res
}

func mustTick(t *testing.T, d *DBSCAN, tm int64) Result {
	t.Helper()
	res, err := d.Tick(tm)
	if err != nil {
		t.Fatalf("Tick(%d) unexpected error: %v", tm, err)
	}
	return res
}

func newTestDB(t *testing.T, eps, minPts int, w int64, c int) *DBSCAN {
	t.Helper()
	d, err := New(eps, minPts, w, c)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d) unexpected error: %v", eps, minPts, w, c, err)
	}
	return d
}

// TestSpecExample replays the full scenario from the specification.
func TestSpecExample(t *testing.T) {
	d := newTestDB(t, 2, 3, 10, 100)

	res := mustInsert(t, d, 11, 0, 0)
	assertChanges(t, res.Changes, []Change{{11, -1, 0}})
	assertEvents(t, res.Events, nil)

	res = mustInsert(t, d, 12, 2, 0)
	assertChanges(t, res.Changes, []Change{{12, -1, 0}})

	res = mustInsert(t, d, 13, 4, 0)
	assertChanges(t, res.Changes, []Change{{11, 0, 12}, {12, 0, 12}, {13, -1, 12}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{12})})

	for _, id := range []int{14, 15, 16, 17} {
		res = mustInsert(t, d, id, 2*(id-11), 0)
		assertChanges(t, res.Changes, []Change{{id, -1, 12}})
		assertEvents(t, res.Events, nil)
	}
	wantClusters := []Cluster{{Label: 12, Members: []int{11, 12, 13, 14, 15, 16, 17}}}
	if got := d.Clusters(); !reflect.DeepEqual(got, wantClusters) {
		t.Fatalf("clusters mismatch:\n got: %+v\nwant: %+v", got, wantClusters)
	}

	res = mustRemove(t, d, 14)
	assertChanges(t, res.Changes, []Change{{14, 12, -1}, {15, 12, 16}, {16, 12, 16}, {17, 12, 16}})
	assertEvents(t, res.Events, []Event{ev(Split, []int{12}, []int{12, 16})})

	res = mustTick(t, d, 4)
	assertChanges(t, res.Changes, nil)
	assertEvents(t, res.Events, nil)

	res = mustInsert(t, d, 18, 6, 0)
	assertChanges(t, res.Changes, []Change{{15, 16, 12}, {16, 16, 12}, {17, 16, 12}, {18, -1, 12}})
	assertEvents(t, res.Events, []Event{ev(Merge, []int{12, 16}, []int{12})})

	res = mustTick(t, d, 10)
	assertChanges(t, res.Changes, []Change{
		{11, 12, -1}, {12, 12, -1}, {13, 12, -1}, {15, 12, -1}, {16, 12, -1}, {17, 12, -1}, {18, 12, 0},
	})
	assertEvents(t, res.Events, []Event{ev(Death, []int{12}, nil)})

	res = mustTick(t, d, 13)
	assertChanges(t, res.Changes, nil)

	res = mustTick(t, d, 14)
	assertChanges(t, res.Changes, []Change{{18, 0, -1}})
	assertEvents(t, res.Events, nil)
	if d.Alive() != 0 {
		t.Fatalf("Alive() = %d, want 0", d.Alive())
	}
}
