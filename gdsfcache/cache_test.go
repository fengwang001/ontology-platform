package gdsfcache

import (
	"errors"
	"math/big"
	"reflect"
	"testing"
)

func rat(t *testing.T, num, den int64) *big.Rat {
	t.Helper()
	return big.NewRat(num, den)
}

func mustCache(t *testing.T, cap int64) *Cache {
	t.Helper()
	c, err := New(cap)
	if err != nil {
		t.Fatalf("New(%d): %v", cap, err)
	}
	return c
}

func mustPut(t *testing.T, c *Cache, key string, size, cost int64) []string {
	t.Helper()
	evicted, err := c.Put(key, size, cost)
	if err != nil {
		t.Fatalf("Put(%q, %d, %d): %v", key, size, cost, err)
	}
	return evicted
}

func mustGet(t *testing.T, c *Cache, key string) bool {
	t.Helper()
	hit, err := c.Get(key)
	if err != nil {
		t.Fatalf("Get(%q): %v", key, err)
	}
	return hit
}

func mustPeek(t *testing.T, c *Cache, key string) Info {
	t.Helper()
	info, err := c.Peek(key)
	if err != nil {
		t.Fatalf("Peek(%q): %v", key, err)
	}
	return info
}

// L advances to the evicted entry's H exactly: not 0, not an average.
func TestLAdvancesToEvictedH(t *testing.T) {
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, 4)
	mustPut(t, c, "b", 1, 6)
	evicted := mustPut(t, c, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"a"}) {
		t.Fatalf("evicted = %v, want [a]", evicted)
	}
	if got, want := c.L(), rat(t, 4, 1); got.Cmp(want) != 0 {
		t.Fatalf("L = %v, want %v (the evicted entry's H, not 0 or an average)", got, want)
	}
}

// A hit recomputes H from the current L, not the L at insertion time.
func TestHitRecomputesWithCurrentL(t *testing.T) {
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, 2)
	mustPut(t, c, "b", 1, 5)
	mustGet(t, c, "a") // a: freq=2, H=4 (L still 0)
	evicted := mustPut(t, c, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"a"}) {
		t.Fatalf("evicted = %v, want [a]", evicted)
	}
	// L is now 4. Hitting b must give H = 4 + 2*5/1 = 14, not 0 + 10 = 10.
	mustGet(t, c, "b")
	info := mustPeek(t, c, "b")
	if want := rat(t, 14, 1); info.H.Cmp(want) != 0 {
		t.Fatalf("b.H = %v, want %v (recomputed with current L)", info.H, want)
	}
	if info.Freq != 2 {
		t.Fatalf("b.Freq = %d, want 2", info.Freq)
	}
}

// One Put evicting several entries advances L victim by victim,
// monotonically, ending at the last victim's H.
func TestMultiEvictionAdvancesLStepwise(t *testing.T) {
	c := mustCache(t, 3)
	mustPut(t, c, "a", 1, 1)
	mustPut(t, c, "b", 1, 2)
	mustPut(t, c, "c", 1, 3)
	evicted := mustPut(t, c, "big", 3, 2)
	if !reflect.DeepEqual(evicted, []string{"a", "b", "c"}) {
		t.Fatalf("evicted = %v, want [a b c] in ascending-H order", evicted)
	}
	// L moved 1 -> 2 -> 3, one victim at a time; final L is the last
	// victim's H, and the new entry's H is computed from that final L.
	if got, want := c.L(), rat(t, 3, 1); got.Cmp(want) != 0 {
		t.Fatalf("L = %v, want %v", got, want)
	}
	info := mustPeek(t, c, "big")
	if want := rat(t, 11, 3); info.H.Cmp(want) != 0 {
		t.Fatalf("big.H = %v, want %v (L=3 plus 1*2/3)", info.H, want)
	}
}

