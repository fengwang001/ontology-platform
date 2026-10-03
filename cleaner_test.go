package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustNew(t *testing.T, rules []Rule, maxCount, maxBytes, trashTTL int64) *Cleaner {
	t.Helper()
	c, err := New(rules, maxCount, maxBytes, trashTTL)
	if err != nil {
		t.Fatalf("New: unexpected error %v", err)
	}
	return c
}

func tsOf(vs []Version) []int64 {
	out := make([]int64, len(vs))
	for i, v := range vs {
		out[i] = v.T
	}
	return out
}

func eqInt64(a, b []int64) bool {
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

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		name     string
		rules    []Rule
		maxCount int64
		maxBytes int64
		trashTTL int64
	}{
		{"no rules", nil, 1, 1, 1},
		{"zero until", []Rule{{Until: 0, Step: 1}}, 1, 1, 1},
		{"zero step", []Rule{{Until: 1, Step: 0}}, 1, 1, 1},
		{"until not increasing", []Rule{{Until: 10, Step: 1}, {Until: 10, Step: 2}}, 1, 1, 1},
		{"until decreasing", []Rule{{Until: 20, Step: 1}, {Until: 10, Step: 2}}, 1, 1, 1},
		{"bad count", []Rule{{Until: 1, Step: 1}}, 0, 1, 1},
		{"bad bytes", []Rule{{Until: 1, Step: 1}}, 1, 0, 1},
		{"bad ttl", []Rule{{Until: 1, Step: 1}}, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.rules, tc.maxCount, tc.maxBytes, tc.trashTTL); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestClockMonotonic(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1000, Step: 1}}, 10, 1e15, 10)
	if err := c.Add(10, "f", 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(9, "f", 1, 1); !errors.Is(err, ErrClock) {
		t.Fatalf("Add backwards: want ErrClock, got %v", err)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0}) {
		t.Fatalf("state changed after rejected Add: %v", got)
	}
	if res := c.Clean(5); len(res.Deleted) != 0 || len(res.Purged) != 0 {
		t.Fatalf("Clean backwards changed state: %+v", res)
	}
	if err := c.Undelete(5, "f", 0); !errors.Is(err, ErrClock) {
		t.Fatalf("Undelete backwards: want ErrClock, got %v", err)
	}
	if err := c.Add(9, "", -1, 0); !errors.Is(err, ErrClock) {
		t.Fatalf("precedence: want ErrClock, got %v", err)
	}
}

