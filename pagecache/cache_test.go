package pagecache

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cap int64) *Cache {
	t.Helper()
	c, err := New(cap)
	if err != nil {
		t.Fatalf("New(%d): %v", cap, err)
	}
	return c
}

func TestErrorOrdering(t *testing.T) {
	c := mustNew(t, 100)
	mustLimit(t, c, "A", 50)
	mustMap(t, c, 5, "A", "f", []Page{{ID: "X", Size: 10}})

	// Map: ErrParam -> ErrClock -> ErrNoLimit -> ErrExists ->
	// ErrSizeMismatch -> ErrCapacity -> ErrLimit.
	if err := c.Map(5, "A", "x", nil); !errors.Is(err, ErrParam) {
		t.Fatalf("empty pages: %v", err)
	}
	if err := c.Map(-1, "A", "x", []Page{{ID: "X", Size: 10}}); !errors.Is(err, ErrParam) {
		t.Fatalf("negative now: %v", err)
	}
	if err := c.Map(4, "A", "x", []Page{{ID: "X", Size: 10}}); !errors.Is(err, ErrClock) {
		t.Fatalf("backwards clock: %v", err)
	}
	if err := c.Map(5, "Z", "x", []Page{{ID: "X", Size: 10}}); !errors.Is(err, ErrNoLimit) {
		t.Fatalf("no limit: %v", err)
	}
	if err := c.Map(6, "A", "f", []Page{{ID: "X", Size: 10}}); !errors.Is(err, ErrExists) {
		t.Fatalf("exists: %v", err)
	}
	// Mismatch inside the same request comes before cache-level errors.
	if err := c.Map(6, "A", "m1", []Page{{ID: "X", Size: 10}, {ID: "X", Size: 11}}); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("intra-request mismatch: %v", err)
	}
	if err := c.Map(6, "A", "m2", []Page{{ID: "X", Size: 11}}); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("cache mismatch: %v", err)
	}

	// Unmap: ErrParam -> ErrClock -> ErrNoLimit -> ErrNotFound.
	if err := c.Unmap(-1, "A", "f"); !errors.Is(err, ErrParam) {
		t.Fatalf("unmap param: %v", err)
	}
	if err := c.Unmap(4, "A", "f"); !errors.Is(err, ErrClock) {
		t.Fatalf("unmap clock: %v", err)
	}
	if err := c.Unmap(6, "Z", "f"); !errors.Is(err, ErrNoLimit) {
		t.Fatalf("unmap nolimit: %v", err)
	}
	if err := c.Unmap(6, "A", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unmap notfound: %v", err)
	}

	// A rejected op changed nothing.
	checkInvariants(t, c, "after rejections")
	if c.Total() != 10 || c.Used("A") != 10 {
		t.Fatalf("total=%d A=%d", c.Total(), c.Used("A"))
	}
	if err := c.Unmap(6, "A", "f"); err != nil {
		t.Fatalf("valid unmap: %v", err)
	}
}

// Concurrent mutations must keep all accounting identities consistent.
func TestConcurrent(t *testing.T) {
	c := mustNew(t, 1_000_000)
	cgs := []string{"c0", "c1", "c2", "c3"}
	for _, cg := range cgs {
		mustLimit(t, c, cg, 1_000_000)
	}
	// Stable sizes per page ID so failures come only from the modeled rules.
	pageSize := func(id int) int64 { return int64(1 + (id*37)%200) }

	const goroutines = 8
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := uint64(g*7919 + 1)
			nextRand := func(bound uint64) int {
				rng = rng*6364136223846793005 + 1442695040888963407
				return int((rng >> 33) % bound)
			}
			var clock int64
			type placed struct {
				cg   string
				name string
			}
			var live []placed
			defer func() {
				for _, p := range live {
					clock++
					_ = c.Unmap(clock, p.cg, p.name)
				}
			}()
			for i := 0; i < 400; i++ {
				cg := cgs[nextRand(uint64(len(cgs)))]
				name := fmt.Sprintf("g%d-n%d", g, i)
				clock += int64(nextRand(3))
				nPages := nextRand(4) + 1
				pages := make([]Page, nPages)
				for j := range pages {
					id := nextRand(12)
					pages[j] = Page{ID: fmt.Sprintf("p%d", id), Size: pageSize(id)}
				}
				if err := c.Map(clock, cg, name, pages); err == nil {
					live = append(live, placed{cg, name})
				}
				if len(live) > 0 && nextRand(2) == 0 {
					idx := nextRand(uint64(len(live)))
					clock += int64(nextRand(3))
					if err := c.Unmap(clock, live[idx].cg, live[idx].name); err == nil {
						live[idx] = live[len(live)-1]
						live = live[:len(live)-1]
					}
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			_ = c.Total()
			for _, cg := range cgs {
				_ = c.Used(cg)
				_ = c.Over(cg)
			}
		}
	}()

	wg.Wait()
	checkInvariants(t, c, "post concurrent")
}

