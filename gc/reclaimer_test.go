package gc_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/gc"
)

func newReclaimer(t *testing.T, capacity int64, k int) *gc.Reclaimer {
	t.Helper()
	r, err := gc.NewReclaimer(capacity, k)
	if err != nil {
		t.Fatalf("NewReclaimer(%d, %d): %v", capacity, k, err)
	}
	return r
}

func mustPull(t *testing.T, r *gc.Reclaimer, img string, layers []gc.Layer, now int64) {
	t.Helper()
	if err := r.Pull(img, layers, now); err != nil {
		t.Fatalf("Pull(%s) at %d: %v", img, now, err)
	}
}

func TestSpecExample(t *testing.T) {
	build := func(k int) *gc.Reclaimer {
		r := newReclaimer(t, 1000, k)
		mustPull(t, r, "r:1", []gc.Layer{{ID: "L1", Size: 300}}, 10)
		mustPull(t, r, "r:2", []gc.Layer{{ID: "L1", Size: 300}, {ID: "L2", Size: 200}}, 20)
		mustPull(t, r, "r:3", []gc.Layer{{ID: "L3", Size: 100}}, 30)
		return r
	}

	r := build(1)
	if got := r.Used(); got != toInt64(600) {
		t.Fatalf("used=%d want 600", got)
	}
	res, err := r.GC(100, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"r:1", "r:2"}) || res.Freed != 500 || res.Insufficient {
		t.Fatalf("K=1 got %+v, want deleted=[r:1 r:2] freed=500 insufficient=false", res)
	}

	r = build(2)
	res, err = r.GC(100, 50, 20, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"r:1"}) || res.Freed != 0 || !res.Insufficient {
		t.Fatalf("K=2 got %+v, want deleted=[r:1] freed=0 insufficient=true", res)
	}
}

func toInt64(v int64) int64 { return v }

func TestTriggerBoundary(t *testing.T) {
	// used*100 == high*C triggers.
	r := newReclaimer(t, 100, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 50}}, 1)
	res, err := r.GC(10, 50, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 1 || res.Freed != 50 {
		t.Fatalf("equality must trigger, got %+v", res)
	}

	// used*100 == high*C - 1 does not trigger: C=67, high=3 -> high*C=201,
	// used=2 -> used*100=200 = 201-1.
	r = newReclaimer(t, 67, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 2}}, 1)
	res, err = r.GC(10, 3, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || res.Freed != 0 || res.Insufficient {
		t.Fatalf("one below threshold must not trigger, got %+v", res)
	}
	if got := r.Used(); got != 2 {
		t.Fatalf("used=%d want 2", got)
	}
}

func TestNeedFloors(t *testing.T) {
	// C=333, low=10 -> floor(333*10/100)=33, need=100-33=67.
	// First deletion frees 66 (<67), so a second image must be deleted.
	r := newReclaimer(t, 333, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 66}}, 1)
	mustPull(t, r, "a:2", []gc.Layer{{ID: "L2", Size: 34}}, 2)
	res, err := r.GC(10, 30, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 100 || res.Insufficient {
		t.Fatalf("got %+v, want deleted=[a:1 a:2] freed=100 insufficient=false", res)
	}
}

func TestMinAgeBoundary(t *testing.T) {
	build := func() *gc.Reclaimer {
		r := newReclaimer(t, 1000, 0)
		mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 600}}, 10)
		return r
	}
	// now-lastUsed == minAge qualifies.
	r := build()
	res, err := r.GC(20, 50, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:1"}) {
		t.Fatalf("age==minAge must qualify, got %+v", res)
	}
	// now-lastUsed == minAge-1 does not qualify.
	r = build()
	res, err = r.GC(20, 50, 10, 11)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Deleted) != 0 || !res.Insufficient {
		t.Fatalf("age==minAge-1 must not qualify, got %+v", res)
	}
	if got := r.Used(); got != 600 {
		t.Fatalf("used=%d want 600", got)
	}
}

