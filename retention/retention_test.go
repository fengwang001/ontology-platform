package retention

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

var specRules = []Rule{{Until: 60, Step: 20}, {Until: 300, Step: 100}, {Until: 1000, Step: 300}}

func mustCleaner(t *testing.T, rules []Rule, maxCount, maxBytes, trashTTL int64) *Cleaner {
	t.Helper()
	c, err := NewCleaner(rules, maxCount, maxBytes, trashTTL)
	if err != nil {
		t.Fatalf("NewCleaner(%v, %d, %d, %d): %v", rules, maxCount, maxBytes, trashTTL, err)
	}
	return c
}

func mustAdd(t *testing.T, c *Cleaner, now int64, file string, ts ...int64) {
	t.Helper()
	for _, vt := range ts {
		if err := c.Add(now, file, vt, 1); err != nil {
			t.Fatalf("Add(%d, %q, %d, 1): %v", now, file, vt, err)
		}
	}
}

func versionTs(vs []Version) []int64 {
	out := make([]int64, len(vs))
	for i, v := range vs {
		out[i] = v.T
	}
	return out
}

func checkVersions(t *testing.T, c *Cleaner, file string, want []int64) {
	t.Helper()
	got := versionTs(c.Versions(file))
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Versions(%q) = %v, want %v", file, got, want)
	}
}

func checkDeleted(t *testing.T, res CleanResult, want []Deletion) {
	t.Helper()
	if !reflect.DeepEqual(res.Deleted, want) {
		t.Errorf("Deleted = %v, want %v", res.Deleted, want)
	}
}

func TestNewCleanerValidation(t *testing.T) {
	cases := []struct {
		name               string
		rules              []Rule
		maxCount, maxBytes int64
		trashTTL           int64
	}{
		{"empty rules", nil, 1, 1, 1},
		{"until zero", []Rule{{Until: 0, Step: 1}}, 1, 1, 1},
		{"step zero", []Rule{{Until: 1, Step: 0}}, 1, 1, 1},
		{"until not increasing", []Rule{{Until: 10, Step: 1}, {Until: 10, Step: 1}}, 1, 1, 1},
		{"until decreasing", []Rule{{Until: 10, Step: 1}, {Until: 5, Step: 1}}, 1, 1, 1},
		{"maxCount zero", []Rule{{Until: 1, Step: 1}}, 0, 1, 1},
		{"maxBytes zero", []Rule{{Until: 1, Step: 1}}, 1, 0, 1},
		{"trashTTL zero", []Rule{{Until: 1, Step: 1}}, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewCleaner(tc.rules, tc.maxCount, tc.maxBytes, tc.trashTTL); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := NewCleaner(specRules, 1, 1, 1); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
}

func TestAddErrorPrecedence(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 100)
	mustAdd(t, c, 10, "f", 5)

	// ErrClock beats everything (duplicate + invalid would also match).
	if err := c.Add(9, "f", 5, 0); !errors.Is(err, ErrClock) {
		t.Fatalf("Add with older now: got %v, want ErrClock", err)
	}
	// Invalid args: empty file, negative t, t > now, size < 1.
	for _, a := range []struct {
		file string
		t, s int64
	}{{"", 1, 1}, {"f", -1, 1}, {"f", 11, 1}, {"f", 1, 0}} {
		if err := c.Add(10, a.file, a.t, a.s); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Add(10, %q, %d, %d): got %v, want ErrInvalid", a.file, a.t, a.s, err)
		}
	}
	// Duplicate beats ErrTooLarge.
	if err := c.Add(10, "f", 5, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate Add: got %v, want ErrDuplicate", err)
	}
	// Rejected calls must not change state.
	checkVersions(t, c, "f", []int64{5})
}

func TestAddDuplicateInTrash(t *testing.T) {
	c := mustCleaner(t, []Rule{{Until: 1000, Step: 1}}, 1, 1<<50, 5)
	mustAdd(t, c, 10, "f", 1, 2)
	if _, err := c.Clean(10); err != nil {
		t.Fatal(err)
	}
	// t=1 was deleted (OverCount) and sits in the trash: still a duplicate.
	if err := c.Add(10, "f", 1, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Add over trashed t: got %v, want ErrDuplicate", err)
	}
	// Even after the TTL elapsed (but before Clean purges it).
	if err := c.Add(100, "f", 1, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Add over expired trash entry: got %v, want ErrDuplicate", err)
	}
	// After Clean purges it, the same t can be added again.
	if _, err := c.Clean(100); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(100, "f", 1, 1); err != nil {
		t.Fatalf("Add after purge: %v", err)
	}
}

