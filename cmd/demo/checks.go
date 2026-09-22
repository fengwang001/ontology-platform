package main

import (
	"context"
	"errors"
	"math/rand"
	"runtime"
	"sync"
	"time"

	"ontology/bus"
	"ontology/entry"
	"ontology/replica"
	"ontology/version"
)

func checkOutOfOrder() bool {
	setup := func() (*replica.Replica, *bus.Bus) {
		f := newFixture(0)
		f.be.set("k", "v1")
		read(f.rep, "k")
		b := bus.New(0)
		f.rep.Subscribe(b)
		for v := 2; v <= 10; v++ {
			_ = b.Publish(bus.Notification{Key: "k", Version: version.Version(v)})
		}
		return f.rep, b
	}
	ref, refBus := setup()
	refBus.Flush()
	want := ref.Inspect("k")
	for trial := 0; trial < 100; trial++ {
		rep, b := setup()
		b.FlushShuffled(rand.New(rand.NewSource(int64(trial))))
		got := rep.Inspect("k")
		if got.State != want.State || got.Version != want.Version {
			return false
		}
	}
	return want.State == entry.Stale && want.Version == 10
}

func checkDuplicate() bool {
	f := newFixture(0)
	f.be.set("k", "v1")
	read(f.rep, "k")
	b := bus.New(0)
	f.rep.Subscribe(b)
	_ = b.Duplicate(bus.Notification{Key: "k", Version: 2})
	_ = b.Publish(bus.Notification{Key: "k", Version: 2})
	b.Flush()
	return f.rep.Inspect("k").Invalidations == 1
}

func checkExpiry() bool {
	f := newFixture(0)
	f.be.set("k", "v1")
	read(f.rep, "k")
	f.clock.Advance(ttl - time.Nanosecond)
	if read(f.rep, "k").State != replica.Hit || f.be.loadCalls() != 1 {
		return false
	}
	f.clock.Advance(time.Nanosecond) // now == expiresAt
	f.be.set("k", "v2")
	res := read(f.rep, "k")
	return res.State == replica.Hit && res.Value == "v2" && f.be.loadCalls() == 2
}

func checkSingleflight() bool {
	f := newFixture(0)
	f.be.set("k", "v1")
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	f.be.hook = func(string) { once.Do(func() { close(started) }); <-release }
	const n = 16
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); read(f.rep, "k") }()
	}
	<-started
	for i := 0; i < 100000 && f.be.loadCalls() != 1; i++ {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	return f.be.loadCalls() == 1 && f.rep.FetchCalls() == 1
}

func checkSlowKey() bool {
	f := newFixture(0)
	f.be.set("slow", "s")
	f.be.set("fast", "f")
	started := make(chan struct{})
	release := make(chan struct{})
	f.be.hook = func(key string) {
		if key == "slow" {
			close(started)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { defer close(done); read(f.rep, "slow") }()
	<-started
	res := read(f.rep, "fast") // must complete while "slow" is blocked
	close(release)
	<-done
	return res.State == replica.Hit && res.Value == "f"
}

func checkFailure() bool {
	f := newFixture(0)
	f.be.set("k", "v1")
	f.be.fail = errors.New("backend down")
	if _, err := f.rep.Read(context.Background(), "k"); err == nil {
		return false
	}
	if f.rep.Inspect("k").State != entry.Hole {
		return false
	}
	f.be.fail = nil
	res := read(f.rep, "k")
	return res.State == replica.Hit && res.Value == "v1" && f.be.loadCalls() == 2
}
