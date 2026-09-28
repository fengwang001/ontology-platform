package counter

import (
	"errors"
	"math"
	"math/big"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// ---- naive reference model -------------------------------------------------

// naive is the obviously-correct specification: per-replica totals of local
// inc/dec operations. A PN-counter in which every operation has propagated to
// every replica must equal sum(inc)-sum(dec) exactly.
type naive struct {
	inc []*big.Int
	dec []*big.Int
}

func newNaive(n int) *naive {
	m := &naive{inc: make([]*big.Int, n), dec: make([]*big.Int, n)}
	for i := range m.inc {
		m.inc[i] = new(big.Int)
		m.dec[i] = new(big.Int)
	}
	return m
}

func (m *naive) add(r int, d uint64, inc bool) {
	x := new(big.Int).SetUint64(d)
	if inc {
		m.inc[r].Add(m.inc[r], x)
	} else {
		m.dec[r].Add(m.dec[r], x)
	}
}

func (m *naive) value() *big.Int {
	v := new(big.Int)
	for _, x := range m.inc {
		v.Add(v, x)
	}
	for _, x := range m.dec {
		v.Sub(v, x)
	}
	return v
}

func logValues(t *testing.T, step, basis string, c *Counter) {
	t.Helper()
	vals := c.Values()
	snap := c.Snapshot()
	t.Logf("step=%-28s | %s", step, basis)
	for i := range vals {
		t.Logf("    replica %d: value=%-4s P=%v N=%v", i, vals[i], snap.P[i], snap.N[i])
	}
}

func mustInc(t *testing.T, c *Counter, r int, d uint64) {
	t.Helper()
	if err := c.Inc(r, d); err != nil {
		t.Fatalf("Inc(%d,%d) unexpected error: %v", r, d, err)
	}
}

func mustDec(t *testing.T, c *Counter, r int, d uint64) {
	t.Helper()
	if err := c.Dec(r, d); err != nil {
		t.Fatalf("Dec(%d,%d) unexpected error: %v", r, d, err)
	}
}

func mustMerge(t *testing.T, c *Counter, dst, src int) {
	t.Helper()
	if err := c.Merge(dst, src); err != nil {
		t.Fatalf("Merge(%d,%d) unexpected error: %v", dst, src, err)
	}
}

func expectErr(t *testing.T, err error, target error, where string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error wrapping %v, got nil", where, target)
	}
	if !errors.Is(err, target) {
		t.Fatalf("%s: expected %v, got %v", where, target, err)
	}
	t.Logf("step=reject %-26s | rejected as expected: %v", where, err)
}

func u64Equal(a, b []uint64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func snapEqual(a, b Snapshot) bool {
	if a.Replicas != b.Replicas || len(a.P) != len(b.P) {
		return false
	}
	for i := range a.P {
		if !u64Equal(a.P[i], b.P[i]) || !u64Equal(a.N[i], b.N[i]) {
			return false
		}
	}
	return true
}

func assertConverged(t *testing.T, c *Counter, want *big.Int) {
	t.Helper()
	for i, v := range c.Values() {
		if v.Cmp(want) != 0 {
			t.Fatalf("replica %d value %s, want %s (no convergence)", i, v, want)
		}
	}
}

// ---- basic semantics -------------------------------------------------------

func TestBasicIncDecValue(t *testing.T) {
	c, err := New(3, math.MaxUint64)
	if err != nil {
		t.Fatal(err)
	}
	logValues(t, "init", "all vectors zero => every value 0 (value=sum(P)-sum(N))", c)

	mustInc(t, c, 0, 5)
	mustInc(t, c, 1, 3)
	mustDec(t, c, 0, 4) // replica 0 value goes negative
	mustDec(t, c, 2, 10)
	logValues(t, "local ops only", "each replica sees only its own component", c)

	want := []int64{1, 3, -10}
	for i, w := range want {
		v, err := c.Value(i)
		if err != nil {
			t.Fatal(err)
		}
		if v.Int64() != w {
			t.Fatalf("before merge replica %d: got %d want %d", i, v.Int64(), w)
		}
	}

	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			mustMerge(t, c, i, j)
		}
	}
	logValues(t, "all pairs merged", "merge=component-wise max; vectors identical", c)

	// 5+3-4-10 = -6
	assertConverged(t, c, big.NewInt(-6))
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}
	t.Log("verdict: convergence to -6 (may be negative), SelfCheck OK")
}

