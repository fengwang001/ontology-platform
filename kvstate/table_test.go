package kvstate

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWallClock struct {
	unixNano atomic.Int64
}

func newFakeWallClock(at time.Time) *fakeWallClock {
	clock := &fakeWallClock{}
	clock.set(at)
	return clock
}

func (c *fakeWallClock) now() time.Time {
	return time.Unix(0, c.unixNano.Load()).UTC()
}

func (c *fakeWallClock) set(at time.Time) {
	c.unixNano.Store(at.UnixNano())
}

func TestExpirationBoundaryIsInclusive(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	clock := newFakeWallClock(base)
	table := NewTable(clock.now)
	ttl := 10 * time.Second

	if err := table.Put("lazy", "active", base, ttl); err != nil {
		t.Fatalf("put input: key=lazy ttl=%s event=%s, error=%v", ttl, base, err)
	}
	t.Logf("input=Put(lazy,%v,%s); result=stored; basis=wall-event=%s < ttl=%s", "active", base, clock.now().Sub(base), ttl)

	clock.set(base.Add(ttl))
	value, ok := table.Get("lazy")
	t.Logf("input=Get(lazy) wall=%s; result=(%v,%t); basis=wall-last=%s >= ttl=%s means expired", clock.now(), value, ok, ttl, ttl)
	if ok || value != nil {
		t.Fatalf("entry at exactly ttl must be lazily removed, got (%v, %t)", value, ok)
	}
	if table.Len() != 0 {
		t.Fatalf("lazy removal failed, len=%d", table.Len())
	}

	if err := table.Put("purged", "active", base, ttl); err != nil {
		t.Fatal(err)
	}
	clock.set(base.Add(ttl - time.Nanosecond))
	if removed := table.PurgeExpired(); removed != 0 {
		t.Fatalf("entry one nanosecond before ttl was removed: %d", removed)
	}
	clock.set(base.Add(ttl))
	removed := table.PurgeExpired()
	t.Logf("input=PurgeExpired wall=%s; result=removed=%d; basis=wall-last=%s >= ttl=%s", clock.now(), removed, ttl, ttl)
	if removed != 1 || table.Len() != 0 {
		t.Fatalf("active boundary purge removed=%d len=%d", removed, table.Len())
	}
}

func TestOutOfOrderEventsDoNotMoveLastActivityBackward(t *testing.T) {
	base := time.Unix(200, 0).UTC()
	clock := newFakeWallClock(base)
	table := NewTable(clock.now)
	ttl := time.Minute

	newTime := base.Add(20 * time.Second)
	lateTime := base.Add(10 * time.Second)
	if err := table.Put("key", "new", newTime, ttl); err != nil {
		t.Fatal(err)
	}
	if err := table.Put("key", "late", lateTime, ttl); err != nil {
		t.Fatal(err)
	}

	current := table.entries["key"]
	t.Logf("input=Put(new at %s), Put(late at %s); result=value=%v last=%s; basis=last=max(last,event)=%s while value follows the write", newTime, lateTime, current.value, current.lastActivity, newTime)
	if current.value != "late" || !current.lastActivity.Equal(newTime) {
		t.Fatalf("late event changed state to value=%v last=%s", current.value, current.lastActivity)
	}

	value, ok := table.Get("key")
	t.Logf("input=Get(key) wall=%s; result=(%v,%t); basis=wall-last=%s < ttl=%s", clock.now(), value, ok, clock.now().Sub(current.lastActivity), ttl)
	if !ok || value != "late" {
		t.Fatalf("unexpected read after late event: (%v, %t)", value, ok)
	}
}