func TestSpecExample(t *testing.T) {
	rules := []Rule{{Until: 60, Step: 20}, {Until: 300, Step: 100}, {Until: 1000, Step: 300}}
	c := mustNew(t, rules, 100, 1<<60, 1000)
	for _, v := range []int64{0, 10, 30, 45} {
		if err := c.Add(45, "f", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	res := c.Clean(45)
	wantDel := []Deletion{{File: "f", T: 10, Reason: ReasonThinned}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(wantDel) {
		t.Fatalf("Clean(45) deletions=%v", res.Deleted)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 30, 45}) {
		t.Fatalf("after Clean(45): %v", got)
	}
	if c.thinnedExams != 4 {
		t.Fatalf("thinnedExams=%d want 4", c.thinnedExams)
	}

	if err := c.Add(70, "f", 70, 1); err != nil {
		t.Fatal(err)
	}
	res = c.Clean(75)
	wantDel = []Deletion{{File: "f", T: 45, Reason: ReasonThinned}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(wantDel) {
		t.Fatalf("Clean(75) deletions=%v", res.Deleted)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 30, 70}) {
		t.Fatalf("after Clean(75): %v", got)
	}
	if c.thinnedExams != 4 {
		t.Fatalf("thinnedExams=%d want 4", c.thinnedExams)
	}

	res = c.Clean(200)
	wantDel = []Deletion{{File: "f", T: 30, Reason: ReasonThinned}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(wantDel) {
		t.Fatalf("Clean(200) deletions=%v", res.Deleted)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 70}) {
		t.Fatalf("after Clean(200): %v", got)
	}
	if c.thinnedExams != 3 {
		t.Fatalf("thinnedExams=%d want 3", c.thinnedExams)
	}

	res = c.Clean(200)
	if len(res.Deleted) != 0 || len(res.Purged) != 0 {
		t.Fatalf("repeat Clean not idempotent: %+v", res)
	}
}

func TestTierBoundaries(t *testing.T) {
	rules := []Rule{{Until: 60, Step: 20}, {Until: 300, Step: 100}}
	c := mustNew(t, rules, 100, 1<<60, 1000)
	for _, v := range []int64{0, 40, 100} {
		if err := c.Add(100, "f", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	res := c.Clean(100)
	want := []Deletion{{File: "f", T: 40, Reason: ReasonThinned}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("boundary age: deletions=%v", res.Deleted)
	}

	c2 := mustNew(t, []Rule{{Until: 1000, Step: 20}}, 100, 1<<60, 1000)
	for _, v := range []int64{0, 20, 40} {
		if err := c2.Add(50, "g", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	if res := c2.Clean(50); len(res.Deleted) != 0 {
		t.Fatalf("spacing == Step must be kept: %v", res.Deleted)
	}

	c3 := mustNew(t, []Rule{{Until: 60, Step: 1}}, 100, 1<<60, 1000)
	for _, v := range []int64{0, 40, 60} {
		if err := c3.Add(60, "h", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	res = c3.Clean(60)
	if len(res.Deleted) != 1 || res.Deleted[0].T != 0 || res.Deleted[0].Reason != ReasonAged {
		t.Fatalf("age == last Until must age out, got %v", res.Deleted)
	}
	if got := tsOf(c3.Versions("h")); !eqInt64(got, []int64{40, 60}) {
		t.Fatalf("aged survivors: %v", got)
	}
	if c3.thinnedExams != 2 {
		t.Fatalf("thinnedExams=%d want 2", c3.thinnedExams)
	}
}

func TestLatestIdentityChanges(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1000, Step: 100}}, 100, 1<<60, 1000)
	for _, v := range []int64{0, 10} {
		if err := c.Add(10, "f", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	if res := c.Clean(10); len(res.Deleted) != 0 {
		t.Fatalf("latest must survive: %v", res.Deleted)
	}
	if err := c.Add(20, "f", 20, 1); err != nil {
		t.Fatal(err)
	}
	res := c.Clean(20)
	want := []Deletion{{File: "f", T: 10, Reason: ReasonThinned}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("ex-latest must be thinned: %v", res.Deleted)
	}
}

func TestPinnedChangesSpacingBaseline(t *testing.T) {
	build := func(pinned bool) *Cleaner {
		c := mustNew(t, []Rule{{Until: 1000, Step: 20}}, 100, 1<<60, 1000)
		for _, v := range []int64{0, 10, 25, 29} {
			if err := c.Add(30, "f", v, 1); err != nil {
				t.Fatal(err)
			}
		}
		if pinned {
			if err := c.Pin("f", 10); err != nil {
				t.Fatal(err)
			}
		}
		return c
	}

	c := build(true)
	c.Clean(30)
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 10, 29}) {
		t.Fatalf("pinned baseline: %v", got)
	}
	if vs := c.Versions("f"); !vs[1].Pinned {
		t.Fatalf("t=10 should report pinned")
	}
	if err := c.Unpin("f", 10); err != nil {
		t.Fatal(err)
	}
	c.Clean(30)
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 29}) {
		t.Fatalf("after unpin + clean: %v", got)
	}

	c2 := build(false)
	c2.Clean(30)
	if got := tsOf(c2.Versions("f")); !eqInt64(got, []int64{0, 25, 29}) {
		t.Fatalf("unpinned baseline: %v", got)
	}

	if err := c.Pin("f", 25); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("pin missing: want ErrNoVersion, got %v", err)
	}
	if err := c.Unpin("f", 0); err != nil {
		t.Fatalf("unpin present: %v", err)
	}
	if err := c.Unpin("f", 0); err != nil {
		t.Fatalf("repeated unpin must be idempotent: %v", err)
	}
	if err := c.Pin("f", 0); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
	if err := c.Pin("f", 0); err != nil {
		t.Fatalf("repeated pin must be idempotent: %v", err)
	}
}

func TestPinnedSurvivesAgedAndLimits(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 60, Step: 1}}, 100, 1<<60, 1000)
	for _, v := range []int64{0, 100} {
		if err := c.Add(100, "f", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Pin("f", 0); err != nil {
		t.Fatal(err)
	}
	if res := c.Clean(100); len(res.Deleted) != 0 {
		t.Fatalf("pinned over-aged version must survive: %v", res.Deleted)
	}

	c2 := mustNew(t, []Rule{{Until: 1 << 30, Step: 1}}, 1, 1<<60, 1000)
	for _, v := range []int64{0, 10, 20} {
		if err := c2.Add(20, "f", v, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := c2.Pin("f", 0); err != nil {
		t.Fatal(err)
	}
	res := c2.Clean(20)
	want := []Deletion{{File: "f", T: 10, Reason: ReasonOverCount}}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("cap with pinned/latest: %v", res.Deleted)
	}
	if got := tsOf(c2.Versions("f")); !eqInt64(got, []int64{0, 20}) {
		t.Fatalf("pinned forces cap violation, got %v", got)
	}
	if n, b := c2.Totals("f"); n != 2 || b != 2 {
		t.Fatalf("totals = %d,%d", n, b)
	}
}

func TestLimitsReasonOrder(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1 << 30, Step: 1}}, 3, 10, 1000)
	for _, v := range []int64{0, 10, 20, 30} {
		if err := c.Add(30, "f", v, 4); err != nil {
			t.Fatal(err)
		}
	}
	res := c.Clean(30)
	want := []Deletion{
		{File: "f", T: 0, Reason: ReasonOverCount},
		{File: "f", T: 10, Reason: ReasonOverBytes},
	}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("limit deletions=%v", res.Deleted)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{20, 30}) {
		t.Fatalf("limit survivors=%v", got)
	}
}