func TestAddTooLarge(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<60, 100)
	// Exactly 10^15 is allowed (not strictly greater).
	if err := c.Add(0, "f", 0, 1_000_000_000_000_000); err != nil {
		t.Fatalf("Add size 10^15: %v", err)
	}
	if _, bytes := c.Totals("f"); bytes != 1_000_000_000_000_000 {
		t.Fatalf("Totals bytes = %d, want 10^15", bytes)
	}
	if err := c.Add(1, "f", 1, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Add past 10^15: got %v, want ErrTooLarge", err)
	}
	// Boundary: sum 10^15-5, size 5 fits, size 6 does not.
	if err := c.Add(0, "g", 0, 1_000_000_000_000_000-5); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(1, "g", 1, 6); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Add size 6 over 10^15-5: got %v, want ErrTooLarge", err)
	}
	if err := c.Add(1, "g", 1, 5); err != nil {
		t.Fatalf("Add size 5 over 10^15-5: %v", err)
	}
}

func TestTierBoundary(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 1000)
	// Gap exactly equal to Step is kept.
	mustAdd(t, c, 50, "eq-step", 0, 20, 50)
	res, err := c.Clean(50)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, nil)
	checkVersions(t, c, "eq-step", []int64{0, 20, 50})

	// Age exactly equal to Until belongs to the next tier (Step 100, not 20):
	// t=40 has age 60 at now=100, so gap 40 < 100 thins it, while t=41 with
	// age 59 stays in tier 0 (Step 20) and is kept.
	c2 := mustCleaner(t, specRules, 100, 1<<50, 1000)
	mustAdd(t, c2, 100, "eq-until", 0, 40, 100)
	mustAdd(t, c2, 100, "lt-until", 0, 41, 100)
	res, err = c2.Clean(100)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{File: "eq-until", T: 40, Reason: ReasonThinned}})
	checkVersions(t, c2, "eq-until", []int64{0, 100})
	checkVersions(t, c2, "lt-until", []int64{0, 41, 100})
}

func TestSpecWorkedExample(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 50)
	mustAdd(t, c, 45, "f", 0, 10, 30, 45)

	res, err := c.Clean(45)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 10, ReasonThinned}})
	checkVersions(t, c, "f", []int64{0, 30, 45})

	mustAdd(t, c, 75, "f", 70)
	res, err = c.Clean(75)
	if err != nil {
		t.Fatal(err)
	}
	// t=45 is no longer the latest and is 15 < 20 from prev t=30.
	checkDeleted(t, res, []Deletion{{"f", 45, ReasonThinned}})
	checkVersions(t, c, "f", []int64{0, 30, 70})

	res, err = c.Clean(200)
	if err != nil {
		t.Fatal(err)
	}
	// t=30 now has age 170 (tier 1, Step 100): 30 < 100 thins it.
	checkDeleted(t, res, []Deletion{{"f", 30, ReasonThinned}})
	// Trash TTL 50: t=10 (deleted at 45) and t=45 (deleted at 75) both expire.
	if want := []Purge{{"f", 10}, {"f", 45}}; !reflect.DeepEqual(res.Purged, want) {
		t.Fatalf("Purged = %v, want %v", res.Purged, want)
	}
	checkVersions(t, c, "f", []int64{0, 70})
}

func TestPinChangesThinningBaseline(t *testing.T) {
	// Pinned t=10 becomes the spacing baseline, so t=25 (gap 15 < 20) dies.
	c := mustCleaner(t, specRules, 100, 1<<50, 1000)
	mustAdd(t, c, 30, "f", 0, 10, 25, 29)
	if err := c.Pin("f", 10); err != nil {
		t.Fatal(err)
	}
	res, err := c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 25, ReasonThinned}})
	checkVersions(t, c, "f", []int64{0, 10, 29})

	// Unpinning later cannot bring the deleted t=25 back; t=10 itself now
	// becomes an ordinary version and is thinned (gap 10 < 20 from t=0).
	if err := c.Unpin("f", 10); err != nil {
		t.Fatal(err)
	}
	res, err = c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 10, ReasonThinned}})
	checkVersions(t, c, "f", []int64{0, 29})

	// Without the pin, t=10 dies instead and t=25 survives off baseline t=0.
	c2 := mustCleaner(t, specRules, 100, 1<<50, 1000)
	mustAdd(t, c2, 30, "f", 0, 10, 25, 29)
	res, err = c2.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 10, ReasonThinned}})
	checkVersions(t, c2, "f", []int64{0, 25, 29})
}

