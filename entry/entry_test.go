package entry

import (
	"testing"
	"time"

	"ontology/version"
)

var t0 = time.Unix(1_000_000, 0)

func validEntry(t *testing.T, v version.Version) *Entry {
	t.Helper()
	e := New()
	e.BeginFetch()
	ok := e.CompleteFetch(FetchResult{Value: "x", Version: v, Found: true}, t0, 10*time.Second)
	if !ok {
		t.Fatal("setup: fetch result rejected")
	}
	return e
}

func TestZeroEntryIsHole(t *testing.T) {
	e := New()
	if e.State() != Hole || !e.Version().IsZero() {
		t.Fatalf("zero entry = %v %v", e.State(), e.Version())
	}
}

func TestInvalidateOlderDropped(t *testing.T) {
	e := validEntry(t, 5)
	if e.ApplyInvalidate(4) {
		t.Fatal("older notification must be dropped")
	}
	if e.State() != Valid || e.Version() != 5 {
		t.Fatalf("state changed: %v %v", e.State(), e.Version())
	}
}

func TestInvalidateEqualAndNewer(t *testing.T) {
	e := validEntry(t, 5)
	if !e.ApplyInvalidate(5) || e.State() != Stale || e.Version() != 5 {
		t.Fatal("equal version must invalidate")
	}
	if !e.ApplyInvalidate(7) || e.Version() != 7 {
		t.Fatal("newer version must advance known version")
	}
}

func TestInvalidateDuplicateIdempotent(t *testing.T) {
	e := validEntry(t, 5)
	if !e.ApplyInvalidate(6) {
		t.Fatal("first apply must take effect")
	}
	for i := 0; i < 5; i++ {
		if e.ApplyInvalidate(6) {
			t.Fatal("duplicate apply must be a no-op")
		}
	}
	_, _, _, inv := e.Counters()
	if inv != 1 {
		t.Fatalf("invalidations = %d, want 1", inv)
	}
}

func TestExpiryLeftClosedRightOpen(t *testing.T) {
	e := validEntry(t, 1)
	if e.ExpireIfDue(t0.Add(10*time.Second - time.Nanosecond)) {
		t.Fatal("must be alive just before expiry")
	}
	if !e.ExpireIfDue(t0.Add(10 * time.Second)) {
		t.Fatal("now == expiresAt must be expired")
	}
	if e.State() != Stale {
		t.Fatalf("state = %v, want stale", e.State())
	}
}

func TestCompleteFetchDiscardsStaleResult(t *testing.T) {
	e := New()
	e.BeginFetch()
	e.ApplyInvalidate(5) // invalidation arrives mid-flight
	if e.State() != Fetching {
		t.Fatalf("state = %v, want fetching", e.State())
	}
	ok := e.CompleteFetch(FetchResult{Value: "old", Version: 3, Found: true}, t0, time.Minute)
	if ok || e.State() != Stale || e.Value() != nil {
		t.Fatalf("stale result stored: ok=%v state=%v", ok, e.State())
	}
}

func TestFailFetchRefetchable(t *testing.T) {
	e := New()
	e.BeginFetch()
	e.FailFetch()
	if e.State() != Hole {
		t.Fatalf("state = %v, want hole", e.State())
	}
	e.BeginFetch()
	e.ApplyInvalidate(2)
	e.FailFetch()
	if e.State() != Stale {
		t.Fatalf("state = %v, want stale", e.State())
	}
}

func TestNegativeEntry(t *testing.T) {
	e := New()
	e.BeginFetch()
	e.CompleteFetch(FetchResult{Version: 3, Found: false}, t0, time.Minute)
	if !e.Negative() || e.State() != Valid || e.Version() != 3 {
		t.Fatal("negative entry broken")
	}
	if !e.ApplyInvalidate(4) || e.Negative() {
		t.Fatal("negative entry must invalidate like any entry")
	}
}

func TestCounters(t *testing.T) {
	e := validEntry(t, 1)
	e.NoteHit()
	e.NoteHit()
	e.NoteMiss()
	e.NoteFetch()
	h, m, f, i := e.Counters()
	if h != 2 || m != 1 || f != 1 || i != 0 {
		t.Fatalf("counters = %d,%d,%d,%d", h, m, f, i)
	}
}
