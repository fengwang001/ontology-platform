package entry

import (
	"testing"
	"time"
)

func TestInitialHole(t *testing.T) {
	e := New("k")
	if e.State() != Hole {
		t.Fatalf("new entry should be Hole, got %v", e.State())
	}
	if e.Alive(0) {
		t.Fatal("hole must not be alive")
	}
}

func TestFillAndAliveBoundary(t *testing.T) {
	e := New("k")
	e.Fill("v", true, 3, 100)
	if e.State() != Valid || e.Version() != 3 || !e.Found() || e.Value() != "v" {
		t.Fatal("fill did not set fields")
	}
	if !e.Alive(99) {
		t.Fatal("now=99 < expiry=100 must be alive")
	}
	if e.Alive(100) {
		t.Fatal("now == expiry must be expired (left-closed right-open)")
	}
	if e.EffectiveState(100) != Stale {
		t.Fatal("expired valid entry must view as Stale")
	}
	if e.TTLRemaining(40) != 60*time.Duration(1) {
		t.Fatalf("TTLRemaining=%v want 60", e.TTLRemaining(40))
	}
	if e.TTLRemaining(100) != 0 {
		t.Fatal("expired entry must report 0 TTL")
	}
}

func TestInvalidateDropsOldAndDuplicate(t *testing.T) {
	e := New("k")
	e.Fill("v", true, 5, 1000)
	if e.ApplyInvalidate(4) {
		t.Fatal("older notification must be dropped")
	}
	if e.ApplyInvalidate(5) {
		t.Fatal("equal-version notification must be dropped")
	}
	if e.State() != Valid {
		t.Fatal("dropped notifications must not change state")
	}
	if !e.ApplyInvalidate(7) {
		t.Fatal("newer notification must apply")
	}
	if e.State() != Stale || e.InvalidatedVersion() != 7 {
		t.Fatal("invalidate must demote to Stale and record version")
	}
	if e.ApplyInvalidate(7) {
		t.Fatal("duplicate notification must be idempotent")
	}
	if e.ApplyInvalidate(6) {
		t.Fatal("older-than-applied notification must be dropped")
	}
	if got := e.Stats().Invalidations; got != 1 {
		t.Fatalf("invalidations=%d want 1", got)
	}
}

func TestFloorTracksMaxOfDataAndInvalidation(t *testing.T) {
	e := New("k")
	e.Fill("v", true, 5, 1000)
	e.ApplyInvalidate(8)
	if e.Floor() != 8 {
		t.Fatalf("floor=%v want 8", e.Floor())
	}
	e.Fill("v2", true, 9, 2000)
	if e.Floor() != 9 {
		t.Fatalf("floor=%v want 9", e.Floor())
	}
	if e.ApplyInvalidate(8) {
		t.Fatal("notification below floor must be dropped after refill")
	}
}

func TestInvalidateHoleRecordsVersion(t *testing.T) {
	e := New("k")
	if !e.ApplyInvalidate(4) {
		t.Fatal("notification on hole with version > 0 must apply")
	}
	if e.State() != Hole {
		t.Fatal("hole stays hole, only version floor rises")
	}
	if e.Floor() != 4 {
		t.Fatalf("floor=%v want 4", e.Floor())
	}
}

func TestNegativeCacheEntry(t *testing.T) {
	e := New("k")
	e.Fill("", false, 2, 100)
	if e.Found() {
		t.Fatal("negative cache entry must report Found=false")
	}
	if !e.Alive(50) {
		t.Fatal("negative cache entry must be alive within TTL")
	}
	if !e.ApplyInvalidate(3) || e.State() != Stale {
		t.Fatal("negative cache must honor invalidation like normal entries")
	}
}