func TestPurgeExpiredRemovesOnlyExpiredEntries(t *testing.T) {
	base := time.Unix(300, 0).UTC()
	clock := newFakeWallClock(base)
	table := NewTable(clock.now)

	if err := table.Put("expired", "old", base, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := table.Put("alive", "new", base, 20*time.Second); err != nil {
		t.Fatal(err)
	}
	clock.set(base.Add(10 * time.Second))

	removed := table.PurgeExpired()
	t.Logf("input=PurgeExpired wall=%s; result=removed=%d; basis=expired elapsed>=10s, alive elapsed<20s", clock.now(), removed)
	if removed != 1 {
		t.Fatalf("removed=%d", removed)
	}
	if _, ok := table.Get("expired"); ok {
		t.Fatal("expired key remains")
	}
	if value, ok := table.Get("alive"); !ok || value != "new" {
		t.Fatalf("alive key changed: (%v, %t)", value, ok)
	}
}

func TestInvalidInputsAreRejectedWithoutStateChange(t *testing.T) {
	base := time.Unix(400, 0).UTC()
	clock := newFakeWallClock(base)
	table := NewTable(clock.now)
	if err := table.Put("stable", "value", base, time.Second); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		key  string
		ttl  time.Duration
		want error
	}{
		{name: "empty key", key: "", ttl: time.Second, want: ErrEmptyKey},
		{name: "zero ttl", key: "invalid", ttl: 0, want: ErrInvalidTTL},
		{name: "negative ttl", key: "invalid", ttl: -time.Nanosecond, want: ErrInvalidTTL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := table.Put(tc.key, "rejected", base, tc.ttl)
			t.Logf("input=Put(key=%q,ttl=%s); result=%v; basis=%s must be rejected before locking in a new state", tc.key, tc.ttl, err, tc.want)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
		})
	}

	if table.Len() != 1 {
		t.Fatalf("state changed after rejected puts: len=%d", table.Len())
	}
	if value, ok := table.Get("stable"); !ok || value != "value" {
		t.Fatalf("stable state changed after rejected puts: (%v, %t)", value, ok)
	}
	if _, exists := table.entries[""]; exists {
		t.Fatal("empty key was inserted")
	}
	if err := table.Put("after-rejection", "ok", base, time.Second); err != nil {
		t.Fatalf("table unusable after rejected input: %v", err)
	}
}

func TestMissingKeyReturnsNoMatch(t *testing.T) {
	base := time.Unix(500, 0).UTC()
	table := NewTable(newFakeWallClock(base).now)

	value, ok := table.Get("missing")
	t.Logf("input=Get(missing); result=(%v,%t); basis=key is absent", value, ok)
	if ok || value != nil {
		t.Fatalf("missing key returned (%v, %t)", value, ok)
	}
	if removed := table.PurgeExpired(); removed != 0 {
		t.Fatalf("purge on empty table removed %d", removed)
	}
}

func TestSelfCheckAcceptsOnlyValidStoredState(t *testing.T) {
	base := time.Unix(550, 0).UTC()
	table := NewTable(newFakeWallClock(base).now)
	if err := table.Put("key", "value", base, time.Second); err != nil {
		t.Fatal(err)
	}

	if err := table.Check(); err != nil {
		t.Fatalf("check result=%v; basis=all stored keys are non-empty and ttl values are positive", err)
	}
	t.Log("input=Check; result=nil; basis=non-empty keys and positive ttl values")
}

func TestConcurrentReadsWritesAndPurgesHaveNoExpiredStaleRead(t *testing.T) {
	base := time.Unix(600, 0).UTC()
	ttl := time.Second
	clock := newFakeWallClock(base.Add(ttl))
	table := NewTable(clock.now)
	if err := table.Put("key", "old", base, ttl); err != nil {
		t.Fatal(err)
	}
	if err := table.Put("fixed-expired", "gone", base, ttl); err != nil {
		t.Fatal(err)
	}

	var readers, writers, purges atomic.Int64
	var checks atomic.Int64
	var stop atomic.Bool
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				value, ok := table.Get("key")
				readers.Add(1)
				if ok && value == "old" {
					t.Errorf("stale expired value observed at wall=%s", clock.now())
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		event := clock.now()
		for !stop.Load() {
			event = event.Add(time.Millisecond)
			if err := table.Put("key", "new", event, ttl); err != nil {
				t.Errorf("concurrent put failed: %v", err)
				return
			}
			writers.Add(1)
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			purges.Add(int64(table.PurgeExpired()))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for !stop.Load() {
			if err := table.Check(); err != nil {
				t.Errorf("concurrent check failed: %v", err)
				return
			}
			checks.Add(1)
		}
	}()

	time.Sleep(20 * time.Millisecond)
	stop.Store(true)
	wg.Wait()
	t.Logf("input=concurrent Get/Put/PurgeExpired/Check; result=reads=%d writes=%d purges=%d checks=%d; basis=locked reads see one atomic freshness decision", readers.Load(), writers.Load(), purges.Load(), checks.Load())
	if readers.Load() == 0 || writers.Load() == 0 || purges.Load() != 1 {
		t.Fatal("concurrent workload did not execute")
	}
}
