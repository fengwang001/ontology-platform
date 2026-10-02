package pagecache

import (
	"errors"
	"testing"
)

func mustSet(t *testing.T, c *Cache, cg string, limit int64) {
	t.Helper()
	if err := c.SetLimit(cg, limit); err != nil {
		t.Fatalf("SetLimit(%q,%d): %v", cg, limit, err)
	}
}

func mustMap(t *testing.T, c *Cache, now int64, cg, name string, pages ...Page) {
	t.Helper()
	if err := c.Map(now, cg, name, pages); err != nil {
		t.Fatalf("Map(%d,%q,%q,%v): unexpected %v", now, cg, name, pages, err)
	}
}

func mustUnmap(t *testing.T, c *Cache, now int64, cg, name string) {
	t.Helper()
	if err := c.Unmap(now, cg, name); err != nil {
		t.Fatalf("Unmap(%d,%q,%q): unexpected %v", now, cg, name, err)
	}
}

func assertErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestWorkedExample(t *testing.T) {
	c, err := New(1000)
	if err != nil {
		t.Fatal(err)
	}
	mustSet(t, c, "A", 100)
	mustSet(t, c, "B", 100)

	mustMap(t, c, 1, "A", "f1", Page{"X", 40})
	if c.Used("A") != 40 || c.Total() != 40 {
		t.Fatalf("after f1: used A=%d total=%d", c.Used("A"), c.Total())
	}
	mustMap(t, c, 2, "B", "g1", Page{"X", 40})
	if c.Used("A") != 40 || c.Used("B") != 0 || c.Logical("B") != 40 {
		t.Fatalf("after g1: used A=%d B=%d logicalB=%d", c.Used("A"), c.Used("B"), c.Logical("B"))
	}
	mustUnmap(t, c, 3, "A", "f1")
	if c.Used("A") != 0 || c.Used("B") != 40 || c.Total() != 40 {
		t.Fatalf("after unmap f1: used A=%d B=%d total=%d", c.Used("A"), c.Used("B"), c.Total())
	}
	mustMap(t, c, 4, "A", "f2", Page{"X", 40})
	if c.Used("B") != 40 || c.Used("A") != 0 {
		t.Fatalf("after f2: used A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	mustUnmap(t, c, 5, "B", "g1")
	if c.Used("A") != 40 || c.Used("B") != 0 || c.Total() != 40 {
		t.Fatalf("after unmap g1: used A=%d B=%d total=%d", c.Used("A"), c.Used("B"), c.Total())
	}
}

// Equal since is broken by cgroup name; the takeover counts into delta.
func TestEqualSinceNameTieBreak(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "B", 100)
	mustSet(t, c, "A", 100)

	mustMap(t, c, 10, "B", "h", Page{"Y", 30})
	if c.Used("B") != 30 {
		t.Fatalf("B used = %d", c.Used("B"))
	}
	mustMap(t, c, 10, "A", "i", Page{"Y", 30})
	if c.Used("A") != 30 || c.Used("B") != 0 {
		t.Fatalf("takeover: used A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	if c.Total() != 30 {
		t.Fatalf("total = %d", c.Total())
	}
	mustSet(t, c, "A", 29)
	if !c.Over("A") {
		t.Fatal("A should be over after lowering its limit")
	}
}

// Repeated references keep since; only a 0 -> positive transition re-stamps it.
func TestSameCgroupRepeatedSince(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 1000)
	mustSet(t, c, "B", 1000)

	mustMap(t, c, 1, "A", "f1", Page{"X", 10})
	mustMap(t, c, 2, "A", "f2", Page{"X", 10}, Page{"X", 10})
	mustMap(t, c, 3, "B", "g", Page{"X", 10})
	if c.Used("A") != 10 || c.Used("B") != 0 {
		t.Fatalf("used A=%d B=%d", c.Used("A"), c.Used("B"))
	}

	mustUnmap(t, c, 4, "A", "f1")
	mustUnmap(t, c, 5, "A", "f2")
	if c.Used("B") != 10 || c.Used("A") != 0 {
		t.Fatalf("after A leaves: used A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	mustMap(t, c, 6, "A", "f3", Page{"X", 10})
	if c.Used("B") != 10 || c.Used("A") != 0 {
		t.Fatalf("re-acquire keeps B owner: used A=%d B=%d", c.Used("A"), c.Used("B"))
	}
	mustUnmap(t, c, 7, "B", "g")
	if c.Used("A") != 10 || c.Used("B") != 0 {
		t.Fatalf("ownership back to A: used A=%d B=%d", c.Used("A"), c.Used("B"))
	}
}

// Ownership migrates by smallest since, not smallest cgroup name.
func TestMigrationBySinceNotByName(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 1000)
	mustSet(t, c, "B", 1000)
	mustSet(t, c, "C", 1000)

	mustMap(t, c, 1, "A", "fa", Page{"X", 10})
	mustMap(t, c, 5, "C", "fc", Page{"X", 10})
	mustMap(t, c, 9, "B", "fb", Page{"X", 10})
	mustUnmap(t, c, 10, "A", "fa")
	if c.Used("C") != 10 || c.Used("B") != 0 {
		t.Fatalf("C must inherit: used B=%d C=%d", c.Used("B"), c.Used("C"))
	}
}

// Migration may push the receiver over limit; a later new-page Map is then
// rejected while a pure-reference Map is admitted.
func TestOverLimitAfterMigration(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 100)
	mustSet(t, c, "B", 30)

	mustMap(t, c, 1, "A", "f1", Page{"X", 40})
	mustMap(t, c, 2, "B", "g1", Page{"X", 40})
	mustUnmap(t, c, 3, "A", "f1")
	if !c.Over("B") || c.Used("B") != 40 {
		t.Fatalf("B over limit after migration: used=%d over=%v", c.Used("B"), c.Over("B"))
	}
	mustMap(t, c, 4, "B", "g2", Page{"X", 40})
	assertErr(t, c.Map(5, "B", "g3", []Page{{"Z", 1}}), ErrLimit)
	if c.Total() != 40 {
		t.Fatalf("rejected map changed total: %d", c.Total())
	}
}

func TestDeltaLimitBoundary(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 100)
	mustMap(t, c, 1, "A", "f1", Page{"X", 70})
	mustMap(t, c, 2, "A", "f2", Page{"Y", 30})
	if c.Used("A") != 100 {
		t.Fatalf("used = %d", c.Used("A"))
	}
	assertErr(t, c.Map(3, "A", "f3", []Page{{"Z", 1}}), ErrLimit)
}

