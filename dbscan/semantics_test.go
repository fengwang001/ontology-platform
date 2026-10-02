package dbscan

import (
	"errors"
	"reflect"
	"testing"
)

// A point at distance exactly eps is a neighbor; one unit farther is not.
func TestEpsBoundary(t *testing.T) {
	d := newTestDB(t, 5, 2, 100, 10)

	res := mustInsert(t, d, 1, 0, 0)
	assertChanges(t, res.Changes, []Change{{1, -1, 0}})

	// dist^2 = 5^2+1 = 26 > 25: not a neighbor.
	res = mustInsert(t, d, 2, 5, 1)
	assertChanges(t, res.Changes, []Change{{2, -1, 0}})
	if got := d.Neighbors(1); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("Neighbors(1) = %v, want [1]", got)
	}

	// dist^2 = 3^2+4^2 = 25 = eps^2: neighbor, and it chains to 2 as well.
	res = mustInsert(t, d, 3, 3, 4)
	assertChanges(t, res.Changes, []Change{{1, 0, 1}, {2, 0, 1}, {3, -1, 1}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{1})})
	if got := d.Neighbors(1); !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("Neighbors(1) = %v, want [1 3]", got)
	}
	if got := d.Neighbors(2); !reflect.DeepEqual(got, []int{2, 3}) {
		t.Fatalf("Neighbors(2) = %v, want [2 3]", got)
	}
}

// |N(p)| == minPts makes a core point; one less does not.
func TestMinPtsBoundary(t *testing.T) {
	d := newTestDB(t, 2, 3, 100, 10)

	mustInsert(t, d, 1, 0, 0)
	res := mustInsert(t, d, 2, 1, 0)
	// |N(1)| = |N(2)| = 2 < 3: still noise.
	assertChanges(t, res.Changes, []Change{{2, -1, 0}})
	if d.Label(1) != 0 || d.Label(2) != 0 {
		t.Fatalf("labels = %d,%d, want 0,0", d.Label(1), d.Label(2))
	}

	res = mustInsert(t, d, 3, 0, 1)
	// Now |N| = 3 = minPts for all three: all become core.
	assertChanges(t, res.Changes, []Change{{1, 0, 1}, {2, 0, 1}, {3, -1, 1}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{1})})
}

// With minPts == 1 every point is a core point on its own.
func TestMinPtsOne(t *testing.T) {
	d := newTestDB(t, 2, 1, 100, 10)

	res := mustInsert(t, d, 1, 0, 0)
	assertChanges(t, res.Changes, []Change{{1, -1, 1}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{1})})

	res = mustInsert(t, d, 2, 100, 100)
	assertChanges(t, res.Changes, []Change{{2, -1, 2}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{2})})

	want := []Cluster{{Label: 1, Members: []int{1}}, {Label: 2, Members: []int{2}}}
	if got := d.Clusters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clusters() = %+v, want %+v", got, want)
	}
}

// An insertion that promotes a non-core point to core and bridges two
// clusters merges them under the smaller label.
func TestInsertBridgesClusters(t *testing.T) {
	d := newTestDB(t, 2, 3, 100, 20)

	for _, p := range [][3]int{{1, 0, 0}, {2, 1, 0}, {3, 2, 0}} {
		mustInsert(t, d, p[0], p[1], p[2])
	}
	for _, p := range [][3]int{{5, 6, 0}, {6, 7, 0}, {7, 8, 0}} {
		mustInsert(t, d, p[0], p[1], p[2])
	}
	if d.Label(1) != 1 || d.Label(5) != 5 {
		t.Fatalf("cluster labels = %d,%d, want 1,5", d.Label(1), d.Label(5))
	}

	// 4 sits next to core 3 but stays non-core (|N(4)| = 2 < 3).
	res := mustInsert(t, d, 4, 3, 0)
	assertChanges(t, res.Changes, []Change{{4, -1, 1}})
	assertEvents(t, res.Events, nil)

	// 8 promotes 4 to core and bridges clusters 1 and 5; the merged
	// cluster takes the smaller label 1.
	res = mustInsert(t, d, 8, 4, 0)
	assertChanges(t, res.Changes, []Change{{5, 5, 1}, {6, 5, 1}, {7, 5, 1}, {8, -1, 1}})
	assertEvents(t, res.Events, []Event{ev(Merge, []int{1, 5}, []int{1})})
	if d.Label(4) != 1 {
		t.Fatalf("Label(4) = %d, want 1", d.Label(4))
	}
}