// Stealing ownership counts into delta; a rejected steal changes no state,
// including holder presence and since.
func TestStealCountsDeltaAndRejectionKeepsSince(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 10)
	mustLimit(t, c, "B", 100)
	mustMap(t, c, 1, "B", "b", []Page{{ID: "X", Size: 30}})
	if err := c.Map(1, "A", "a", []Page{{ID: "X", Size: 30}}); !errors.Is(err, ErrLimit) {
		t.Fatalf("steal err = %v, want ErrLimit", err)
	}
	if got := c.Logical("A"); got != 0 {
		t.Fatalf("A logical = %d after rejected steal, want 0", got)
	}
	if c.Used("B") != 30 || c.Total() != 30 {
		t.Fatalf("B=%d total=%d after rejection", c.Used("B"), c.Total())
	}
	// The rejected steal never advanced the clock; same-now steal works once
	// A has room, proving since was not touched.
	mustLimit(t, c, "A", 100)
	mustMap(t, c, 1, "B", "by", []Page{{ID: "Y", Size: 20}})
	mustMap(t, c, 1, "A", "ay", []Page{{ID: "Y", Size: 20}})
	if c.Used("A") != 20 || c.Used("B") != 30 {
		t.Fatalf("A=%d B=%d, want 20/30", c.Used("A"), c.Used("B"))
	}
}

// Duplicate IDs count once for fresh/delta but every occurrence is a ref.
func TestDuplicateIDsRefAndDelta(t *testing.T) {
	c := mustNew(t, 100)
	mustLimit(t, c, "A", 1000)
	mustLimit(t, c, "B", 1000)

	mustMap(t, c, 1, "A", "f", []Page{{ID: "X", Size: 30}, {ID: "X", Size: 30}})
	if c.Total() != 30 || c.Used("A") != 30 {
		t.Fatalf("total=%d A=%d, want 30/30", c.Total(), c.Used("A"))
	}
	mustMap(t, c, 2, "B", "g", []Page{
		{ID: "X", Size: 30}, {ID: "X", Size: 30}, {ID: "X", Size: 30},
	})
	if c.Total() != 30 || c.Used("A") != 30 || c.Logical("B") != 30 {
		t.Fatalf("total=%d A=%d logicalB=%d", c.Total(), c.Used("A"), c.Logical("B"))
	}
	if err := c.Unmap(3, "A", "f"); err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, c, "A unmap")
	if c.Total() != 30 || c.Used("B") != 30 {
		t.Fatalf("after A leaves: total=%d B=%d", c.Total(), c.Used("B"))
	}
	if err := c.Unmap(4, "B", "g"); err != nil {
		t.Fatal(err)
	}
	if c.Total() != 0 {
		t.Fatalf("total=%d after reclaim, want 0", c.Total())
	}
}

// A reclaimed page forgets its size; the same ID may reappear with a new size.
func TestReclaimedPageCanChangeSize(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 1000)
	mustMap(t, c, 1, "A", "f", []Page{{ID: "X", Size: 40}})
	if err := c.Unmap(2, "A", "f"); err != nil {
		t.Fatal(err)
	}
	mustMap(t, c, 3, "A", "g", []Page{{ID: "X", Size: 99}})
	if c.Total() != 99 || c.Used("A") != 99 {
		t.Fatalf("total=%d A=%d, want 99/99", c.Total(), c.Used("A"))
	}
	if err := c.Map(4, "A", "h", []Page{{ID: "X", Size: 100}}); !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("size mismatch err = %v, want ErrSizeMismatch", err)
	}
}

// Migration can push a receiver strictly over-limit; its later Map with a new
// page is rejected, but a dedup-only Map (delta 0) passes.
func TestOverLimitThenDedupAllowed(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 50)
	mustLimit(t, c, "B", 50)

	mustMap(t, c, 1, "A", "a", []Page{{ID: "X", Size: 40}})
	mustMap(t, c, 2, "B", "b", []Page{{ID: "X", Size: 40}, {ID: "Z", Size: 11}})
	if err := c.Unmap(3, "A", "a"); err != nil {
		t.Fatal(err)
	}
	if c.Used("B") != 51 || !c.Over("B") {
		t.Fatalf("B=%d over=%v, want 51/true", c.Used("B"), c.Over("B"))
	}
	mustMap(t, c, 4, "A", "a2", []Page{{ID: "X", Size: 40}})
	if err := c.Map(5, "B", "n", []Page{{ID: "N", Size: 1}}); !errors.Is(err, ErrLimit) {
		t.Fatalf("over-limit new page err = %v, want ErrLimit", err)
	}
	mustLimit(t, c, "C", 1)
	mustMap(t, c, 6, "C", "c", []Page{{ID: "W", Size: 1}})
	mustMap(t, c, 7, "B", "b2", []Page{{ID: "W", Size: 1}})
	if c.Used("B") != 51 || !c.Over("B") {
		t.Fatalf("B after dedup = %d over=%v", c.Used("B"), c.Over("B"))
	}
}

