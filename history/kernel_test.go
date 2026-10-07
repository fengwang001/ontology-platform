package history_test

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"ontology/history"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return t0.Add(time.Duration(sec) * time.Second) }

func newKernel(t *testing.T, capacity int, ttl time.Duration) *history.Kernel {
	t.Helper()
	k, err := history.NewKernel(history.Config{CacheCapacity: capacity, CacheTTL: ttl, Now: t0})
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	return k
}

func nav(t *testing.T, k *history.Kernel, url string, sameDoc bool, sec int) history.Entry {
	t.Helper()
	e, err := k.Navigate(url, "state-"+url, sameDoc, at(sec))
	if err != nil {
		t.Fatalf("Navigate(%q): %v", url, err)
	}
	return e
}

func traverse(t *testing.T, k *history.Kernel, delta, sec int) history.Outcome {
	t.Helper()
	k.Traverse(delta, at(sec))
	outcomes := k.Drain()
	if len(outcomes) != 1 {
		t.Fatalf("Drain returned %d outcomes, want 1", len(outcomes))
	}
	return outcomes[0]
}

func mustTraverse(t *testing.T, k *history.Kernel, delta, sec int) {
	t.Helper()
	if o := traverse(t, k, delta, sec); o.Err != nil {
		t.Fatalf("Traverse(%d): %v", delta, o.Err)
	}
}

func docStatus(t *testing.T, k *history.Kernel, docID uint64) history.DocStatus {
	t.Helper()
	info, ok := k.Snapshot().Docs[docID]
	if !ok {
		t.Fatalf("doc %d not found", docID)
	}
	return info.Status
}

// delta == 0 reloads the current entry instead of being a no-op.
func TestZeroDeltaIsReload(t *testing.T) {
	k := newKernel(t, 8, time.Minute)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	mustTraverse(t, k, -1, 2) // restore doc 1
	before, _ := k.CurrentEntry()
	if before.DocID != 1 {
		t.Fatalf("current doc = %d, want 1", before.DocID)
	}
	mustTraverse(t, k, 0, 3) // reload
	after, _ := k.CurrentEntry()
	if after.DocID == before.DocID {
		t.Fatalf("reload kept doc %d", after.DocID)
	}
	if info, ok := k.Snapshot().Docs[before.DocID]; ok && info.Status != history.DocUnloaded {
		t.Fatalf("old doc status = %s, want unloaded or forgotten", info.Status)
	}
	if k.IsCached(before.DocID) {
		t.Fatal("reloaded document must not enter the cache")
	}
	if err := k.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// Deltas beyond either end are rejected as invalid arguments.
func TestTraverseOutOfBounds(t *testing.T) {
	k := newKernel(t, 8, time.Minute)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	for _, delta := range []int{1, 5, -2, -100} {
		if o := traverse(t, k, delta, 2); !errors.Is(o.Err, history.ErrInvalidArgument) {
			t.Fatalf("Traverse(%d) err = %v, want ErrInvalidArgument", delta, o.Err)
		}
	}
	snap := k.Snapshot()
	if snap.Pos != 1 || len(snap.Entries) != 2 {
		t.Fatalf("rejected traversals changed state: pos=%d entries=%d", snap.Pos, len(snap.Entries))
	}
}

// Same-document traversal never caches or unloads the current document.
func TestSameDocumentTraversalNotCached(t *testing.T) {
	k := newKernel(t, 8, time.Minute)
	nav(t, k, "a", false, 0)
	nav(t, k, "a#frag", true, 1)
	mustTraverse(t, k, -1, 2)
	snap := k.Snapshot()
	if len(snap.Cached) != 0 {
		t.Fatalf("same-document traversal cached %v", snap.Cached)
	}
	if got := docStatus(t, k, 1); got != history.DocActive {
		t.Fatalf("doc 1 status = %s, want active", got)
	}
	mustTraverse(t, k, 1, 3)
	if len(k.Snapshot().Cached) != 0 {
		t.Fatal("same-document forward traversal cached a document")
	}
}

// Each of the four eligibility conditions, when missing, forces an unload.
func TestEligibilityFourConditions(t *testing.T) {
	flags := []history.Flag{
		history.FlagNetworkPending,
		history.FlagUnloadBlocker,
		history.FlagExclusiveResource,
		history.FlagUncacheable,
	}
	for _, flag := range flags {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			k := newKernel(t, 8, time.Minute)
			nav(t, k, "a", false, 0)
			if err := k.SetEligibility(1, flag, true, at(1)); err != nil {
				t.Fatalf("SetEligibility: %v", err)
			}
			nav(t, k, "b", false, 2)
			if k.IsCached(1) {
				t.Fatalf("doc with %s entered the cache", flag)
			}
			if got := docStatus(t, k, 1); got != history.DocUnloaded {
				t.Fatalf("doc 1 status = %s, want unloaded", got)
			}
		})
	}
	t.Run("all-clear-is-cached", func(t *testing.T) {
		k := newKernel(t, 8, time.Minute)
		nav(t, k, "a", false, 0)
		nav(t, k, "b", false, 1)
		if !k.IsCached(1) {
			t.Fatal("eligible document was not cached")
		}
	})
}

