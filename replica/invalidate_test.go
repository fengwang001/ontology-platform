package replica

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"ontology/bus"
	"ontology/entry"
	"ontology/version"
)

const ttl = 10 * time.Second

// dataState is the observable data state compared across runs.
type dataState struct {
	state     entry.State
	ver       version.Version
	remaining time.Duration
}

func observe(r *Replica, key string) dataState {
	info := r.Inspect(key)
	return dataState{info.State, info.Version, info.Remaining}
}

// Requirement 1: shuffling the same notification set 100 times must
// always converge to the exact final state of in-order delivery.
func TestOutOfOrderDeliveryConverges(t *testing.T) {
	const versions = 10
	setup := func() (*Replica, *bus.Bus) {
		f := newFixture(t, ttl, 0)
		f.be.set("k", "v1")
		if res := mustRead(t, f.rep, "k"); res.State != Hit {
			t.Fatalf("setup read = %v", res.State)
		}
		b := bus.New(0)
		f.rep.Subscribe(b)
		return f.rep, b
	}
	publish := func(b *bus.Bus, order []int) {
		for _, v := range order {
			if err := b.Publish(bus.Notification{Key: "k", Version: version.Version(v)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	inOrder := make([]int, 0, versions-1)
	for v := 2; v <= versions; v++ {
		inOrder = append(inOrder, v)
	}

	refRep, refBus := setup()
	publish(refBus, inOrder)
	refBus.Flush()
	want := observe(refRep, "k")
	if want.state != entry.Stale || want.ver != versions {
		t.Fatalf("reference state = %+v", want)
	}

	for trial := 0; trial < 100; trial++ {
		rep, b := setup()
		shuffled := append([]int(nil), inOrder...)
		rand.New(rand.NewSource(int64(trial))).Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		publish(b, shuffled)
		b.FlushShuffled(rand.New(rand.NewSource(int64(trial + 1000))))
		if got := observe(rep, "k"); got != want {
			t.Fatalf("trial %d: got %+v, want %+v", trial, got, want)
		}
	}
}

// Requirement 2: duplicate deliveries must not grow the invalidation
// counter beyond the first effective application.
func TestDuplicateNotificationIdempotent(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	mustRead(t, f.rep, "k")
	b := bus.New(0)
	f.rep.Subscribe(b)
	if err := b.Duplicate(bus.Notification{Key: "k", Version: 2}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 7; i++ {
		if err := b.Publish(bus.Notification{Key: "k", Version: 2}); err != nil {
			t.Fatal(err)
		}
	}
	b.Flush()
	info := f.rep.Inspect("k")
	if info.Invalidations != 1 {
		t.Fatalf("invalidations = %d, want 1", info.Invalidations)
	}
	if info.State != entry.Stale || info.Version != 2 {
		t.Fatalf("info = %+v", info)
	}
}

// Requirement 3: liveness is [start, expiry); at now == expiry the
// entry is stale and the next read refetches, even with no
// notification ever delivered (notification loss is tolerated).
func TestExpiryBoundaryTriggersRefetch(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	mustRead(t, f.rep, "k")
	if got := f.be.loadCalls(); got != 1 {
		t.Fatalf("load calls = %d", got)
	}

	f.clock.Advance(ttl - time.Nanosecond) // still alive
	if res := mustRead(t, f.rep, "k"); res.State != Hit {
		t.Fatalf("just before expiry: %v", res.State)
	}
	if got := f.be.loadCalls(); got != 1 {
		t.Fatalf("refetch before expiry, calls = %d", got)
	}

	f.clock.Advance(time.Nanosecond) // now == expiresAt: expired
	f.be.set("k", "v2")
	res := mustRead(t, f.rep, "k")
	if res.State != Hit || res.Value != "v2" || res.Version != 2 {
		t.Fatalf("after expiry: %+v", res)
	}
	if got := f.be.loadCalls(); got != 2 {
		t.Fatalf("load calls = %d, want 2", got)
	}
}

// Requirement 3 (negative path): a negative entry expires too.
func TestNegativeEntryExpires(t *testing.T) {
	f := newFixture(t, ttl, 0)
	res := mustRead(t, f.rep, "ghost")
	if res.State != Absent || res.Found {
		t.Fatalf("res = %+v", res)
	}
	f.clock.Advance(ttl)
	f.be.set("ghost", "now-exists")
	res = mustRead(t, f.rep, "ghost")
	if res.State != Hit || res.Value != "now-exists" {
		t.Fatalf("res = %+v", res)
	}
}

// Old notifications must not move state even after lazy expiry.
func TestOldNotificationAfterExpiryDropped(t *testing.T) {
	f := newFixture(t, ttl, 0)
	f.be.set("k", "v1")
	mustRead(t, f.rep, "k")
	f.clock.Advance(ttl) // expired -> stale, known version stays 1
	f.be.set("k", "v2")
	mustRead(t, f.rep, "k") // refetch -> valid v2
	if err := f.rep.ApplyNotification(bus.Notification{Key: "k", Version: 1}); err != nil {
		t.Fatal(err)
	}
	info := f.rep.Inspect("k")
	if info.State != entry.Valid || info.Version != 2 {
		t.Fatalf("old notification moved state: %+v", info)
	}
	if _, err := f.rep.Read(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	if got := f.be.loadCalls(); got != 2 {
		t.Fatalf("load calls = %d, want 2 (no spurious refetch)", got)
	}
}