func TestLimitEqualityAndOneOver(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 30)
	mustMap(t, c, 1, "A", "f", []Page{{ID: "X", Size: 30}})

	c2 := mustNew(t, 10_000)
	mustLimit(t, c2, "A", 29)
	if err := c2.Map(1, "A", "f", []Page{{ID: "X", Size: 30}}); !errors.Is(err, ErrLimit) {
		t.Fatalf("one over limit err = %v, want ErrLimit", err)
	}
}

func TestCapacityEqualityOneOverOrder(t *testing.T) {
	c := mustNew(t, 100)
	mustLimit(t, c, "A", 100)
	mustMap(t, c, 1, "A", "f", []Page{{ID: "X", Size: 70}})
	mustMap(t, c, 2, "A", "g", []Page{{ID: "Y", Size: 30}})
	if err := c.Map(3, "A", "h", []Page{{ID: "Z", Size: 31}}); !errors.Is(err, ErrCapacity) {
		t.Fatalf("31-byte page err = %v, want ErrCapacity", err)
	}
	mustLimit(t, c, "B", 0)
	mustMap(t, c, 4, "B", "b", []Page{{ID: "X", Size: 70}})
	if c.Total() != 100 || c.rescans != 0 {
		t.Fatalf("total=%d rescans=%d", c.Total(), c.rescans)
	}
}

// Repeated references keep the original since; ref falling to zero then rising
// takes the new now.
func TestSinceStableThenReset(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 1000)
	mustLimit(t, c, "B", 1000)

	mustMap(t, c, 1, "A", "f", []Page{{ID: "X", Size: 10}, {ID: "X", Size: 10}})
	mustMap(t, c, 5, "B", "g", []Page{{ID: "X", Size: 10}})
	mustMap(t, c, 6, "A", "f2", []Page{{ID: "X", Size: 10}})
	if err := c.Unmap(7, "A", "f2"); err != nil {
		t.Fatal(err)
	}
	if c.Used("A") != 10 || c.Used("B") != 0 {
		t.Fatalf("after partial unmap A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	if err := c.Unmap(8, "A", "f"); err != nil {
		t.Fatal(err)
	}
	if c.Used("A") != 0 || c.Used("B") != 10 {
		t.Fatalf("after full unmap A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	mustMap(t, c, 9, "A", "f3", []Page{{ID: "X", Size: 10}})
	if c.Used("A") != 0 || c.Used("B") != 10 {
		t.Fatalf("after remap A=%d B=%d", c.Used("A"), c.Used("B"))
	}
}

// The owner leaving migrates to the smallest since, not smallest name.
func TestMigrationBySinceNotName(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "O", 1000)
	mustLimit(t, c, "B", 1000)
	mustLimit(t, c, "A", 1000)

	mustMap(t, c, 1, "O", "o", []Page{{ID: "P", Size: 10}})
	mustMap(t, c, 5, "B", "b", []Page{{ID: "P", Size: 10}})
	mustMap(t, c, 9, "A", "a", []Page{{ID: "P", Size: 10}})
	if err := c.Unmap(10, "O", "o"); err != nil {
		t.Fatal(err)
	}
	if c.Used("B") != 10 || c.Used("A") != 0 {
		t.Fatalf("owner left: B=%d A=%d, want B by since", c.Used("B"), c.Used("A"))
	}
	if err := c.Unmap(11, "B", "b"); err != nil {
		t.Fatal(err)
	}
	if c.Used("A") != 10 || c.Used("B") != 0 || c.Total() != 10 {
		t.Fatalf("second leave: A=%d B=%d total=%d", c.Used("A"), c.Used("B"), c.Total())
	}
	if err := c.Unmap(12, "A", "a"); err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, c, "reclaimed")
	if c.Total() != 0 {
		t.Fatalf("total after reclaim = %d, want 0", c.Total())
	}
}

