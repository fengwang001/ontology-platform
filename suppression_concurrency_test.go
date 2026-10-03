package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	manager := newTestManager(t)
	const goroutines = 16

	for g := 0; g < goroutines; g++ {
		address := fmt.Sprintf("worker-%d@example.com", g)
		if err := manager.Soft(address, int64(3*g+1)); err != nil {
			t.Fatal(err)
		}
		if err := manager.Soft(address, int64(3*g+2)); err != nil {
			t.Fatal(err)
		}
		if err := manager.Unsub(address, int64(3*g+3)); err != nil {
			t.Fatal(err)
		}
	}

	seqs := make([]int64, goroutines)
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines*2)
	start := make(chan struct{})

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			address := fmt.Sprintf("worker-%d@example.com", g)
			seq, err := manager.RequestConfirm(address, 200)
			if err != nil {
				errCh <- err
				return
			}
			seqs[g] = seq
		}(g)
	}
	close(start)
	wg.Wait()

	startConfirm := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-startConfirm
			address := fmt.Sprintf("worker-%d@example.com", g)
			if err := manager.Confirm(address, seqs[g], 201); err != nil {
				errCh <- err
			}
		}(g)
	}
	close(startConfirm)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if manager.nextSeq != goroutines+1 {
		t.Fatalf("nextSeq = %d, want %d", manager.nextSeq, goroutines+1)
	}
	for g := 0; g < goroutines; g++ {
		address := fmt.Sprintf("worker-%d@example.com", g)
		state := manager.addresses[address]
		if state.reason != None || state.hasToken || state.confirmedAt != 201 {
			t.Fatalf("state for %s = %+v, want confirmed recovery", address, state)
		}
	}
}