// ---- rejection categories & atomicity --------------------------------------

func TestRejectionCategoriesAndNoTrace(t *testing.T) {
	if _, err := New(0, 10); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(0): got %v, want ErrInvalidArgument", err)
	}
	if _, err := New(2, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(2,0): got %v, want ErrInvalidArgument", err)
	}
	t.Logf("step=reject constructor           | New(0), New(2,0) -> %v", ErrInvalidArgument)

	c, err := New(2, 10)
	if err != nil {
		t.Fatal(err)
	}

	expectErr(t, c.Inc(2, 1), ErrUnknownReplica, "Inc(2,1)")
	expectErr(t, c.Dec(-1, 1), ErrUnknownReplica, "Dec(-1,1)")
	expectErr(t, c.Merge(0, 2), ErrUnknownReplica, "Merge(0,2)")
	expectErr(t, c.Merge(-1, 0), ErrUnknownReplica, "Merge(-1,0)")
	if _, err := c.Value(9); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("Value(9): got %v, want ErrUnknownReplica", err)
	}

	expectErr(t, c.Inc(0, 0), ErrNonPositive, "Inc(0,0)")
	expectErr(t, c.Dec(1, 0), ErrNonPositive, "Dec(1,0)")

	mustInc(t, c, 0, 10)
	mustDec(t, c, 0, 10)
	before := c.Snapshot()
	expectErr(t, c.Inc(0, 1), ErrOverflow, "Inc(0,1) at limit")
	expectErr(t, c.Dec(0, 1), ErrOverflow, "Dec(0,1) at limit")

	after := c.Snapshot()
	if !snapEqual(before, after) {
		t.Fatalf("state changed after rejection:\nbefore P=%v N=%v\nafter  P=%v N=%v",
			before.P, before.N, after.P, after.N)
	}

	// dst == src is a legal no-op and must not change anything.
	dstBefore, _, _ := c.ReplicaSnapshot(0)
	if err := c.Merge(0, 0); err != nil {
		t.Fatalf("self merge: %v", err)
	}
	dstAfter, _, err := c.ReplicaSnapshot(0)
	if err != nil {
		t.Fatal(err)
	}
	if !u64Equal(dstBefore, dstAfter) {
		t.Fatalf("self merge mutated state: %v -> %v", dstBefore, dstAfter)
	}

	logValues(t, "after all rejections", "only accepted op is Inc(0,10)", c)
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("selfcheck: %v", err)
	}

	var oe *OpError
	if err := c.Inc(5, 1); !errors.As(err, &oe) || oe.Op != "inc" {
		t.Fatalf("expected *OpError{Op:inc}, got %v", err)
	}
	t.Log("verdict: distinct rejection categories; no trace left by failures")
}

// Overflow during merge must be atomic: dst untouched. We emulate state
// imported from a counter with a larger limit by directly raising a vector.
func TestMergeOverflowRejectedAtomically(t *testing.T) {
	c, err := New(2, 5)
	if err != nil {
		t.Fatal(err)
	}
	mustInc(t, c, 0, 5)
	c.rs[1].mu.Lock()
	c.rs[1].p[1] = 9 // foreign state above this counter's limit
	c.rs[1].mu.Unlock()

	dstBefore, _, _ := c.ReplicaSnapshot(0)
	expectErr(t, c.Merge(0, 1), ErrOverflow, "Merge importing component 9, limit 5")
	dstAfter, _, _ := c.ReplicaSnapshot(0)
	if !u64Equal(dstBefore, dstAfter) {
		t.Fatalf("dst mutated despite rejected merge: %v -> %v", dstBefore, dstAfter)
	}
	t.Log("verdict: overflow merge is atomic, destination unchanged")
}

// ---- idempotence / commutativity / associativity ---------------------------