// Equal H values evict the entry with the smaller last tick.
func TestTieBreaksBySmallerLast(t *testing.T) {
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, 3) // H=3, last=0
	mustPut(t, c, "b", 1, 3) // H=3, last=1
	evicted := mustPut(t, c, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"a"}) {
		t.Fatalf("evicted = %v, want [a] (smaller last wins the tie)", evicted)
	}
	// Hits refresh last; with H tied again, the entry touched earlier loses.
	d := mustCache(t, 2)
	mustPut(t, d, "a", 1, 3)
	mustPut(t, d, "b", 1, 3)
	mustGet(t, d, "a") // a: H=6, last=2
	mustGet(t, d, "b") // b: H=6, last=3
	evicted = mustPut(t, d, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"a"}) {
		t.Fatalf("evicted = %v, want [a] (tie on H=6, last 2 < 3)", evicted)
	}
	// Same setup, opposite hit order: now b's last is the smaller one.
	e := mustCache(t, 2)
	mustPut(t, e, "a", 1, 3)
	mustPut(t, e, "b", 1, 3)
	mustGet(t, e, "b")
	mustGet(t, e, "a")
	evicted = mustPut(t, e, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"b"}) {
		t.Fatalf("evicted = %v, want [b] (tie on H=6, last 3 < 4)", evicted)
	}
}

// Overwriting removes the old entry first (freeing bytes) and does not
// advance L by itself.
func TestOverwriteRemovesOldWithoutAdvancingL(t *testing.T) {
	c := mustCache(t, 3)
	mustPut(t, c, "a", 2, 7)
	mustPut(t, c, "b", 1, 1)
	evicted := mustPut(t, c, "a", 1, 5) // frees 2 bytes, then 1+1 <= 3
	if len(evicted) != 0 {
		t.Fatalf("evicted = %v, want none (old entry freed enough room)", evicted)
	}
	if got := c.L(); got.Sign() != 0 {
		t.Fatalf("L = %v, want 0 (overwrite alone must not advance L)", got)
	}
	if got := c.Used(); got != 2 {
		t.Fatalf("Used = %d, want 2", got)
	}
	info := mustPeek(t, c, "a")
	if info.Freq != 1 {
		t.Fatalf("a.Freq = %d, want 1 (overwrite resets the entry)", info.Freq)
	}
	if want := rat(t, 5, 1); info.H.Cmp(want) != 0 {
		t.Fatalf("a.H = %v, want %v", info.H, want)
	}
	if info.Last != 2 {
		t.Fatalf("a.Last = %d, want 2 (third successful mutating op)", info.Last)
	}
}

// used+size == Cap exactly inserts without evicting.
func TestExactFitDoesNotEvict(t *testing.T) {
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, 1)
	evicted := mustPut(t, c, "b", 1, 1)
	if len(evicted) != 0 {
		t.Fatalf("evicted = %v, want none (used+size == Cap exactly)", evicted)
	}
	if got := c.Used(); got != 2 {
		t.Fatalf("Used = %d, want 2", got)
	}
	if got := c.L(); got.Sign() != 0 {
		t.Fatalf("L = %v, want 0", got)
	}
}

// No admission filtering: a new entry whose H is below every existing
// entry's H is still inserted.
func TestNoAdmissionFilter(t *testing.T) {
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, 100) // H=100
	evicted := mustPut(t, c, "b", 1, 1)
	if len(evicted) != 0 {
		t.Fatalf("evicted = %v, want none", evicted)
	}
	info := mustPeek(t, c, "b")
	if want := rat(t, 1, 1); info.H.Cmp(want) != 0 {
		t.Fatalf("b.H = %v, want %v", info.H, want)
	}
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (low-H entry admitted)", c.Len())
	}
}

// Exact rational arithmetic: with float64, 2^54+1 and 2^54+2 both round to
// 2^54, so a float cache would see a tie and evict the older key "a".
// Exact arithmetic sees H(b) < H(a) and must evict "b".
func TestExactRationalsWhereFloat64Fails(t *testing.T) {
	const (
		costA = 1<<54 + 2
		costB = 1<<54 + 1
	)
	if float64(costA) != float64(costB) {
		t.Fatalf("test premise broken: float64(%d) != float64(%d)", costA, costB)
	}
	c := mustCache(t, 2)
	mustPut(t, c, "a", 1, costA) // larger H, smaller last
	mustPut(t, c, "b", 1, costB) // smaller H, larger last
	evicted := mustPut(t, c, "c", 1, 1)
	if !reflect.DeepEqual(evicted, []string{"b"}) {
		t.Fatalf("evicted = %v, want [b]; float64 arithmetic would tie and evict [a]", evicted)
	}
	if got, want := c.L(), rat(t, costB, 1); got.Cmp(want) != 0 {
		t.Fatalf("L = %v, want %v", got, want)
	}
}

