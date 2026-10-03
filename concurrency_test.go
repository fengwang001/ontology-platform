package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentNoncesAndOneAcceptancePerNonce(t *testing.T) {
	const workers = 64
	machine, err := New(Config{
		AuthPendingTTL:   100,
		AuthValidTTL:     1000,
		OrderTTL:         500,
		FailureWindow:    60,
		FailureThreshold: 2,
		NonceCapacity:    3,
		PendingAuthLimit: workers + 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	nonces := make(chan uint64, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			nonces <- machine.Nonce()
		}()
	}
	group.Wait()
	close(nonces)

	seen := make(map[uint64]int)
	for nonce := range nonces {
		seen[nonce]++
	}
	if len(seen) != workers {
		t.Fatalf("generated %d unique nonces, want %d", len(seen), workers)
	}

	var accepted int64
	var acceptedMu sync.Mutex
	var barrier sync.WaitGroup
	for nonce := range seen {
		barrier.Add(1)
		go func(nonce uint64) {
			defer barrier.Done()
			_, err := machine.NewOrder([]byte("acc"), []string{"shared.com"}, nonce, 1)
			if err == nil {
				acceptedMu.Lock()
				accepted++
				acceptedMu.Unlock()
			}
		}(nonce)
	}
	barrier.Wait()

	if accepted != int64(machine.cfg.NonceCapacity) {
		t.Fatalf("accepted %d nonces, want pool capacity %d", accepted, machine.cfg.NonceCapacity)
	}
}

func TestReuseLookupAmortizationIgnoresUnrelatedAuths(t *testing.T) {
	machine := testMachine(t)
	for i := 0; i < 10_000; i++ {
		machine.auths = append(machine.auths, &authRecord{
			id:         i + 1,
			account:    fmt.Sprintf("other-%d", i),
			identifier: "unrelated.com",
			status:     StatusValid,
			expires:    1000,
		})
	}

	expiredID := len(machine.auths) + 1
	machine.auths = append(machine.auths, &authRecord{
		id:         expiredID,
		account:    "acc",
		identifier: "target.com",
		status:     StatusInvalid,
		expires:    30,
	})
	machine.insertValid(machine.auths[expiredID-1])

	freshID := len(machine.auths) + 1
	machine.auths = append(machine.auths, &authRecord{
		id:         freshID,
		account:    "acc",
		identifier: "target.com",
		status:     StatusValid,
		expires:    20,
	})
	machine.insertValid(machine.auths[freshID-1])

	before := machine.lookupExamined
	if id := machine.findReusableAuthorization("acc", "target.com", 10); id != freshID {
		t.Fatalf("selected z%d, want z%d", id, freshID)
	}
	if examined := machine.lookupExamined - before; examined != 2 {
		t.Fatalf("examined %d key entries, want 2", examined)
	}

	before = machine.lookupExamined
	if id := machine.findReusableAuthorization("acc", "target.com", 10); id != freshID {
		t.Fatalf("second lookup selected z%d", id)
	}
	if examined := machine.lookupExamined - before; examined != 1 {
		t.Fatalf("stale entry examined %d times", examined)
	}
}