type op struct {
	r     int
	d     uint64
	isInc bool
}

func genScript(rng *rand.Rand, n, ops int) []op {
	s := make([]op, ops)
	for i := range s {
		s[i] = op{
			r:     rng.Intn(n),
			d:     uint64(1 + rng.Intn(7)),
			isInc: rng.Intn(2) == 0,
		}
	}
	return s
}

func playScript(t *testing.T, c *Counter, s []op, m *naive) {
	t.Helper()
	for _, o := range s {
		var err error
		if o.isInc {
			err = c.Inc(o.r, o.d)
		} else {
			err = c.Dec(o.r, o.d)
		}
		if err != nil {
			t.Fatalf("script op %+v: %v", o, err)
		}
		if m != nil {
			m.add(o.r, o.d, o.isInc)
		}
	}
}

func fullMerge(c *Counter, n int) {
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			_ = c.Merge(i, j)
		}
	}
}

func TestMergeIdempotence(t *testing.T) {
	const n = 3
	c, _ := New(n, math.MaxUint64)
	m := newNaive(n)
	playScript(t, c, genScript(rand.New(rand.NewSource(1)), n, 14), m)

	fullMerge(c, n)
	first := c.Snapshot()
	for round := 0; round < 5; round++ {
		fullMerge(c, n)
	}
	if !snapEqual(first, c.Snapshot()) {
		t.Fatal("repeated merges changed converged state")
	}
	logValues(t, "idempotence x5", "repeated/self merges are no-ops once converged", c)
	assertConverged(t, c, m.value())
	t.Logf("verdict: merge idempotent; all values match naive %s", m.value())
}

func TestMergeCommutativity(t *testing.T) {
	const n = 3
	s := genScript(rand.New(rand.NewSource(2)), n, 14)
	c1, _ := New(n, math.MaxUint64)
	c2, _ := New(n, math.MaxUint64)
	playScript(t, c1, s, nil)
	playScript(t, c2, s, nil)

	// Same pairwise exchanges, opposite directions.
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			mustMerge(t, c1, i, j)
		}
	}
	for i := n - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			mustMerge(t, c2, i, j)
		}
	}
	if !snapEqual(c1.Snapshot(), c2.Snapshot()) {
		t.Fatalf("merge order changed result:\n%v\n%v",
			c1.Snapshot().P, c2.Snapshot().P)
	}
	logValues(t, "commutativity", "forward vs reverse merge order identical", c1)
	t.Log("verdict: merge commutative (order-independent)")
}

func TestMergeAssociativity(t *testing.T) {
	const n = 3
	s := genScript(rand.New(rand.NewSource(3)), n, 15)

	// Left: ((0<+1)<+2), then fan out. Right: (0<+(1<+2)) style grouping.
	left, _ := New(n, math.MaxUint64)
	right, _ := New(n, math.MaxUint64)
	playScript(t, left, s, nil)
	playScript(t, right, s, nil)

	// left-associated cascade
	mustMerge(t, left, 0, 1)
	mustMerge(t, left, 0, 2)
	mustMerge(t, left, 1, 0)
	mustMerge(t, left, 2, 0)

	// right-associated cascade
	mustMerge(t, right, 1, 2)
	mustMerge(t, right, 2, 1)
	mustMerge(t, right, 0, 2)
	mustMerge(t, right, 1, 0)
	mustMerge(t, right, 2, 0)

	lv, _ := left.Value(0)
	rv, _ := right.Value(0)
	if lv.Cmp(rv) != 0 {
		t.Fatalf("association changed value: %s vs %s", lv, rv)
	}
	fullMerge(left, n)
	fullMerge(right, n)
	if !snapEqual(left.Snapshot(), right.Snapshot()) {
		t.Fatal("fully merged states differ by merge grouping")
	}
	t.Logf("verdict: merge associative (both groupings -> %s)", lv)
}

// ---- convergence vs naive reference under random merge schedules -----------