func TestCapacityBoundaryAndPrecedence(t *testing.T) {
	c, _ := New(100)
	mustSet(t, c, "A", 100000)
	mustSet(t, c, "B", 100000)
	mustMap(t, c, 1, "A", "f1", Page{"X", 70})
	mustMap(t, c, 2, "A", "f2", Page{"Y", 30})
	assertErr(t, c.Map(3, "A", "f3", []Page{{"Z", 1}}), ErrCapacity)
	mustMap(t, c, 4, "B", "g1", Page{"X", 70}, Page{"Y", 30})
	if c.Total() != 100 {
		t.Fatalf("total = %d", c.Total())
	}

	// Capacity is checked before the per-cgroup limit.
	c2, _ := New(100)
	mustSet(t, c2, "C", 0)
	assertErr(t, c2.Map(1, "C", "h", []Page{{"Q", 101}}), ErrCapacity)
}

// A rejected takeover must not stamp since on the rejected holder.
func TestRejectedTakeoverDoesNotChangeSince(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "B", 100)
	mustSet(t, c, "A", 0)

	mustMap(t, c, 1, "B", "h", Page{"Y", 30})
	assertErr(t, c.Map(1, "A", "i", []Page{{"Y", 30}}), ErrLimit)
	if c.Used("B") != 30 || c.Logical("A") != 0 {
		t.Fatalf("state changed by rejected map: usedB=%d logicalA=%d", c.Used("B"), c.Logical("A"))
	}
	mustSet(t, c, "A", 100)
	mustMap(t, c, 2, "A", "i", Page{"Y", 30})
	if c.Used("B") != 30 || c.Used("A") != 0 {
		t.Fatalf("B keeps ownership: usedA=%d usedB=%d", c.Used("A"), c.Used("B"))
	}
}

