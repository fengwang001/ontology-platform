package main

import (
	"errors"
	"sync"
	"time"

	"ontology/gateway"
	"ontology/record"
)

var body = []byte(`{"op":"create","name":"alice"}`)

func checkExactlyOnce() bool {
	g, _ := newGateway()
	for i := 0; i < 3; i++ {
		if _, err := g.Submit("k", body, ok("v")); err != nil {
			return false
		}
	}
	return g.ExecCount() == 1
}

func checkReplay() bool {
	g, _ := newGateway()
	first, err := g.Submit("k", body, ok("v1"))
	if err != nil || first.Replayed {
		return false
	}
	dup, err := g.Submit("k", body, ok("v2"))
	return err == nil && dup.Replayed && dup.Value == first.Value
}

func checkConflict() bool {
	g, _ := newGateway()
	if _, err := g.Submit("k", body, ok("v1")); err != nil {
		return false
	}
	_, err := g.Submit("k", []byte(`{"op":"delete"}`), ok("v2"))
	if !errors.Is(err, gateway.ErrConflict) || g.ExecCount() != 1 {
		return false
	}
	res, err := g.Submit("k", body, ok("v3"))
	return err == nil && res.Value == "v1" // stored result untouched
}

func checkFailureRetry() bool {
	g, _ := newGateway()
	boom := errors.New("boom")
	_, err := g.Submit("k", body, func() (any, error) { return nil, boom })
	if !errors.Is(err, boom) {
		return false
	}
	res, err := g.Submit("k", body, ok("recovered"))
	if err != nil || res.Replayed || res.Value != "recovered" {
		return false
	}
	res, err = g.Submit("k", body, ok("other"))
	return err == nil && res.Replayed && res.Value == "recovered" && g.ExecCount() == 2
}

func checkConcurrentJoin() bool {
	g, _ := newGateway()
	const n = 16
	var wg sync.WaitGroup
	values := make(chan any, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := g.Submit("k", body, func() (any, error) {
				time.Sleep(50 * time.Millisecond)
				return "joined", nil
			})
			if err == nil {
				values <- res.Value
			}
		}()
	}
	wg.Wait()
	close(values)
	for v := range values {
		if v != "joined" {
			return false
		}
	}
	return g.ExecCount() == 1
}

func checkInFlightConflict() bool {
	g, _ := newGateway()
	release, started, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Submit("k", body, func() (any, error) {
			close(started)
			<-release
			return "v", nil
		})
	}()
	<-started
	_, err := g.Submit("k", []byte(`{"op":"delete"}`), ok("x"))
	if !errors.Is(err, gateway.ErrConflict) {
		return false
	}
	select { // must have returned while the first is still running
	case <-done:
		return false
	default:
	}
	close(release)
	<-done
	return g.ExecCount() == 1
}

func checkExpiryReexecutes() bool {
	g, c := newGateway()
	if _, err := g.Submit("k", body, ok("v1")); err != nil {
		return false
	}
	c.advance(ttl) // exactly at the expiry instant: already expired
	res, err := g.Submit("k", body, ok("v2"))
	return err == nil && !res.Replayed && res.Value == "v2" && g.ExecCount() == 2
}

func checkExpirySparesInFlight() bool {
	g, c := newGateway()
	release, started, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		_, _ = g.Submit("k", body, func() (any, error) {
			close(started)
			<-release
			return "v", nil
		})
	}()
	<-started
	c.advance(1000 * ttl) // far past the TTL while still running
	st := g.Query("k")
	close(release)
	<-done
	return st.Exists && st.State == record.StateInFlight && g.ExecCount() == 1
}

func checkQueryZero() bool {
	g, _ := newGateway()
	st := g.Query("missing")
	return st == (gateway.Status{})
}

func checkNoGlobalLock() bool {
	g, _ := newGateway()
	release, started, slowDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(slowDone)
		_, _ = g.Submit("slow", body, func() (any, error) {
			close(started)
			<-release
			return "slow", nil
		})
	}()
	<-started
	res, err := g.Submit("fast", body, ok("fast"))
	if err != nil || res.Value != "fast" {
		return false
	}
	select { // fast key finished while slow key still blocked
	case <-slowDone:
		return false
	default:
	}
	close(release)
	<-slowDone
	return true
}