func TestBytesOnlyReason(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1 << 30, Step: 1}}, 10, 10, 1000)
	for _, v := range []int64{0, 10, 20} {
		if err := c.Add(20, "f", v, 4); err != nil {
			t.Fatal(err)
		}
	}
	res := c.Clean(20)
	want := []Deletion{
		{File: "f", T: 0, Reason: ReasonOverBytes},
	}
	if fmt.Sprint(res.Deleted) != fmt.Sprint(want) {
		t.Fatalf("bytes-only deletions=%v", res.Deleted)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{10, 20}) {
		t.Fatalf("bytes-only survivors=%v", got)
	}
}

func TestTrashPurgeAndUndeleteBoundary(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1000, Step: 1000}}, 1, 1<<60, 10)
	for _, v := range []int64{0, 1} {
		if err := c.Add(1, "f", v, 7); err != nil {
			t.Fatal(err)
		}
	}
	res := c.Clean(1)
	if fmt.Sprint(res.Deleted) != fmt.Sprint([]Deletion{{File: "f", T: 0, Reason: ReasonOverCount}}) {
		t.Fatalf("setup deletions=%v", res.Deleted)
	}
	if tr := c.Trash("f"); len(tr) != 1 || tr[0] != (TrashItem{T: 0, Size: 7, DeletedAt: 1}) {
		t.Fatalf("trash=%v", tr)
	}
	if err := c.Add(1, "f", 0, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Add duplicate-in-trash: %v", err)
	}
	if err := c.Undelete(9, "f", 0); err != nil {
		t.Fatalf("undelete at ttl-1: %v", err)
	}
	if got := tsOf(c.Versions("f")); !eqInt64(got, []int64{0, 1}) {
		t.Fatalf("after undelete: %v", got)
	}

	res = c.Clean(10)
	if len(res.Deleted) != 1 || res.Deleted[0].T != 0 {
		t.Fatalf("re-delete: %v", res.Deleted)
	}
	if err := c.Undelete(20, "f", 0); !errors.Is(err, ErrNoVersion) {
		t.Fatalf("expired-but-not-purged undelete: %v", err)
	}
	if err := c.Add(20, "f", 0, 3); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expired trash still blocks duplicates: %v", err)
	}
	if tr := c.Trash("f"); len(tr) != 1 || tr[0].T != 0 || tr[0].DeletedAt != 10 {
		t.Fatalf("expired entry still visible in trash: %v", tr)
	}
	res = c.Clean(20)
	if fmt.Sprint(res.Purged) != fmt.Sprint([]PurgedVersion{{File: "f", T: 0}}) {
		t.Fatalf("purged=%v", res.Purged)
	}
	if len(res.Deleted) != 0 {
		t.Fatalf("unexpected deletions: %v", res.Deleted)
	}
	if err := c.Add(20, "f", 0, 3); err != nil {
		t.Fatalf("reuse after purge: %v", err)
	}

	// Purged list is ordered by (file, t).
	c3 := mustNew(t, []Rule{{Until: 1000, Step: 1000}}, 1, 1<<60, 1)
	for _, add := range []struct {
		file string
		t    int64
	}{{"b", 0}, {"b", 1}, {"a", 0}, {"a", 1}, {"a", 2}} {
		if err := c3.Add(2, add.file, add.t, 1); err != nil {
			t.Fatal(err)
		}
	}
	c3.Clean(2)
	res = c3.Clean(3)
	wantPurged := []PurgedVersion{{File: "a", T: 0}, {File: "a", T: 1}, {File: "b", T: 0}}
	if fmt.Sprint(res.Purged) != fmt.Sprint(wantPurged) {
		t.Fatalf("purged order=%v", res.Purged)
	}
}