func TestLastUsedTieBreak(t *testing.T) {
	// All three images share lastUsed=10. K=1 protects the lexicographically
	// smallest ID (r:a); candidates delete smaller ID first.
	r := newReclaimer(t, 1000, 1)
	mustPull(t, r, "r:a", []gc.Layer{{ID: "L1", Size: 50}}, 10)
	mustPull(t, r, "r:b", []gc.Layer{{ID: "L2", Size: 200}}, 10)
	mustPull(t, r, "r:c", []gc.Layer{{ID: "L3", Size: 50}}, 10)
	res, err := r.GC(100, 30, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	// need = 300-100 = 200; r:b deleted first frees exactly 200 -> stop.
	if !reflect.DeepEqual(res.Deleted, []string{"r:b"}) || res.Freed != 200 || res.Insufficient {
		t.Fatalf("got %+v, want deleted=[r:b] freed=200", res)
	}

	// K=2 cuts inside the tie: r:a and r:b protected, only r:c candidate.
	r = newReclaimer(t, 1000, 2)
	mustPull(t, r, "r:a", []gc.Layer{{ID: "L1", Size: 50}}, 10)
	mustPull(t, r, "r:b", []gc.Layer{{ID: "L2", Size: 200}}, 10)
	mustPull(t, r, "r:c", []gc.Layer{{ID: "L3", Size: 50}}, 10)
	res, err = r.GC(100, 30, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"r:c"}) || res.Freed != 50 || !res.Insufficient {
		t.Fatalf("got %+v, want deleted=[r:c] freed=50 insufficient=true", res)
	}
}

func TestProtectionIncludesRunning(t *testing.T) {
	// r:1 is running and newest, so it takes the single protection slot;
	// r:2 becomes the only candidate.
	r := newReclaimer(t, 1000, 1)
	mustPull(t, r, "r:1", []gc.Layer{{ID: "L1", Size: 300}}, 1)
	mustPull(t, r, "r:2", []gc.Layer{{ID: "L2", Size: 300}}, 2)
	if err := r.Run("r:1", 3); err != nil {
		t.Fatal(err)
	}
	res, err := r.GC(10, 50, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"r:2"}) || res.Freed != 300 {
		t.Fatalf("got %+v, want deleted=[r:2] freed=300", res)
	}
}

func TestSharedLayerFreedAtLastHolder(t *testing.T) {
	// L1 shared by a:1 and a:2; deleting a:1 frees nothing, deleting a:2
	// frees L1+L2.
	r := newReclaimer(t, 1000, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 1)
	mustPull(t, r, "a:2", []gc.Layer{{ID: "L1", Size: 300}, {ID: "L2", Size: 200}}, 2)
	res, err := r.GC(10, 50, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:1", "a:2"}) || res.Freed != 500 {
		t.Fatalf("got %+v, want deleted=[a:1 a:2] freed=500", res)
	}
}

func TestZeroFreeImageStillDeleted(t *testing.T) {
	// a:1's only layer is shared with protected a:2; a:1 is deleted anyway.
	r := newReclaimer(t, 1000, 1)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 1)
	mustPull(t, r, "a:2", []gc.Layer{{ID: "L1", Size: 300}}, 2)
	res, err := r.GC(10, 30, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:1"}) || res.Freed != 0 || !res.Insufficient {
		t.Fatalf("got %+v, want deleted=[a:1] freed=0 insufficient=true", res)
	}
	if got := r.Used(); got != 300 {
		t.Fatalf("used=%d want 300 (shared layer kept)", got)
	}
}

func TestPullingLayersOccupyShareAndAbort(t *testing.T) {
	r := newReclaimer(t, 400, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 1)
	// Pulling image shares L1 and adds L2: used=350.
	if err := r.BeginPull("a:2", []gc.Layer{{ID: "L1", Size: 300}, {ID: "L2", Size: 50}}, 2); err != nil {
		t.Fatal(err)
	}
	if got := r.Used(); got != 350 {
		t.Fatalf("used=%d want 350", got)
	}
	// Conflict against a layer owned by the pulling image.
	err := r.BeginPull("a:3", []gc.Layer{{ID: "L2", Size: 51}}, 3)
	var lc *gc.LayerConflictError
	if !errors.As(err, &lc) || lc.LayerID != "L2" {
		t.Fatalf("want layer conflict on L2, got %v", err)
	}
	// No space: 350+100 > 400.
	if err := r.Pull("a:4", []gc.Layer{{ID: "L3", Size: 100}}, 4); !errors.Is(err, gc.ErrNoSpace) {
		t.Fatalf("want no space, got %v", err)
	}
	// AbortPull releases only the exclusive layer L2.
	if err := r.AbortPull("a:2", 5); err != nil {
		t.Fatal(err)
	}
	if got := r.Used(); got != 300 {
		t.Fatalf("used=%d want 300 after abort", got)
	}
	// GC never touches pulling images.
	if err := r.BeginPull("a:5", []gc.Layer{{ID: "L4", Size: 100}}, 6); err != nil {
		t.Fatal(err)
	}
	res, err := r.GC(10, 50, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:1"}) || res.Freed != 300 {
		t.Fatalf("got %+v, want deleted=[a:1] freed=300", res)
	}
	if got := r.Used(); got != 100 {
		t.Fatalf("used=%d want 100 (pulling image kept)", got)
	}
}