// A condition failing while the document sits in the cache evicts it at once.
func TestCachedConditionInvalidatedEvictsImmediately(t *testing.T) {
	flags := []history.Flag{
		history.FlagNetworkPending,
		history.FlagUnloadBlocker,
		history.FlagExclusiveResource,
		history.FlagUncacheable,
	}
	for _, flag := range flags {
		t.Run(fmt.Sprint(flag), func(t *testing.T) {
			k := newKernel(t, 8, time.Minute)
			nav(t, k, "a", false, 0)
			nav(t, k, "b", false, 1)
			if !k.IsCached(1) {
				t.Fatal("doc 1 should be cached")
			}
			if err := k.SetEligibility(1, flag, true, at(2)); err != nil {
				t.Fatalf("SetEligibility: %v", err)
			}
			if k.IsCached(1) {
				t.Fatalf("doc 1 still cached after %s was set", flag)
			}
			if got := docStatus(t, k, 1); got != history.DocUnloaded {
				t.Fatalf("doc 1 status = %s, want unloaded", got)
			}
		})
	}
}

// A cached document whose age reaches exactly the TTL is evicted.
func TestTTLExactBoundary(t *testing.T) {
	k := newKernel(t, 8, 10*time.Second)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1) // doc 1 cached at t0+1s
	if err := k.AdvanceClock(9 * time.Second); err != nil {
		t.Fatal(err)
	}
	if !k.IsCached(1) {
		t.Fatal("doc 1 evicted before TTL")
	}
	if err := k.AdvanceClock(1 * time.Second); err != nil {
		t.Fatal(err)
	}
	if k.IsCached(1) {
		t.Fatal("doc 1 still cached at exactly TTL age")
	}
}

// Cache fills exactly to capacity, then the oldest entry is evicted.
func TestCapacityExactAndOverflow(t *testing.T) {
	k := newKernel(t, 2, time.Hour)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	nav(t, k, "c", false, 2)
	if got := k.Snapshot().Cached; len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("cached = %v, want [1 2]", got)
	}
	nav(t, k, "d", false, 3) // doc 3 enters, doc 1 is oldest
	if k.IsCached(1) {
		t.Fatal("oldest doc 1 not evicted on overflow")
	}
	if !k.IsCached(2) || !k.IsCached(3) {
		t.Fatal("docs 2 and 3 should remain cached")
	}
	if err := k.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// Truncation evicts a cached document that lost its last reference but
// keeps one that is still referenced by a remaining entry.
func TestTruncationReferencedVsUnreferenced(t *testing.T) {
	t.Run("unreferenced-is-evicted", func(t *testing.T) {
		k := newKernel(t, 8, time.Hour)
		nav(t, k, "a", false, 0) // doc 1
		nav(t, k, "b", false, 1) // doc 2, doc 1 cached
		nav(t, k, "c", false, 2) // doc 3, doc 2 cached
		mustTraverse(t, k, -1, 3)
		nav(t, k, "d", false, 4) // truncates "c"; doc 3 cached but unreferenced
		if k.IsCached(3) {
			t.Fatal("unreferenced cached doc 3 not evicted on truncation")
		}
		if got := docStatus(t, k, 3); got != history.DocUnloaded {
			t.Fatalf("doc 3 status = %s, want unloaded", got)
		}
		if !k.IsCached(1) {
			t.Fatal("doc 1 is still referenced and must stay cached")
		}
	})
	t.Run("referenced-is-kept", func(t *testing.T) {
		k := newKernel(t, 8, time.Hour)
		nav(t, k, "a", false, 0)     // doc 1
		nav(t, k, "a#frag", true, 1) // doc 1
		nav(t, k, "b", false, 2)     // doc 2, doc 1 cached
		mustTraverse(t, k, -2, 3)    // restore doc 1 at "a"
		nav(t, k, "c", false, 4)     // truncates "a#frag"; doc 1 still referenced by "a"
		if !k.IsCached(1) {
			t.Fatal("doc 1 is still referenced by a remaining entry and must stay cached")
		}
	})
}