// Reference propagation: maintain per-replica knowledge as component-wise
// maxima of local vectors; merge copies maxima. Final value must equal the
// naive global sum regardless of duplicate / out-of-order messages.
func TestRandomSchedulesConvergeToNaive(t *testing.T) {
	const n = 4
	var last *Counter
	for seed := int64(10); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		c, _ := New(n, 1<<40)
		last = c
		m := newNaive(n)
		playScript(t, c, genScript(rng, n, 40), m)

		// Random, redundant, out-of-order pairwise merges.
		for k := 0; k < 200; k++ {
			a, b := rng.Intn(n), rng.Intn(n)
			if err := c.Merge(a, b); err != nil {
				t.Fatalf("seed %d merge(%d,%d): %v", seed, a, b, err)
			}
		}
		// Guarantee full propagation with a final complete round.
		fullMerge(c, n)
		assertConverged(t, c, m.value())
		if err := c.SelfCheck(); err != nil {
			t.Fatalf("seed %d selfcheck: %v", seed, err)
		}
	}
	logValues(t, "10 random schedules", "redundant/out-of-order merges; final round propagates all ops", last)
	t.Logf("verdict: all schedules converge to naive value; SelfCheck OK")
}

// ---- concurrency ------------------------------------------------------------

func TestConcurrentAllOpsNoDeadlock(t *testing.T) {
	const n = 4
	c, err := New(n, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	m := newNaive(n)
	var mu sync.Mutex

	record := func(r int, d uint64, inc bool) {
		mu.Lock()
		m.add(r, d, inc)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// Local inc/dec workers (their operations only touch the own component
	// but merges propagate them concurrently).
	for w := 0; w < n; w++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(100 + r)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				d := uint64(1 + rng.Intn(5))
				if rng.Intn(2) == 0 {
					if err := c.Inc(r, d); err == nil {
						record(r, d, true)
					}
				} else {
					if err := c.Dec(r, d); err == nil {
						record(r, d, false)
					}
				}
			}
		}(w)
	}

	// Merge workers: includes mutually inverse merges between the same pair.
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = c.Merge(rng.Intn(n), rng.Intn(n))
			}
		}(200 + int64(w))
	}

	// Read-only workers: value, snapshot, self-check concurrently.
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				_, _ = c.Value(rng.Intn(n))
				_ = c.Snapshot()
				_ = c.SelfCheck()
				_, _, _ = c.ReplicaSnapshot(rng.Intn(n))
			}
		}(300 + int64(w))
	}

	// If locking could deadlock, this timeout fires instead of finishing.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	time.Sleep(300 * time.Millisecond)
	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: workers did not finish")
	}

	fullMerge(c, n)
	logValues(t, "concurrent run + final merge", "all accepted ops propagated to every replica", c)
	assertConverged(t, c, m.value())
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("selfcheck after concurrent run: %v", err)
	}
	t.Logf("verdict: concurrent inc/dec/merge/value/snapshot/selfcheck converged to naive %s", m.value())
}

// Deterministic inverse-merge stress: Merge(a,b) and Merge(b,a) hammered on
// the same pair from many goroutines must neither deadlock nor corrupt.
func TestInverseMergesNoDeadlock(t *testing.T) {
	c, _ := New(2, math.MaxUint64)
	mustInc(t, c, 0, 7)
	mustDec(t, c, 1, 4)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_ = c.Merge(0, 1)
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				_ = c.Merge(1, 0)
			}
		}()
	}
	wg.Wait()
	assertConverged(t, c, big.NewInt(3))
	t.Log("verdict: bidirectional merges under high concurrency: no deadlock, value 3")
}

// Large components show big.Int evaluation stays exact past int64 range.
func TestLargeValuesExact(t *testing.T) {
	c, _ := New(2, math.MaxUint64)
	mustInc(t, c, 0, math.MaxUint64)
	mustInc(t, c, 1, 1)
	mustDec(t, c, 0, math.MaxUint64)
	mustMerge(t, c, 1, 0)
	v, _ := c.Value(1)
	want := big.NewInt(1)
	if v.Cmp(want) != 0 {
		t.Fatalf("large value: got %s want %s", v, want)
	}
	t.Logf("verdict: MaxUint64-scale components evaluate exactly (%s)", v)
}