// Validation rejects with distinguishable reasons, reporting only the
// first failure in the order: empty key, size <= 0, cost < 1, size > Cap.
func TestRejectionOrder(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrNonPositiveCapacity) {
		t.Fatalf("New(0) = %v, want ErrNonPositiveCapacity", err)
	}
	if _, err := New(-5); !errors.Is(err, ErrNonPositiveCapacity) {
		t.Fatalf("New(-5) = %v, want ErrNonPositiveCapacity", err)
	}
	c := mustCache(t, 4)
	cases := []struct {
		name       string
		key        string
		size, cost int64
		want       error
	}{
		{"empty key wins over all", "", 0, 0, ErrEmptyKey},
		{"empty key wins over oversize", "", 100, 1, ErrEmptyKey},
		{"bad size beats bad cost", "k", 0, 0, ErrNonPositiveSize},
		{"bad size beats oversize", "k", -1, 1, ErrNonPositiveSize},
		{"bad cost beats oversize", "k", 100, 0, ErrInvalidCost},
		{"oversize reported last", "k", 5, 1, ErrSizeExceedsCapacity},
	}
	for _, tc := range cases {
		if _, err := c.Put(tc.key, tc.size, tc.cost); !errors.Is(err, tc.want) {
			t.Errorf("%s: Put = %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, err := c.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Get(\"\") = %v, want ErrEmptyKey", err)
	}
	if _, err := c.Peek(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Peek(\"\") = %v, want ErrEmptyKey", err)
	}
	if _, err := c.Peek("missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Peek(missing) = %v, want ErrNotFound", err)
	}
}

// A rejected operation (including a rejected overwrite) changes nothing:
// no entry, L, or tick is touched.
func TestRejectedOpsChangeNothing(t *testing.T) {
	c := mustCache(t, 4)
	mustPut(t, c, "a", 2, 3)
	before := mustPeek(t, c, "a")
	if _, err := c.Put("a", 5, 1); !errors.Is(err, ErrSizeExceedsCapacity) {
		t.Fatalf("rejected overwrite: %v, want ErrSizeExceedsCapacity", err)
	}
	if _, err := c.Put("b", 0, 1); !errors.Is(err, ErrNonPositiveSize) {
		t.Fatalf("rejected insert: %v, want ErrNonPositiveSize", err)
	}
	if _, err := c.Get(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("rejected get: %v, want ErrEmptyKey", err)
	}
	if hit, err := c.Get("missing"); err != nil || hit {
		t.Fatalf("Get(missing) = %v, %v, want false, nil", hit, err)
	}
	after := mustPeek(t, c, "a")
	if before.Freq != after.Freq || before.Last != after.Last || before.H.Cmp(after.H) != 0 {
		t.Fatalf("entry a changed after rejected ops: %+v -> %+v", before, after)
	}
	if got := c.L(); got.Sign() != 0 {
		t.Fatalf("L = %v, want 0 after rejected ops", got)
	}
	// tick must not have advanced: the next insert gets last == 1.
	mustPut(t, c, "b", 1, 1)
	if info := mustPeek(t, c, "b"); info.Last != 1 {
		t.Fatalf("b.Last = %d, want 1 (rejected ops must not consume ticks)", info.Last)
	}
	if got := c.Used(); got != 3 {
		t.Fatalf("Used = %d, want 3", got)
	}
}

// Replaying the same operation sequence reproduces the exact same
// eviction sequences and L.
func TestReplayDeterminism(t *testing.T) {
	run := func() ([][]string, string) {
		c := mustCache(t, 6)
		var evictions [][]string
		evictions = append(evictions, mustPut(t, c, "a", 2, 3))
		evictions = append(evictions, mustPut(t, c, "b", 3, 7))
		mustGet(t, c, "a")
		evictions = append(evictions, mustPut(t, c, "c", 4, 1))
		evictions = append(evictions, mustPut(t, c, "a", 1, 2))
		mustGet(t, c, "c")
		evictions = append(evictions, mustPut(t, c, "d", 6, 5))
		return evictions, c.L().String()
	}
	ev1, l1 := run()
	ev2, l2 := run()
	if !reflect.DeepEqual(ev1, ev2) {
		t.Fatalf("eviction sequences differ: %v vs %v", ev1, ev2)
	}
	if l1 != l2 {
		t.Fatalf("L differs: %v vs %v", l1, l2)
	}
}