// The two worked examples from the specification, step by step.
func TestSpecExamples(t *testing.T) {
	c := mustNew(t, 10_000)
	mustLimit(t, c, "A", 100)
	mustLimit(t, c, "B", 100)

	check := func(where string, ua, ub, tot int64) {
		t.Helper()
		checkInvariants(t, c, where)
		if c.Used("A") != ua || c.Used("B") != ub || c.Total() != tot {
			t.Fatalf("%s: used A=%d B=%d total=%d, want %d %d %d",
				where, c.Used("A"), c.Used("B"), c.Total(), ua, ub, tot)
		}
	}

	mustMap(t, c, 1, "A", "f1", []Page{{ID: "X", Size: 40}})
	check("now1", 40, 0, 40)
	mustMap(t, c, 2, "B", "g1", []Page{{ID: "X", Size: 40}})
	check("now2", 40, 0, 40)
	if err := c.Unmap(3, "A", "f1"); err != nil {
		t.Fatal(err)
	}
	check("now3", 0, 40, 40)
	mustMap(t, c, 4, "A", "f2", []Page{{ID: "X", Size: 40}})
	check("now4", 0, 40, 40)
	if err := c.Unmap(5, "B", "g1"); err != nil {
		t.Fatal(err)
	}
	check("now5", 40, 0, 40)

	mustMap(t, c, 10, "B", "h", []Page{{ID: "Y", Size: 30}})
	check("now10-B", 40, 30, 70)
	mustMap(t, c, 10, "A", "i", []Page{{ID: "Y", Size: 30}})
	check("now10-A", 70, 0, 70)
}

func mustLimit(t *testing.T, c *Cache, cg string, bytes int64) {
	t.Helper()
	if err := c.SetLimit(cg, bytes); err != nil {
		t.Fatalf("SetLimit(%q,%d): %v", cg, bytes, err)
	}
}

func mustMap(t *testing.T, c *Cache, now int64, cg, name string, pages []Page) {
	t.Helper()
	if err := c.Map(now, cg, name, pages); err != nil {
		t.Fatalf("Map(now=%d cg=%q name=%q pages=%v): %v", now, cg, name, pages, err)
	}
}

func TestNewParam(t *testing.T) {
	for _, cap := range []int64{0, -1, maxCap + 1} {
		if _, err := New(cap); !errors.Is(err, ErrParam) {
			t.Fatalf("New(%d) err = %v, want ErrParam", cap, err)
		}
	}
}

func TestSetLimitParam(t *testing.T) {
	c := mustNew(t, 100)
	for _, tc := range []struct {
		cg    string
		bytes int64
	}{
		{"", 10}, {"a", -1}, {"a", maxCap + 1},
	} {
		if err := c.SetLimit(tc.cg, tc.bytes); !errors.Is(err, ErrParam) {
			t.Fatalf("SetLimit(%q,%d) err = %v, want ErrParam", tc.cg, tc.bytes, err)
		}
	}
	mustLimit(t, c, "a", 5)
	mustLimit(t, c, "a", 3)
}

// checkInvariants verifies the global accounting identities on every step.
func checkInvariants(t *testing.T, c *Cache, where string) {
	t.Helper()
	var sumUsed int64
	for name, g := range c.groups {
		sumUsed += g.used
		var ownedSum int64
		for id, pi := range c.pages {
			if pi.owner == name {
				ownedSum += pi.size
			}
			h := pi.hold[name]
			if h != nil && h.ref <= 0 {
				t.Fatalf("%s: non-positive ref of %s for %s", where, id, name)
			}
			if _, ok := pi.hold[pi.owner]; !ok {
				t.Fatalf("%s: owner %s of %s is not a holder", where, pi.owner, id)
			}
		}
		if ownedSum != g.used {
			t.Fatalf("%s: maintained used(%s)=%d but owned sum=%d", where, name, g.used, ownedSum)
		}
	}
	if sumUsed != c.total {
		t.Fatalf("%s: sum used=%d != total=%d", where, sumUsed, c.total)
	}
	var totalSum int64
	var refSum int
	type occ struct {
		cg   string
		name string
	}
	occRef := map[occ]int{}
	for cg, g := range c.groups {
		for mname, m := range g.maps {
			for _, p := range m.pages {
				refSum++
				occRef[occ{cg, p.ID}]++
				_ = mname
			}
		}
	}
	var heldRef int
	for _, pi := range c.pages {
		totalSum += pi.size
		for _, h := range pi.hold {
			heldRef += h.ref
		}
	}
	if totalSum != c.total {
		t.Fatalf("%s: recomputed total=%d != maintained %d", where, totalSum, c.total)
	}
	if heldRef != refSum {
		t.Fatalf("%s: holder refs=%d != mapping occurrences=%d", where, heldRef, refSum)
	}
	for o, n := range occRef {
		pi := c.pages[o.name]
		if pi == nil {
			t.Fatalf("%s: references exist for reclaimed page %s", where, o.name)
		}
		if pi.hold[o.cg] == nil || pi.hold[o.cg].ref != n {
			t.Fatalf("%s: ref mismatch %s/%s", where, o.cg, o.name)
		}
	}
}