func TestPinUnpinErrorsAndIdempotence(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 1000)
	mustAdd(t, c, 10, "f", 1)
	if err := c.Pin("f", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Pin("f", 1); err != nil { // idempotent
		t.Fatal(err)
	}
	if err := c.Unpin("f", 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Unpin("f", 1); err != nil { // idempotent
		t.Fatal(err)
	}
	for _, err := range []error{c.Pin("f", 2), c.Unpin("f", 2), c.Pin("g", 1), c.Unpin("g", 1)} {
		if !errors.Is(err, ErrNoVersion) {
			t.Fatalf("got %v, want ErrNoVersion", err)
		}
	}
	// A version in the trash is "not there" for Pin/Unpin.
	c2 := mustCleaner(t, []Rule{{Until: 1000, Step: 1}}, 1, 1<<50, 100)
	mustAdd(t, c2, 10, "f", 1, 2)
	if _, err := c2.Clean(10); err != nil {
		t.Fatal(err)
	}
	if err := c2.Pin("f", 1); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("Pin of trashed version: got %v, want ErrNoVersion", err)
	}
}

func TestAgedPinnedAndLatest(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 10000)
	// t=0 over-aged and unpinned: Aged. t=500 over-aged but pinned: kept.
	// t=1500 in the last tier: kept. t=2000 latest: kept.
	mustAdd(t, c, 2000, "f", 0, 500, 1500, 2000)
	if err := c.Pin("f", 500); err != nil {
		t.Fatal(err)
	}
	// The latest version is kept even when itself over-aged.
	mustAdd(t, c, 2000, "old", 0)

	res, err := c.Clean(2000)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 0, ReasonAged}})
	checkVersions(t, c, "f", []int64{500, 1500, 2000})
	checkVersions(t, c, "old", []int64{0})
}

func TestLimitDeletionOrderAndReasons(t *testing.T) {
	// Step 1 never thins (gaps are >= 1); maxCount 4, maxBytes 25.
	c := mustCleaner(t, []Rule{{Until: 10000, Step: 1}}, 4, 25, 1000)
	for vt := int64(1); vt <= 6; vt++ {
		if err := c.Add(10, "f", vt, 10); err != nil {
			t.Fatal(err)
		}
	}
	res, err := c.Clean(10)
	if err != nil {
		t.Fatal(err)
	}
	// Count 6 > 4: t=1, t=2 die OverCount. Then bytes 40 > 25 with count 4:
	// t=3, t=4 die OverBytes, stopping at 20 bytes.
	checkDeleted(t, res, []Deletion{
		{"f", 1, ReasonOverCount},
		{"f", 2, ReasonOverCount},
		{"f", 3, ReasonOverBytes},
		{"f", 4, ReasonOverBytes},
	})
	checkVersions(t, c, "f", []int64{5, 6})
	if n, b := c.Totals("f"); n != 2 || b != 20 {
		t.Fatalf("Totals = (%d, %d), want (2, 20)", n, b)
	}
}

func TestLimitsKeepPinnedAndLatest(t *testing.T) {
	c := mustCleaner(t, []Rule{{Until: 10000, Step: 1}}, 2, 15, 1000)
	for vt := int64(1); vt <= 3; vt++ {
		if err := c.Add(10, "f", vt, 10); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Pin("f", 1); err != nil {
		t.Fatal(err)
	}
	res, err := c.Clean(10)
	if err != nil {
		t.Fatal(err)
	}
	// t=2 dies OverCount; then bytes 20 > 15 but only pinned t=1 and latest
	// t=3 remain, so the cleaner stops still over the byte limit.
	checkDeleted(t, res, []Deletion{{"f", 2, ReasonOverCount}})
	checkVersions(t, c, "f", []int64{1, 3})
	if n, b := c.Totals("f"); n != 2 || b != 20 {
		t.Fatalf("Totals = (%d, %d), want (2, 20)", n, b)
	}
}

func TestCleanIdempotentAtSameNow(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 1000)
	mustAdd(t, c, 45, "f", 0, 10, 30, 45)
	first, err := c.Clean(45)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Deleted) == 0 {
		t.Fatal("expected deletions in first Clean")
	}
	second, err := c.Clean(45)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, second, nil)
	if len(second.Purged) != 0 {
		t.Fatalf("second Clean purged %v, want none", second.Purged)
	}
}

