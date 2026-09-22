package replica

import (
	"context"
	"fmt"
	"math/rand"
	"testing"

	"ontology/bus"
	"ontology/entry"
	"ontology/version"
)

// Requirement 8: N=8 replicas, each fed the same notification set
// through its own bus with random drops, duplicates and shuffling,
// must converge to the same visible state after one TTL round.
func TestEightReplicasConverge(t *testing.T) {
	const nReplicas = 8
	const nVersions = 20
	clock := newClock()
	be := newBackend()

	reps := make([]*Replica, nReplicas)
	buses := make([]*bus.Bus, nReplicas)
	for i := range reps {
		reps[i] = New(Config{Loader: be.load, Clock: clock.Now, TTL: ttl})
		buses[i] = bus.New(0)
		reps[i].Subscribe(buses[i])
	}

	// Everyone reads the initial version.
	be.set("k", "v1")
	for _, r := range reps {
		if res, err := r.Read(context.Background(), "k"); err != nil || res.State != Hit {
			t.Fatalf("initial read: %v %v", res, err)
		}
	}

	// Backend advances; every replica's bus gets every notification.
	for v := 2; v <= nVersions; v++ {
		be.set("k", fmt.Sprintf("v%d", v))
		for _, b := range buses {
			if err := b.Publish(bus.Notification{Key: "k", Version: version.Version(v)}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Independent chaos per replica: drop some, duplicate some,
	// shuffle the rest.
	for i, b := range buses {
		r := rand.New(rand.NewSource(int64(i*97 + 13)))
		for d := 0; d < i%4; d++ {
			b.DropOldest()
		}
		if err := b.Duplicate(bus.Notification{Key: "k", Version: version.Version(2 + i%(nVersions-1))}); err != nil {
			t.Fatal(err)
		}
		b.FlushShuffled(r)
	}

	// One TTL round: everything expires, every replica refetches.
	clock.Advance(ttl)
	var want dataState
	for i, r := range reps {
		res, err := r.Read(context.Background(), "k")
		if err != nil || res.State != Hit {
			t.Fatalf("replica %d read: %v %v", i, res, err)
		}
		got := observe(r, "k")
		if got.state != entry.Valid || got.ver != nVersions {
			t.Fatalf("replica %d = %+v, want valid v%d", i, got, nVersions)
		}
		if i == 0 {
			want = got
		} else if got != want {
			t.Fatalf("replica %d diverged: %+v vs %+v", i, got, want)
		}
	}
}

// Requirement 9: applying a notification inspects exactly one entry,
// independent of how many entries the replica holds.
func TestNotificationChecksAreConstant(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			f := newFixture(t, ttl, 0)
			for i := 0; i < n; i++ {
				key := fmt.Sprintf("key-%d", i)
				f.be.set(key, i)
				mustRead(t, f.rep, key)
			}
			if f.rep.checked != 0 {
				t.Fatalf("checked = %d before notifications", f.rep.checked)
			}
			const notes = 50
			for i := 0; i < notes; i++ {
				err := f.rep.ApplyNotification(bus.Notification{
					Key:     fmt.Sprintf("key-%d", i*7%n),
					Version: version.Version(100 + i),
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			if f.rep.checked != notes {
				t.Fatalf("checked = %d for %d notifications (N=%d)",
					f.rep.checked, notes, n)
			}
		})
	}
}

// Requirement 9 (unknown key): a notification for an unknown key is
// also exactly one probe.
func TestNotificationUnknownKeySingleProbe(t *testing.T) {
	f := newFixture(t, ttl, 0)
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("k%d", i)
		f.be.set(key, i)
		mustRead(t, f.rep, key)
	}
	if err := f.rep.ApplyNotification(bus.Notification{Key: "new", Version: 1}); err != nil {
		t.Fatal(err)
	}
	if f.rep.checked != 1 {
		t.Fatalf("checked = %d, want 1", f.rep.checked)
	}
}
