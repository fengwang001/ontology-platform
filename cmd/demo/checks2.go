package main

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"

	"ontology/bus"
	"ontology/entry"
	"ontology/replica"
	"ontology/single"
	"ontology/version"
)

func checkMidFlight() bool {
	f := newFixture(0)
	f.be.set("k", "old")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.be.hook = func(string) { once.Do(func() { close(started); <-release }) }
	resCh := make(chan replica.ReadResult, 1)
	go func() { resCh <- read(f.rep, "k") }()
	<-started
	f.be.set("k", "new") // backend moves to v2 mid-flight
	_ = f.rep.ApplyNotification(bus.Notification{Key: "k", Version: 2})
	f.be.mu.Lock()
	f.be.vals["k"], f.be.vers["k"] = "old", 1 // in-flight fetch returns old snapshot
	f.be.mu.Unlock()
	close(release)
	res := <-resCh
	if res.State == replica.Hit || f.rep.Inspect("k").State == entry.Valid {
		return false // stale value must never be cached or served
	}
	f.be.set("k", "new")
	return read(f.rep, "k").Value == "new"
}

func checkHoleAbsent() bool {
	f := newFixture(0)
	if f.rep.Inspect("ghost").State != entry.Hole {
		return false
	}
	res := read(f.rep, "ghost")
	if res.State != replica.Absent || res.Found {
		return false
	}
	info := f.rep.Inspect("ghost")
	return info.State == entry.Valid && info.Remaining > 0 && !info.Version.IsZero()
}

func checkConverge() bool {
	const nReplicas, nVersions = 8, 20
	c := newClock()
	be := newBackend()
	reps := make([]*replica.Replica, nReplicas)
	buses := make([]*bus.Bus, nReplicas)
	for i := range reps {
		reps[i] = replica.New(replica.Config{Loader: be.load, Clock: c.Now, TTL: ttl})
		buses[i] = bus.New(0)
		reps[i].Subscribe(buses[i])
	}
	be.set("k", "v1")
	for _, r := range reps {
		read(r, "k")
	}
	for v := 2; v <= nVersions; v++ {
		be.set("k", fmt.Sprintf("v%d", v))
		for _, b := range buses {
			_ = b.Publish(bus.Notification{Key: "k", Version: version.Version(v)})
		}
	}
	for i, b := range buses {
		for d := 0; d < i%4; d++ {
			b.DropOldest()
		}
		_ = b.Duplicate(bus.Notification{Key: "k", Version: version.Version(2 + i%(nVersions-1))})
		b.FlushShuffled(rand.New(rand.NewSource(int64(i + 1))))
	}
	c.Advance(ttl)
	for _, r := range reps {
		res := read(r, "k")
		info := r.Inspect("k")
		if res.State != replica.Hit || info.Version != nVersions {
			return false
		}
	}
	return true
}

func checkProbeCount() bool {
	probes := func(n int) uint64 {
		f := newFixture(0)
		for i := 0; i < n; i++ {
			key := fmt.Sprintf("key-%d", i)
			f.be.set(key, i)
			read(f.rep, key)
		}
		base := f.rep.Checked()
		_ = f.rep.ApplyNotification(bus.Notification{Key: "key-1", Version: 99999})
		return f.rep.Checked() - base
	}
	return probes(100) == 1 && probes(10000) == 1
}

func checkLimits() bool {
	f := newFixture(2)
	f.be.set("a", 1)
	f.be.set("b", 2)
	read(f.rep, "a")
	read(f.rep, "b")
	f.be.set("c", 3)
	_, errEntries := f.rep.Read(context.Background(), "c")
	if !errors.Is(errEntries, replica.ErrEntriesFull) || f.rep.Inspect("c").State != entry.Hole {
		return false
	}
	b := bus.New(1)
	_ = b.Publish(bus.Notification{Key: "x", Version: 1})
	errQueue := b.Publish(bus.Notification{Key: "y", Version: 2})
	errInflight := single.ErrInflightFull
	return errors.Is(errQueue, bus.ErrQueueFull) &&
		!errors.Is(errEntries, errQueue) && !errors.Is(errEntries, errInflight) &&
		!errors.Is(errQueue, errEntries) && !errors.Is(errQueue, errInflight)
}

func checkInspect() bool {
	f := newFixture(0)
	f.be.set("k", "v1")
	read(f.rep, "k")
	first := f.rep.Inspect("k")
	second := f.rep.Inspect("k")
	zero := f.rep.Inspect("unknown")
	return first == second && f.be.loadCalls() == 1 &&
		zero == (replica.KeyInfo{State: entry.Hole})
}