func TestUndeleteClearsPinAndBypassesSizeGate(t *testing.T) {
	big := int64(800_000_000_000_000)
	c := mustNew(t, []Rule{{Until: 1 << 30, Step: 1}}, 1, 1<<60, 1000)
	_ = c.Add(0, "f", 0, big)
	_ = c.Pin("f", 0)
	_ = c.Add(1, "f", 1, 1)
	_ = c.Unpin("f", 0)
	c.Clean(1)
	_ = c.Add(2, "f", 2, big)
	if err := c.Undelete(2, "f", 0); err != nil {
		t.Fatalf("undelete must skip the 1e15 gate: %v", err)
	}
	if n, b := c.Totals("f"); n != 3 || b != 2*big+1 {
		t.Fatalf("totals after undelete = %d,%d", n, b)
	}
	if vs := c.Versions("f"); vs[0].T != 0 || vs[0].Pinned {
		t.Fatalf("restored version must not be pinned")
	}
}

func TestDuplicateAndValidation(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1000, Step: 1}}, 100, 1<<60, 10)
	if err := c.Add(5, "f", 5, 1); err != nil {
		t.Fatal(err)
	}
	if err := c.Add(5, "f", 5, 1); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("live duplicate: %v", err)
	}
	if err := c.Add(5, "f", 4, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size 0 must be ErrInvalid: %v", err)
	}
	if err := c.Add(5, "f", 6, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("t > now must be ErrInvalid: %v", err)
	}
	if err := c.Add(5, "", 5, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty file must be ErrInvalid: %v", err)
	}
	if err := c.Add(5, "g", -1, 1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative t must be ErrInvalid: %v", err)
	}
	if len(c.Versions("missing")) != 0 {
		t.Fatal("missing file Versions must be empty")
	}
	if n, b := c.Totals("missing"); n != 0 || b != 0 {
		t.Fatalf("missing file Totals = %d,%d", n, b)
	}
}

func TestIncrementalVersusOneShot(t *testing.T) {
	// Non-monotonic steps make results history-dependent: incremental cleans
	// at t=10/t=20 keep t=10 under the first tier's step 100; a single clean
	// at t=30 evaluates t=10 under the second tier's step 5 and drops it.
	rules := []Rule{{Until: 20, Step: 100}, {Until: 1000, Step: 5}}
	inc := mustNew(t, rules, 100, 1<<60, 1000)
	_ = inc.Add(10, "f", 0, 1)
	_ = inc.Add(10, "f", 10, 1)
	_ = inc.Add(15, "f", 15, 1)
	// Clean(18): t=10 is non-latest with age 8 (tier 1, step 100); spacing
	// 10 < 100 so it is thinned before it ever reaches the small-step tier.
	res := inc.Clean(18)
	if fmt.Sprint(res.Deleted) != fmt.Sprint([]Deletion{{File: "f", T: 10, Reason: ReasonThinned}}) {
		t.Fatalf("incremental Clean(18)=%v", res.Deleted)
	}
	_ = inc.Add(25, "f", 25, 1)
	inc.Clean(30)
	if got := tsOf(inc.Versions("f")); !eqInt64(got, []int64{0, 25}) {
		t.Fatalf("incremental got %v", got)
	}

	one := mustNew(t, rules, 100, 1<<60, 1000)
	_ = one.Add(10, "f", 0, 1)
	_ = one.Add(10, "f", 10, 1)
	_ = one.Add(15, "f", 15, 1)
	_ = one.Add(25, "f", 25, 1)
	// One Clean at now=30: t=10 has age exactly 20 (tier 2, step 5), spacing
	// 10 >= 5, so it survives; t=15 stays in tier 1 and is thinned instead.
	one.Clean(30)
	if got := tsOf(one.Versions("f")); !eqInt64(got, []int64{0, 10, 25}) {
		t.Fatalf("one-shot got %v", got)
	}
}

func TestConcurrent(t *testing.T) {
	c := mustNew(t, []Rule{{Until: 1 << 30, Step: 5}}, 50, 1<<60, 1000)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			file := fmt.Sprintf("f%d", g%3)
			for i := 0; i < 50; i++ {
				now := int64(g*50 + i)
				_ = c.Add(now, file, now, 1)
				c.Clean(now)
				_ = c.Versions(file)
				_, _ = c.Totals(file)
				_ = c.Trash(file)
			}
		}(g)
	}
	wg.Wait()
}