func TestUndeleteBoundary(t *testing.T) {
	c := mustCleaner(t, []Rule{{Until: 10000, Step: 1}}, 1, 1<<50, 10)
	mustAdd(t, c, 10, "f", 1, 2)
	if _, err := c.Clean(10); err != nil { // t=1 -> trash at deletedAt=10
		t.Fatal(err)
	}
	if got := c.Trash("f"); len(got) != 1 || got[0].T != 1 || got[0].DeletedAt != 10 {
		t.Fatalf("Trash = %v, want [(1, 1, 10)]", got)
	}
	// now-deletedAt = 9 < TTL: restored.
	if err := c.Undelete(19, "f", 1); err != nil {
		t.Fatalf("Undelete at TTL-1: %v", err)
	}
	checkVersions(t, c, "f", []int64{1, 2})
	if got := c.Trash("f"); len(got) != 0 {
		t.Fatalf("Trash after undelete = %v, want empty", got)
	}
	// Deleted again at now=20.
	if _, err := c.Clean(20); err != nil {
		t.Fatal(err)
	}
	// now-deletedAt = 10 >= TTL: treated as absent even though not purged.
	if err := c.Undelete(30, "f", 1); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("Undelete at TTL boundary: got %v, want ErrNoVersion", err)
	}
	if got := c.Trash("f"); len(got) != 1 {
		t.Fatalf("expired entry should still sit in trash until Clean, got %v", got)
	}
	res, err := c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Purge{{"f", 1}}; !reflect.DeepEqual(res.Purged, want) {
		t.Fatalf("Purged = %v, want %v", res.Purged, want)
	}
	if got := c.Trash("f"); len(got) != 0 {
		t.Fatalf("Trash after purge = %v, want empty", got)
	}
	if err := c.Undelete(30, "f", 1); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("Undelete after purge: got %v, want ErrNoVersion", err)
	}
}

func TestUndeleteErrors(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 100)
	mustAdd(t, c, 10, "f", 1)
	for _, err := range []error{
		c.Undelete(10, "f", 1), // active, not trashed
		c.Undelete(10, "f", 2), // unknown t
		c.Undelete(10, "g", 1), // unknown file
	} {
		if !errors.Is(err, ErrNoVersion) {
			t.Fatalf("got %v, want ErrNoVersion", err)
		}
	}
}

func TestUndeleteRestoresForNormalCleaning(t *testing.T) {
	c := mustCleaner(t, []Rule{{Until: 1000, Step: 20}}, 100, 1<<50, 100)
	mustAdd(t, c, 30, "f", 0, 10, 30)
	res, err := c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 10, ReasonThinned}})
	if err := c.Undelete(30, "f", 10); err != nil {
		t.Fatal(err)
	}
	checkVersions(t, c, "f", []int64{0, 10, 30})
	// The restored version is judged normally by the next Clean.
	res, err = c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, []Deletion{{"f", 10, ReasonThinned}})
}

func TestIncrementalVsOneShot(t *testing.T) {
	// Non-monotonic steps: young versions need spacing 50, old ones only 10.
	rules := []Rule{{Until: 100, Step: 50}, {Until: 1000, Step: 10}}

	incremental := mustCleaner(t, rules, 100, 1<<50, 10000)
	mustAdd(t, incremental, 70, "f", 0, 30, 60)
	res, err := incremental.Clean(70)
	if err != nil {
		t.Fatal(err)
	}
	// At now=70 all versions are young (Step 50): t=30 dies (30 < 50).
	checkDeleted(t, res, []Deletion{{"f", 30, ReasonThinned}})
	res, err = incremental.Clean(500)
	if err != nil {
		t.Fatal(err)
	}
	checkDeleted(t, res, nil)
	checkVersions(t, incremental, "f", []int64{0, 60})

	oneShot := mustCleaner(t, rules, 100, 1<<50, 10000)
	mustAdd(t, oneShot, 70, "f", 0, 30, 60)
	res, err = oneShot.Clean(500)
	if err != nil {
		t.Fatal(err)
	}
	// At now=500 every version is old (Step 10): all gaps >= 10, nothing dies.
	checkDeleted(t, res, nil)
	checkVersions(t, oneShot, "f", []int64{0, 30, 60})

	if reflect.DeepEqual(versionTs(incremental.Versions("f")), versionTs(oneShot.Versions("f"))) {
		t.Fatal("incremental and one-shot cleaning should differ")
	}
}