// Removing a core point splits a chain cluster; each new cluster takes the
// smallest core id of its own part.
func TestSplitOnRemove(t *testing.T) {
	d := newTestDB(t, 2, 2, 100, 20)
	for i := 1; i <= 5; i++ {
		mustInsert(t, d, i, 2*(i-1), 0)
	}
	if d.Label(5) != 1 {
		t.Fatalf("Label(5) = %d, want 1", d.Label(5))
	}

	res := mustRemove(t, d, 3)
	assertChanges(t, res.Changes, []Change{{3, 1, -1}, {4, 1, 4}, {5, 1, 4}})
	assertEvents(t, res.Events, []Event{ev(Split, []int{1}, []int{1, 4})})
	want := []Cluster{
		{Label: 1, Members: []int{1, 2}},
		{Label: 4, Members: []int{4, 5}},
	}
	if got := d.Clusters(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clusters() = %+v, want %+v", got, want)
	}
}

// Expiration of a bridge point splits the cluster the same way a manual
// removal would.
func TestSplitOnExpiry(t *testing.T) {
	d := newTestDB(t, 2, 2, 10, 20)

	mustInsert(t, d, 3, 3, 0) // bridge, born at 0
	mustTick(t, d, 5)
	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustInsert(t, d, 4, 6, 0)
	mustInsert(t, d, 5, 7, 0)
	res := mustInsert(t, d, 6, 5, 0) // connects everything through 3
	assertEvents(t, res.Events, []Event{ev(Merge, []int{1, 4}, []int{1})})

	res = mustTick(t, d, 10) // only 3 (born 0) expires
	assertChanges(t, res.Changes, []Change{{3, 1, -1}, {4, 1, 4}, {5, 1, 4}, {6, 1, 4}})
	assertEvents(t, res.Events, []Event{ev(Split, []int{1}, []int{1, 4})})
}

// A border point adjacent to two clusters takes the smaller label, not the
// first-seen or nearest one.
func TestBorderTakesMinLabel(t *testing.T) {
	d := newTestDB(t, 3, 5, 100, 20)

	// Cluster 6 (plus shape around (7,0)) inserted first.
	for _, p := range [][3]int{{6, 7, 0}, {7, 8, 0}, {8, 6, 0}, {9, 7, 1}, {10, 7, -1}} {
		mustInsert(t, d, p[0], p[1], p[2])
	}
	// Cluster 1 (plus shape around (0,0)) inserted second.
	for _, p := range [][3]int{{1, 0, 0}, {2, 1, 0}, {3, -1, 0}, {4, 0, 1}, {5, 0, -1}} {
		mustInsert(t, d, p[0], p[1], p[2])
	}
	if d.Label(6) != 6 || d.Label(1) != 1 {
		t.Fatalf("labels = %d,%d, want 6,1", d.Label(6), d.Label(1))
	}

	// 11 is closer to cluster 6's cores (dist 2,3) than to cluster 1's
	// (dist 3), and cluster 6 was inserted first; it must still take 1.
	res := mustInsert(t, d, 11, 4, 0)
	assertChanges(t, res.Changes, []Change{{11, -1, 1}})
	assertEvents(t, res.Events, nil)
	if got := d.Neighbors(11); !reflect.DeepEqual(got, []int{2, 6, 8, 11}) {
		t.Fatalf("Neighbors(11) = %v, want [2 6 8 11]", got)
	}
}