// A restore must observe the target entry's current state object,
// including replacements made while the document was cached.
func TestReplaceThenRestoreSeesNewState(t *testing.T) {
	k := newKernel(t, 8, time.Hour)
	nav(t, k, "a", false, 0)
	nav(t, k, "a#frag", true, 1)
	nav(t, k, "b", false, 2) // doc 1 cached
	mustTraverse(t, k, -1, 3)
	if err := k.Replace("a#new", "replaced-state", at(4)); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	nav(t, k, "c", false, 5) // doc 1 cached again
	mustTraverse(t, k, -1, 6)
	cur, _ := k.CurrentEntry()
	if cur.State != "replaced-state" || cur.URL != "a#new" {
		t.Fatalf("restored entry = (%q, %v), want (a#new, replaced-state)", cur.URL, cur.State)
	}
}

// Reload assigns a fresh document ID and updates every entry that
// carried the old one.
func TestReloadUpdatesAllEntriesOfDocument(t *testing.T) {
	k := newKernel(t, 8, time.Hour)
	nav(t, k, "a", false, 0)
	nav(t, k, "a#frag", true, 1) // shares doc 1
	nav(t, k, "b", false, 2)
	mustTraverse(t, k, -2, 3) // restore doc 1
	mustTraverse(t, k, 0, 4)  // reload
	snap := k.Snapshot()
	if snap.Entries[0].DocID != snap.Entries[1].DocID {
		t.Fatalf("entries of reloaded document diverged: %d vs %d",
			snap.Entries[0].DocID, snap.Entries[1].DocID)
	}
	if snap.Entries[0].DocID == 1 {
		t.Fatal("entries still carry the old document ID after reload")
	}
	if _, ok := snap.Docs[1]; ok {
		t.Fatal("old document ID still known after reload")
	}
}

// Traversing to an unloaded document reloads it with a fresh ID.
func TestTraverseToUnloadedReloads(t *testing.T) {
	k := newKernel(t, 8, time.Hour)
	nav(t, k, "a", false, 0)
	if err := k.SetEligibility(1, history.FlagUncacheable, true, at(1)); err != nil {
		t.Fatal(err)
	}
	nav(t, k, "b", false, 2) // doc 1 not cacheable -> unloaded
	mustTraverse(t, k, -1, 3)
	cur, _ := k.CurrentEntry()
	if cur.DocID == 1 {
		t.Fatal("unloaded document was not reloaded with a fresh ID")
	}
	if got := docStatus(t, k, cur.DocID); got != history.DocActive {
		t.Fatalf("reloaded doc status = %s, want active", got)
	}
}