func TestThinnedScanCounter(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 100000)
	mustAdd(t, c, 45, "a", 0, 10, 30, 45)
	mustAdd(t, c, 45, "b", 0, 20, 45)

	c.thinnedScans = 0
	if _, err := c.Clean(45); err != nil {
		t.Fatal(err)
	}
	// No Aged deletions: every version is examined exactly once.
	if c.thinnedScans != 7 {
		t.Fatalf("thinnedScans = %d, want 7", c.thinnedScans)
	}

	c.thinnedScans = 0
	if _, err := c.Clean(2000); err != nil {
		t.Fatal(err)
	}
	// At now=2000 only the latest of each file survives the Aged phase.
	if c.thinnedScans != 2 {
		t.Fatalf("thinnedScans = %d, want 2", c.thinnedScans)
	}
}

func TestClockErrors(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 100)
	mustAdd(t, c, 10, "f", 5)
	if _, err := c.Clean(9); !errors.Is(err, ErrClock) {
		t.Fatalf("Clean(9): got %v, want ErrClock", err)
	}
	if err := c.Undelete(9, "f", 5); !errors.Is(err, ErrClock) {
		t.Fatalf("Undelete(9): got %v, want ErrClock", err)
	}
	// Rejected calls leave the clock and the data untouched.
	if err := c.Add(10, "f", 6, 1); err != nil {
		t.Fatalf("Add at same now after ErrClock: %v", err)
	}
	if _, err := c.Clean(10); err != nil {
		t.Fatalf("Clean at same now: %v", err)
	}
	checkVersions(t, c, "f", []int64{5, 6})
}

func TestDeletedAndPurgedOrdering(t *testing.T) {
	c := mustCleaner(t, []Rule{{Until: 1000, Step: 20}}, 100, 1<<50, 5)
	mustAdd(t, c, 30, "b", 1, 3, 30)
	mustAdd(t, c, 30, "a", 0, 5, 8, 30)
	res, err := c.Clean(30)
	if err != nil {
		t.Fatal(err)
	}
	// Deleted sorted by file (byte order) then t.
	checkDeleted(t, res, []Deletion{
		{"a", 5, ReasonThinned},
		{"a", 8, ReasonThinned},
		{"b", 3, ReasonThinned},
	})
	// Purged sorted by (file, t) as well.
	res, err = c.Clean(40)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Purge{{"a", 5}, {"a", 8}, {"b", 3}}; !reflect.DeepEqual(res.Purged, want) {
		t.Fatalf("Purged = %v, want %v", res.Purged, want)
	}
}

func TestQueriesOnUnknownFile(t *testing.T) {
	c := mustCleaner(t, specRules, 100, 1<<50, 100)
	if got := c.Versions("nope"); len(got) != 0 {
		t.Fatalf("Versions = %v, want empty", got)
	}
	if n, b := c.Totals("nope"); n != 0 || b != 0 {
		t.Fatalf("Totals = (%d, %d), want (0, 0)", n, b)
	}
	if got := c.Trash("nope"); len(got) != 0 {
		t.Fatalf("Trash = %v, want empty", got)
	}
}

func TestConcurrentCalls(t *testing.T) {
	c := mustCleaner(t, specRules, 50, 1<<40, 20)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			file := fmt.Sprintf("f%d", g%3)
			for i := 0; i < 200; i++ {
				now := int64(i)
				_ = c.Add(now, file, int64(g*1000+i), 1)
				_ = c.Pin(file, int64(g*1000+i))
				_ = c.Unpin(file, int64(g*1000+i))
				_, _ = c.Clean(now)
				_ = c.Undelete(now, file, int64(g*1000+i))
				_ = c.Versions(file)
				_, _ = c.Totals(file)
				_ = c.Trash(file)
			}
		}(g)
	}
	wg.Wait()
	// State must be internally consistent after the concurrent run.
	for g := 0; g < 3; g++ {
		file := fmt.Sprintf("f%d", g)
		n, _ := c.Totals(file)
		if got := len(c.Versions(file)); got != n {
			t.Fatalf("Totals count %d != len(Versions) %d for %q", n, got, file)
		}
	}
}
