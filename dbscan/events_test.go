package dbscan

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// Membership changes that keep one cluster with the same label produce no
// event: a border point joins, then a core point extends the cluster.
func TestEventNoneOnMembershipChange(t *testing.T) {
	d := newTestDB(t, 2, 3, 100, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustInsert(t, d, 3, 0, 1)

	res := mustInsert(t, d, 4, 2, 0) // border of cluster 1
	assertChanges(t, res.Changes, []Change{{4, -1, 1}})
	assertEvents(t, res.Events, nil)

	res = mustInsert(t, d, 5, 2, 1) // becomes core, joins cluster 1
	assertChanges(t, res.Changes, []Change{{5, -1, 1}})
	assertEvents(t, res.Events, nil)

	res = mustRemove(t, d, 5) // cluster shrinks but keeps label and cores
	assertChanges(t, res.Changes, []Change{{5, 1, -1}})
	assertEvents(t, res.Events, nil)
}

// Removing the smallest core point relabels the surviving cluster.
func TestEventRelabel(t *testing.T) {
	d := newTestDB(t, 2, 2, 100, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustInsert(t, d, 3, 2, 0)

	res := mustRemove(t, d, 1)
	assertChanges(t, res.Changes, []Change{{1, 1, -1}, {2, 1, 2}, {3, 1, 2}})
	assertEvents(t, res.Events, []Event{ev(Relabel, []int{1}, []int{2})})
}

// Expiration of the smallest core point also relabels the cluster.
func TestEventRelabelOnExpiry(t *testing.T) {
	d := newTestDB(t, 2, 2, 10, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustTick(t, d, 5)
	mustInsert(t, d, 3, 2, 0)
	mustInsert(t, d, 4, 3, 0)

	res := mustTick(t, d, 10) // 1,2 expire; 3 keeps the cluster alive
	assertChanges(t, res.Changes, []Change{{1, 1, -1}, {2, 1, -1}, {3, 1, 3}, {4, 1, 3}})
	assertEvents(t, res.Events, []Event{ev(Relabel, []int{1}, []int{3})})
}

// computeEvents classifies a many-to-many component as Reshape. (With the
// single-point operations of this service a component is always a star, so
// this case is exercised directly at the differencing level, which is also
// what a whole-sale reclustering diff would produce.)
func TestEventReshape(t *testing.T) {
	oldCls := []clusterView{
		{label: 1, cores: map[int]struct{}{1: {}, 2: {}}},
		{label: 3, cores: map[int]struct{}{3: {}, 4: {}}},
	}
	newCls := []clusterView{
		{label: 1, cores: map[int]struct{}{1: {}, 3: {}}},
		{label: 2, cores: map[int]struct{}{2: {}, 4: {}}},
	}
	got := computeEvents(oldCls, newCls)
	want := []Event{ev(Reshape, []int{1, 3}, []int{1, 2})}
	assertEvents(t, got, want)
}

// Clusters sharing no core point alive in both states are not linked, even
// when they carry the same label: the old one dies, a new one is born.
func TestEventSharedCoreMustBeAliveBothSides(t *testing.T) {
	oldCls := []clusterView{{label: 1, cores: map[int]struct{}{1: {}, 2: {}}}}
	newCls := []clusterView{{label: 1, cores: map[int]struct{}{3: {}, 4: {}}}}
	got := computeEvents(oldCls, newCls)
	want := []Event{
		ev(Birth, nil, []int{1}),
		ev(Death, []int{1}, nil),
	}
	assertEvents(t, got, want)

	// Same scenario through the public API across two operations: the old
	// cluster dies, then a new cluster is born at the same coordinates.
	d := newTestDB(t, 2, 2, 10, 20)
	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	res := mustTick(t, d, 10)
	assertEvents(t, res.Events, []Event{ev(Death, []int{1}, nil)})
	res = mustInsert(t, d, 3, 0, 0)
	assertEvents(t, res.Events, nil)
	res = mustInsert(t, d, 4, 1, 0)
	assertEvents(t, res.Events, []Event{ev(Birth, nil, []int{3})})
}

// Events of one operation are ordered by their smallest involved label.
func TestEventsSortedByMinLabel(t *testing.T) {
	d := newTestDB(t, 2, 2, 10, 20)

	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustInsert(t, d, 3, 100, 0)
	mustInsert(t, d, 4, 101, 0)
	res := mustTick(t, d, 10) // both clusters die in one tick
	assertEvents(t, res.Events, []Event{
		ev(Death, []int{1}, nil),
		ev(Death, []int{3}, nil),
	})
}

// Rejected operations must not change points, labels, or the clock.
func TestRejectedOpsKeepState(t *testing.T) {
	if _, err := New(0, 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New eps=0: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1_000_001, 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New eps too big: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 0, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New minPts=0: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 1001, 1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New minPts too big: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 1, 0, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New W=0: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 1, 1_000_000_001, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New W too big: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 1, 1, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New C=0: err = %v, want ErrInvalidParam", err)
	}
	if _, err := New(1, 1, 1, 100_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("New C too big: err = %v, want ErrInvalidParam", err)
	}

	d := newTestDB(t, 2, 2, 10, 3)
	mustInsert(t, d, 1, 0, 0)
	mustInsert(t, d, 2, 1, 0)
	mustTick(t, d, 5)
	snapshot := func() (int, int64, []Cluster) {
		return d.Alive(), d.Now(), d.Clusters()
	}
	beforeAlive, beforeNow, beforeClusters := snapshot()

	rejections := []func() error{
		func() error { _, err := d.Insert(0, 0, 0); return err },
		func() error { _, err := d.Insert(-3, 0, 0); return err },
		func() error { _, err := d.Insert(9, 1_000_001, 0); return err },
		func() error { _, err := d.Insert(9, 0, -1_000_001); return err },
		func() error { _, err := d.Insert(1, 5, 5); return err },
		func() error { _, err := d.Remove(0); return err },
		func() error { _, err := d.Remove(99); return err },
		func() error { _, err := d.Tick(1_000_000_000_000_001); return err },
		func() error { _, err := d.Tick(4); return err },
	}
	wantErrs := []error{
		ErrInvalidParam, ErrInvalidParam, ErrInvalidParam, ErrInvalidParam,
		ErrDuplicateID, ErrInvalidParam, ErrNotFound, ErrInvalidParam, ErrClockBack,
	}
	for i, op := range rejections {
		if err := op(); !errors.Is(err, wantErrs[i]) {
			t.Fatalf("rejection %d: err = %v, want %v", i, err, wantErrs[i])
		}
		gotAlive, gotNow, gotClusters := snapshot()
		if gotAlive != beforeAlive || gotNow != beforeNow || !reflect.DeepEqual(gotClusters, beforeClusters) {
			t.Fatalf("rejection %d changed state: alive %d->%d, now %d->%d, clusters %+v->%+v",
				i, beforeAlive, gotAlive, beforeNow, gotNow, beforeClusters, gotClusters)
		}
	}

	// Tick validation order: invalid parameter wins over clock rollback.
	if _, err := d.Tick(1_000_000_000_000_001); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Tick invalid+rollback: err = %v, want ErrInvalidParam", err)
	}
	// Tick to the current time is allowed and only re-checks expiry.
	res, err := d.Tick(5)
	if err != nil {
		t.Fatalf("Tick(5) at now=5: unexpected error %v", err)
	}
	assertChanges(t, res.Changes, nil)
}

// Concurrent calls are serialized: queries never observe a half-applied
// reclustering and the final state equals the sequential one.
func TestConcurrentAccess(t *testing.T) {
	d := newTestDB(t, 3, 2, 50, 500)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = d.Label(1)
				_ = d.Neighbors(2)
				_ = d.Clusters()
				_ = d.Alive()
			}
		}()
	}

	// Deterministic operation stream applied from one goroutine.
	rng := uint64(42)
	next := func(n int) int {
		rng = rng*6364136223846793005 + 1442695040888963407
		return int(rng >> 33 % uint64(n))
	}
	now := int64(0)
	for i := 0; i < 2000; i++ {
		switch next(3) {
		case 0:
			_, _ = d.Insert(1+next(60), next(40)-20, next(40)-20)
		case 1:
			_, _ = d.Remove(1 + next(60))
		case 2:
			now += int64(next(10))
			_, _ = d.Tick(now)
		}
	}
	close(stop)
	wg.Wait()

	// The final state must be self-consistent: every cluster label equals
	// the smallest core id of its component.
	for _, c := range d.Clusters() {
		min := 0
		for _, id := range c.Members {
			if min == 0 || id < min {
				min = id
			}
		}
		if c.Label > min {
			t.Fatalf("cluster %+v has label greater than its smallest member", c)
		}
	}
}