func TestDuplicateIDsInMapping(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 1000)
	mustSet(t, c, "B", 1000)

	mustMap(t, c, 1, "A", "f1", Page{"X", 10}, Page{"X", 10})
	if c.Used("A") != 10 || c.Logical("A") != 10 || c.Total() != 10 {
		t.Fatalf("fresh counted once: used=%d logical=%d total=%d", c.Used("A"), c.Logical("A"), c.Total())
	}
	mustUnmap(t, c, 2, "A", "f1")
	if c.Total() != 0 || c.Logical("A") != 0 {
		t.Fatalf("page reclaimed: total=%d logical=%d", c.Total(), c.Logical("A"))
	}

	mustMap(t, c, 3, "B", "g1", Page{"X", 10})
	mustMap(t, c, 4, "A", "f2", Page{"X", 10}, Page{"X", 10})
	mustUnmap(t, c, 5, "A", "f2")
	if c.Used("B") != 10 || c.Total() != 10 {
		t.Fatalf("B owner survives: usedB=%d total=%d", c.Used("B"), c.Total())
	}
}

// Once a page loses every holder the cache forgets its size; the same ID may
// later be stored with a different size.
func TestReclaimAllowsResizing(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 1000)
	mustMap(t, c, 1, "A", "f1", Page{"X", 10})
	mustUnmap(t, c, 2, "A", "f1")
	mustMap(t, c, 3, "A", "f2", Page{"X", 25})
	if c.Total() != 25 || c.Used("A") != 25 {
		t.Fatalf("resized: total=%d used=%d", c.Total(), c.Used("A"))
	}
	assertErr(t, c.Map(4, "A", "f3", []Page{{"X", 26}}), ErrSizeMismatch)
	// Conflicting sizes within one mapping.
	assertErr(t, c.Map(5, "A", "f4", []Page{{"W", 1}, {"W", 2}}), ErrSizeMismatch)
}

func TestErrorOrder(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrParam) {
		t.Fatalf("New(0) = %v", err)
	}
	if _, err := New(1_000_000_000_000_001); !errors.Is(err, ErrParam) {
		t.Fatalf("New(>1e15) = %v", err)
	}
	if err := (&Cache{}).SetLimit("", 1); !errors.Is(err, ErrParam) {
		t.Fatalf("SetLimit empty = %v", err)
	}

	c, _ := New(10)
	mustSet(t, c, "A", 100)
	mustMap(t, c, 5, "A", "f1", Page{"X", 5})

	// Map order: ErrParam, ErrClock, ErrNoLimit, ErrExists, ...
	assertErr(t, c.Map(6, "A", "bad", nil), ErrParam)
	assertErr(t, c.Map(6, "A", "bad", []Page{{"", 1}}), ErrParam)
	assertErr(t, c.Map(6, "A", "bad", []Page{{"P", 0}}), ErrParam)
	assertErr(t, c.Map(4, "A", "bad", []Page{{"P", 1}}), ErrClock)
	assertErr(t, c.Map(6, "Z", "bad", []Page{{"P", 1}}), ErrNoLimit)
	assertErr(t, c.Map(6, "A", "f1", []Page{{"P", 1}}), ErrExists)
	assertErr(t, c.Map(6, "A", "bad", []Page{{"X", 6}}), ErrSizeMismatch)

	// Unmap order: ErrParam, ErrClock, ErrNoLimit, ErrNotFound.
	assertErr(t, c.Unmap(6, "", "f1"), ErrParam)
	assertErr(t, c.Unmap(4, "A", "f1"), ErrClock)
	assertErr(t, c.Unmap(6, "Z", "f1"), ErrNoLimit)
	assertErr(t, c.Unmap(6, "A", "nope"), ErrNotFound)

	// Rejected operations leave the clock untouched.
	mustMap(t, c, 5, "A", "f2", Page{"Y", 5})
	mustUnmap(t, c, 5, "A", "f2")
}