// Of several traversals arriving before a drain point, only the last
// runs; the rest are superseded and change nothing.
func TestCoalescedTraversals(t *testing.T) {
	k := newKernel(t, 8, time.Hour)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	nav(t, k, "c", false, 2)
	nav(t, k, "d", false, 3)
	k.Traverse(-1, at(4))
	k.Traverse(-3, at(4))
	k.Traverse(-2, at(4))
	outcomes := k.Drain()
	if len(outcomes) != 3 {
		t.Fatalf("got %d outcomes, want 3", len(outcomes))
	}
	for _, o := range outcomes[:2] {
		if !errors.Is(o.Err, history.ErrSuperseded) {
			t.Fatalf("outcome token=%d err = %v, want ErrSuperseded", o.Token, o.Err)
		}
	}
	last := outcomes[2]
	if last.Err != nil || last.Pos != 1 {
		t.Fatalf("last outcome = %+v, want pos=1", last)
	}
	snap := k.Snapshot()
	if snap.Pos != 1 {
		t.Fatalf("pos = %d, want 1 (only the last traversal applied)", snap.Pos)
	}
	// Only one leave/restore cycle happened: doc 4 cached, doc 2 restored.
	if !k.IsCached(4) {
		t.Fatal("doc 4 should be cached exactly once by the applied traversal")
	}
	if got := docStatus(t, k, 2); got != history.DocActive {
		t.Fatalf("doc 2 status = %s, want active", got)
	}
	if err := k.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// Rejection precedence: invalid argument before clock regression before
// document not found before invalid state.
func TestErrorOrdering(t *testing.T) {
	k := newKernel(t, 8, time.Hour)
	nav(t, k, "a", false, 10)
	if err := k.SetEligibility(1, history.FlagUncacheable, true, at(11)); err != nil {
		t.Fatal(err)
	}
	nav(t, k, "b", false, 12) // doc 1 unloaded

	// invalid argument beats clock regression, not-found and invalid state
	err := k.SetEligibility(999, history.Flag(42), true, at(0))
	if !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
	// clock regression beats not-found and invalid state
	err = k.SetEligibility(999, history.FlagUncacheable, true, at(0))
	if !errors.Is(err, history.ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	// not-found beats invalid state
	err = k.SetEligibility(999, history.FlagUncacheable, true, at(13))
	if !errors.Is(err, history.ErrDocumentNotFound) {
		t.Fatalf("err = %v, want ErrDocumentNotFound", err)
	}
	// cache-period operation on an unloaded document
	err = k.SetEligibility(1, history.FlagUncacheable, false, at(14))
	if !errors.Is(err, history.ErrInvalidState) {
		t.Fatalf("err = %v, want ErrInvalidState", err)
	}

	if _, err := k.Navigate("", nil, false, at(15)); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("empty url err = %v, want ErrInvalidArgument", err)
	}
	if err := k.AdvanceClock(-time.Second); !errors.Is(err, history.ErrClockRegression) {
		t.Fatalf("negative advance err = %v, want ErrClockRegression", err)
	}
	if _, err := history.NewKernel(history.Config{CacheCapacity: -1, Now: t0}); !errors.Is(err, history.ErrInvalidArgument) {
		t.Fatalf("negative capacity err = %v, want ErrInvalidArgument", err)
	}
}

// Rejected operations leave entries, position, cache and clock untouched.
func TestRejectedOpsNoSideEffects(t *testing.T) {
	k := newKernel(t, 2, time.Hour)
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	before := k.Snapshot()

	k.Navigate("", nil, false, at(2))                           // invalid argument
	k.Replace("", nil, at(2))                                   // invalid argument
	k.SetEligibility(1, history.Flag(42), true, at(2))          // invalid argument
	k.SetEligibility(1, history.FlagUncacheable, true, at(0))   // clock regression
	k.SetEligibility(999, history.FlagUncacheable, true, at(2)) // not found
	k.AdvanceClock(-time.Second)                                // clock regression
	traverse(t, k, 10, 2)                                       // out of bounds

	after := k.Snapshot()
	if !after.Now.Equal(before.Now) {
		t.Fatalf("clock moved: %s -> %s", before.Now, after.Now)
	}
	if after.Pos != before.Pos || len(after.Entries) != len(before.Entries) {
		t.Fatal("entries or position changed")
	}
	if fmt.Sprint(after.Cached) != fmt.Sprint(before.Cached) {
		t.Fatalf("cache changed: %v -> %v", before.Cached, after.Cached)
	}
}

// Concurrent callers observe some serial order; invariants always hold.
func TestConcurrentOps(t *testing.T) {
	k := newKernel(t, 4, 5*time.Second)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				sec := i
				switch i % 6 {
				case 0:
					k.Navigate(fmt.Sprintf("g%d/%d", g, i), i, i%3 == 0, at(sec))
				case 1:
					k.Replace(fmt.Sprintf("r%d", i), i, at(sec))
				case 2:
					k.Traverse(i%5-2, at(sec))
				case 3:
					k.Drain()
				case 4:
					k.SetEligibility(uint64(i%8+1), history.Flag(i%4), i%2 == 0, at(sec))
				case 5:
					k.AdvanceClock(time.Duration(i%3) * time.Second)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := k.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// The logger receives inputs, outputs and decision rationale.
func TestDecisionLogging(t *testing.T) {
	var buf strings.Builder
	k, err := history.NewKernel(
		history.Config{CacheCapacity: 2, CacheTTL: time.Hour, Now: t0},
		history.WithLogger(func(format string, args ...any) {
			fmt.Fprintf(&buf, format+"\n", args...)
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	nav(t, k, "a", false, 0)
	nav(t, k, "b", false, 1)
	mustTraverse(t, k, -1, 2)
	log := buf.String()
	for _, want := range []string{"navigate", "traverse", "cache"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}
