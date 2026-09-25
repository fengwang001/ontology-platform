package reg

import (
	"fmt"
	"math/rand"
	"testing"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestReclaimTiming: reclaimed exactly at the 1->0 moment, never before.
func TestReclaimTiming(t *testing.T) {
	r := New()
	must(t, r.Create("x"))
	must(t, r.Acquire("a", "x"))
	must(t, r.Acquire("b", "x"))
	must(t, r.Release("a", "x"))
	if !r.Alive("x") {
		t.Fatal("reclaimed while count=1")
	}
	must(t, r.Release("b", "x"))
	if r.Alive("x") {
		t.Fatal("not reclaimed at count 0")
	}
	if _, err := r.RefCount("x"); err != ErrNotFound {
		t.Fatalf("reclaimed: got %v want ErrNotFound", err)
	}
	if err := r.Acquire("c", "x"); err != ErrNotFound {
		t.Fatalf("acquire reclaimed: got %v want ErrNotFound", err)
	}
}

// TestIdempotentAcquire: repeated Acquire by same referrer counts once.
func TestIdempotentAcquire(t *testing.T) {
	r := New()
	must(t, r.Create("x"))
	for i := 0; i < 5; i++ {
		must(t, r.Acquire("r", "x"))
	}
	if n, _ := r.RefCount("x"); n != 1 {
		t.Fatalf("count=%d want 1", n)
	}
}

// TestSharedLastReleaseReclaims: only the last release reclaims.
func TestSharedLastReleaseReclaims(t *testing.T) {
	r := New()
	must(t, r.Create("x"))
	for _, ref := range []string{"r1", "r2", "r3"} {
		must(t, r.Acquire(ref, "x"))
	}
	for i, ref := range []string{"r1", "r2", "r3"} {
		must(t, r.Release(ref, "x"))
		if alive := r.Alive("x"); alive != (i != 2) {
			t.Fatalf("after release %s alive=%v", ref, alive)
		}
	}
}

// TestFailureLeavesNoTrace: every rejected op is atomic and distinct.
func TestFailureLeavesNoTrace(t *testing.T) {
	r := New()
	must(t, r.Create("x"))
	must(t, r.Acquire("r", "x"))
	cases := []struct{ got, want error }{
		{r.Create(""), ErrEmpty}, {r.Acquire("", "x"), ErrEmpty},
		{r.Acquire("r", ""), ErrEmpty}, {r.Release("", "x"), ErrEmpty},
		{r.Create("x"), ErrExists},
		{r.Acquire("r", "ghost"), ErrNotFound}, {r.Release("r", "ghost"), ErrNotFound},
		{r.Release("nobody", "x"), ErrNotHeld},
	}
	for i, c := range cases {
		if c.got != c.want {
			t.Fatalf("case %d: got %v want %v", i, c.got, c.want)
		}
	}
	if n, _ := r.RefCount("x"); n != 1 || !r.Alive("x") {
		t.Fatal("rejected op changed state")
	}
	must(t, r.Release("r", "x"))
}

// TestRefCountConsistency: random op sequences checked against a model.
func TestRefCountConsistency(t *testing.T) {
	for _, objs := range []int{1, 7, 50} {
		rng := rand.New(rand.NewSource(int64(objs)))
		r := New()
		model := map[string]map[string]bool{}
		for i := 0; i < objs; i++ {
			id := fmt.Sprintf("o%d", i)
			must(t, r.Create(id))
			model[id] = map[string]bool{}
		}
		for step := 0; step < 2000; step++ {
			id := fmt.Sprintf("o%d", rng.Intn(objs))
			ref := fmt.Sprintf("r%d", rng.Intn(10))
			if rng.Intn(2) == 0 {
				_ = r.Acquire(ref, id)
				model[id][ref] = true
				continue
			}
			if !model[id][ref] {
				continue
			}
			_ = r.Release(ref, id)
			delete(model[id], ref)
			if len(model[id]) == 0 { // reclaimed; re-register
				must(t, r.Create(id))
				model[id] = map[string]bool{}
			}
		}
		for id, holders := range model {
			if n, err := r.RefCount(id); err != nil || n != len(holders) {
				t.Fatalf("objs=%d id=%s count=%d want %d", objs, id, n, len(holders))
			}
		}
	}
}

// TestReclaimScanIsConstant: reclaim check inspects only the released object.
func TestReclaimScanIsConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		r := New()
		for i := 0; i < m; i++ {
			id := fmt.Sprintf("o%d", i)
			must(t, r.Create(id))
			must(t, r.Acquire(fmt.Sprintf("r%d", i), id))
		}
		must(t, r.Release("r0", "o0"))
		if r.lastScan > 1 {
			t.Fatalf("m=%d: reclaim scanned %d objects, want <=1", m, r.lastScan)
		}
	}
}