func TestStopRefreshesLastUsed(t *testing.T) {
	r := newReclaimer(t, 1000, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 10)
	mustPull(t, r, "a:2", []gc.Layer{{ID: "L2", Size: 300}}, 20)
	if err := r.Run("a:1", 25); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop("a:1", 30); err != nil {
		t.Fatal(err)
	}
	// a:1 lastUsed=30 -> age 70 < 75 not a candidate; a:2 age 80 qualifies.
	res, err := r.GC(100, 50, 10, 75)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a:2"}) || res.Freed != 300 || !res.Insufficient {
		t.Fatalf("got %+v, want deleted=[a:2] freed=300 insufficient=true", res)
	}
}

func TestRejectionOrdering(t *testing.T) {
	r := newReclaimer(t, 400, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 10)
	if err := r.BeginPull("a:2", []gc.Layer{{ID: "L2", Size: 50}}, 20); err != nil {
		t.Fatal(err)
	}

	// Invalid argument beats clock skew.
	if err := r.BeginPull("", nil, -1); !errors.Is(err, gc.ErrInvalidArgument) {
		t.Fatalf("got %v", err)
	}
	// Clock skew beats image-exists.
	if err := r.Pull("a:1", []gc.Layer{{ID: "L1", Size: 300}}, 5); !errors.Is(err, gc.ErrClockSkew) {
		t.Fatalf("got %v", err)
	}
	// Image-exists beats layer conflict.
	if err := r.Pull("a:1", []gc.Layer{{ID: "L1", Size: 999}}, 20); !errors.Is(err, gc.ErrImageExists) {
		t.Fatalf("got %v", err)
	}
	// Layer conflict beats no-space (L1 conflicts; L3 would also overflow).
	err := r.Pull("a:3", []gc.Layer{{ID: "L1", Size: 1}, {ID: "L3", Size: 400}}, 20)
	var lc *gc.LayerConflictError
	if !errors.As(err, &lc) || lc.LayerID != "L1" {
		t.Fatalf("want conflict on L1, got %v", err)
	}
	// No-space when no conflict.
	if err := r.Pull("a:3", []gc.Layer{{ID: "L3", Size: 51}}, 20); !errors.Is(err, gc.ErrNoSpace) {
		t.Fatalf("got %v", err)
	}
	// Not-found beats state mismatch.
	if err := r.CommitPull("a:9", 20); !errors.Is(err, gc.ErrImageNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := r.AbortPull("a:9", 20); !errors.Is(err, gc.ErrImageNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := r.Run("a:9", 20); !errors.Is(err, gc.ErrImageNotFound) {
		t.Fatalf("got %v", err)
	}
	if err := r.Stop("a:9", 20); !errors.Is(err, gc.ErrImageNotFound) {
		t.Fatalf("got %v", err)
	}
	// State mismatches are distinguishable.
	if err := r.Run("a:2", 20); !errors.Is(err, gc.ErrImagePulling) {
		t.Fatalf("got %v", err)
	}
	if err := r.Stop("a:2", 20); !errors.Is(err, gc.ErrImagePulling) {
		t.Fatalf("got %v", err)
	}
	if err := r.CommitPull("a:1", 20); !errors.Is(err, gc.ErrImageNotPulling) {
		t.Fatalf("got %v", err)
	}
	if err := r.AbortPull("a:1", 20); !errors.Is(err, gc.ErrImageNotPulling) {
		t.Fatalf("got %v", err)
	}
	// Stop with run==0.
	if err := r.Stop("a:1", 20); !errors.Is(err, gc.ErrNotRunning) {
		t.Fatalf("got %v", err)
	}
	// GC argument validation.
	for _, args := range [][4]int64{
		{20, 0, 0, 0},    // low must be > 0
		{20, 50, 50, 0},  // low must be < high
		{20, 101, 50, 0}, // high must be <= 100
		{20, 50, 10, -1}, // minAge must be >= 0
		{-1, 50, 10, 0},  // now must be >= 0
	} {
		if _, err := r.GC(args[0], int(args[1]), int(args[2]), args[3]); !errors.Is(err, gc.ErrInvalidArgument) {
			t.Fatalf("GC%v got %v", args, err)
		}
	}
}

func TestRejectedOpsKeepState(t *testing.T) {
	r := newReclaimer(t, 400, 0)
	mustPull(t, r, "a:1", []gc.Layer{{ID: "L1", Size: 300}}, 10)

	// Failed BeginPull (no space) must not register the image.
	if err := r.BeginPull("a:2", []gc.Layer{{ID: "L2", Size: 200}}, 20); !errors.Is(err, gc.ErrNoSpace) {
		t.Fatal(err)
	}
	if err := r.CommitPull("a:2", 20); !errors.Is(err, gc.ErrImageNotFound) {
		t.Fatalf("rejected BeginPull must not register image, got %v", err)
	}
	// Failed BeginPull (conflict) must not add layers.
	if err := r.BeginPull("a:3", []gc.Layer{{ID: "L1", Size: 1}, {ID: "L9", Size: 10}}, 20); err == nil {
		t.Fatal("want conflict")
	}
	if got := r.Used(); got != 300 {
		t.Fatalf("used=%d want 300", got)
	}
	// Rejected GC must not advance the clock.
	if _, err := r.GC(100, 0, 0, 0); !errors.Is(err, gc.ErrInvalidArgument) {
		t.Fatal(err)
	}
	if err := r.Pull("a:4", []gc.Layer{{ID: "L3", Size: 50}}, 50); err != nil {
		t.Fatalf("clock must not move on rejected GC: %v", err)
	}
	// Accepted (no-op) GC advances the clock.
	if _, err := r.GC(60, 100, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.Pull("a:5", []gc.Layer{{ID: "L4", Size: 10}}, 59); !errors.Is(err, gc.ErrClockSkew) {
		t.Fatalf("got %v", err)
	}
	// Failed Stop (not running) keeps run/lastUsed.
	if err := r.Stop("a:1", 60); !errors.Is(err, gc.ErrNotRunning) {
		t.Fatal(err)
	}
	if err := r.Run("a:1", 60); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop("a:1", 60); err != nil {
		t.Fatalf("run count must be intact: %v", err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, cfg := range [][2]int64{
		{0, 0}, {-1, 0}, {1_000_000_000_000_001, 0}, {100, -1}, {100, 101},
	} {
		if _, err := gc.NewReclaimer(cfg[0], int(cfg[1])); !errors.Is(err, gc.ErrInvalidConfig) {
			t.Fatalf("NewReclaimer(%d, %d) got %v", cfg[0], cfg[1], err)
		}
	}
	if _, err := gc.NewReclaimer(1, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := gc.NewReclaimer(1_000_000_000_000_000, 0); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidPullArgs(t *testing.T) {
	r := newReclaimer(t, 1000, 0)
	cases := []struct {
		img    string
		layers []gc.Layer
		now    int64
	}{
		{"", []gc.Layer{{ID: "L1", Size: 1}}, 0},
		{"a:1", nil, 0},
		{"a:1", []gc.Layer{{ID: "", Size: 1}}, 0},
		{"a:1", []gc.Layer{{ID: "L1", Size: 0}}, 0},
		{"a:1", []gc.Layer{{ID: "L1", Size: 1_000_000_000_001}}, 0},
		{"a:1", []gc.Layer{{ID: "L1", Size: 1}, {ID: "L1", Size: 1}}, 0},
		{"a:1", []gc.Layer{{ID: "L1", Size: 1}}, -1},
	}
	for i, c := range cases {
		if err := r.Pull(c.img, c.layers, c.now); !errors.Is(err, gc.ErrInvalidArgument) {
			t.Fatalf("case %d got %v", i, err)
		}
	}
	if got := r.Used(); got != 0 {
		t.Fatalf("used=%d want 0", got)
	}
}

func TestConcurrentPullSameImage(t *testing.T) {
	r := newReclaimer(t, 1000, 0)
	const n = 32
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.Pull("a:1", []gc.Layer{{ID: "L1", Size: 100}}, 1)
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, gc.ErrImageExists) {
			t.Fatalf("unexpected error %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one pull must succeed, got %d", ok)
	}
	if got := r.Used(); got != 100 {
		t.Fatalf("used=%d want 100", got)
	}
}