// A border point becomes noise when the core points it depends on expire.
func TestBorderBecomesNoise(t *testing.T) {
	d := newTestDB(t, 2, 3, 10, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustInsert(t, d, 3, 0, 1)
	mustTick(t, d, 5)
	res := mustInsert(t, d, 4, 2, 0) // border of cluster 1
	assertChanges(t, res.Changes, []Change{{4, -1, 1}})

	res = mustTick(t, d, 10) // 1,2,3 expire; 4 stays alive but loses its core neighbor
	assertChanges(t, res.Changes, []Change{{1, 1, -1}, {2, 1, -1}, {3, 1, -1}, {4, 1, 0}})
	assertEvents(t, res.Events, []Event{ev(Death, []int{1}, nil)})
	if d.Alive() != 1 {
		t.Fatalf("Alive() = %d, want 1", d.Alive())
	}
}

// A point expires exactly at birth+W and is still alive one tick earlier.
func TestExpiryBoundary(t *testing.T) {
	d := newTestDB(t, 2, 1, 10, 10)

	res := mustInsert(t, d, 1, 0, 0)
	assertChanges(t, res.Changes, []Change{{1, -1, 1}})

	res = mustTick(t, d, 9)
	assertChanges(t, res.Changes, nil)
	if d.Alive() != 1 {
		t.Fatalf("Alive() at 9 = %d, want 1", d.Alive())
	}

	res = mustTick(t, d, 10)
	assertChanges(t, res.Changes, []Change{{1, 1, -1}})
	assertEvents(t, res.Events, []Event{ev(Death, []int{1}, nil)})
	if d.Alive() != 0 {
		t.Fatalf("Alive() at 10 = %d, want 0", d.Alive())
	}
}

// One Tick expiring several points reports exactly the before/after diff.
func TestTickBatchExpiry(t *testing.T) {
	d := newTestDB(t, 2, 2, 5, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustTick(t, d, 1)
	mustInsert(t, d, 3, 10, 0)
	mustInsert(t, d, 4, 11, 0)
	if d.Alive() != 4 {
		t.Fatalf("Alive() = %d, want 4", d.Alive())
	}

	res := mustTick(t, d, 5) // 1,2 expire together
	assertChanges(t, res.Changes, []Change{{1, 1, -1}, {2, 1, -1}})
	assertEvents(t, res.Events, []Event{ev(Death, []int{1}, nil)})
	if d.Label(3) != 3 || d.Label(4) != 3 {
		t.Fatalf("labels = %d,%d, want 3,3", d.Label(3), d.Label(4))
	}

	res = mustTick(t, d, 6) // 3,4 expire together
	assertChanges(t, res.Changes, []Change{{3, 3, -1}, {4, 3, -1}})
	assertEvents(t, res.Events, []Event{ev(Death, []int{3}, nil)})
}

// An id freed by Remove can be inserted again.
func TestReinsertAfterRemove(t *testing.T) {
	d := newTestDB(t, 2, 1, 100, 10)

	mustInsert(t, d, 1, 0, 0)
	res := mustRemove(t, d, 1)
	assertChanges(t, res.Changes, []Change{{1, 1, -1}})
	assertEvents(t, res.Events, []Event{ev(Death, []int{1}, nil)})

	res = mustInsert(t, d, 1, 100, 100)
	assertChanges(t, res.Changes, []Change{{1, -1, 1}})
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{1})})
	if got := d.Neighbors(1); !reflect.DeepEqual(got, []int{1}) {
		t.Fatalf("Neighbors(1) = %v, want [1]", got)
	}
}

// Capacity rejects inserts when full and recovers after a removal.
func TestCapacity(t *testing.T) {
	d := newTestDB(t, 2, 1, 100, 2)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 10, 0)

	if _, err := d.Insert(3, 20, 0); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("Insert into full: err = %v, want ErrCapacityFull", err)
	}
	// Validation order: invalid parameter and duplicate win over capacity.
	if _, err := d.Insert(3, 2_000_000, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Insert invalid+full: err = %v, want ErrInvalidParam", err)
	}
	if _, err := d.Insert(1, 20, 0); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("Insert duplicate+full: err = %v, want ErrDuplicateID", err)
	}

	mustRemove(t, d, 1)
	res := mustInsert(t, d, 3, 20, 0)
	assertChanges(t, res.Changes, []Change{{3, -1, 3}})
	if d.Alive() != 2 {
		t.Fatalf("Alive() = %d, want 2", d.Alive())
	}
}
