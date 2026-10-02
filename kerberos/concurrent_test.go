package kerberos

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentSamePairExactlyOneSuccess(t *testing.T) {
	k := mustKDC(t, 1000, 10000, 30, 500)
	g, _ := k.IssueTGT([]byte("alice"), 0, 5000, 0, 0)
	st, err := k.TGS(g.ID, []byte("web"), 5000, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, replay, other int
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := k.Authenticate(st.ID, 110, 110)
			mu.Lock()
			defer mu.Unlock()
			switch reasonOf(err) {
			case "":
				ok++
			case ReasonReplay:
				replay++
			default:
				other++
			}
		}()
	}
	close(start)
	wg.Wait()
	if ok != 1 || replay != n-1 || other != 0 {
		t.Fatalf("ok=%d replay=%d other=%d", ok, replay, other)
	}
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	k := mustKDC(t, 1_000_000, 10_000_000, 10, 1_000_000)
	const workers = 12
	const per = 80
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
	}
	// Because the specification mandates one global monotonic clock, a
	// timestamp must be reserved immediately before its call; the reservation
	// and the call are therefore paired under clockMu. KDC mutex contention
	// itself is stressed by TestConcurrentSamePairExactlyOneSuccess.
	var clockMu sync.Mutex
	var counter int64
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			subject := []byte{byte('a' + w)}
			var ids []int64
			for i := 0; i < per; i++ {
				clockMu.Lock()
				now := atomic.AddInt64(&counter, 1)
				tk, err := k.IssueTGT(subject, 0, now+100000, 0, now)
				clockMu.Unlock()
				if err != nil {
					setErr(err)
					return
				}
				ids = append(ids, tk.ID)
			}
			for i, id := range ids {
				clockMu.Lock()
				now := atomic.AddInt64(&counter, 1)
				if err := k.Authenticate(id, now, now); err != nil {
					clockMu.Unlock()
					setErr(fmt.Errorf("worker %d auth %d: %w", w, i, err))
					return
				}
				clockMu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}
	if k.nextID != workers*per {
		t.Fatalf("issued %d tickets, want %d", k.nextID, workers*per)
	}
	if k.cache.h.Len() != len(k.cache.m) {
		t.Fatalf("heap/map desync: %d vs %d", k.cache.h.Len(), len(k.cache.m))
	}
	// cache can never exceed total successes and holds only live entries
	if got := len(k.cache.m); got > workers*per {
		t.Fatalf("cache size %d exceeds total successes %d", got, workers*per)
	}
}
