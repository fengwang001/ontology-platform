package ontology

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentOperationsAreSafeAndRemainConsistent(t *testing.T) {
	ledger := newTestLedger(t, 1000, 20, 20)
	var waitGroup sync.WaitGroup

	for worker := 0; worker < 16; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < 200; i++ {
				id, err := ledger.Create(2)
				if err != nil {
					continue
				}
				handle, openErr := ledger.Open(id)
				if openErr != nil {
					t.Errorf("Open(%d): %v", id, openErr)
					return
				}
				if worker%2 == 0 {
					if err := ledger.BeginShrink(id, 1); err != nil {
						t.Errorf("BeginShrink(%d): %v", id, err)
						return
					}
					if err := ledger.FinishShrink(id); err != nil && !errors.Is(err, ErrNoPendingShrink) {
						t.Errorf("FinishShrink(%d): %v", id, err)
						return
					}
				}
				if err := ledger.Close(handle); err != nil && !errors.Is(err, ErrNotFound) {
					t.Errorf("Close(%d): %v", handle, err)
					return
				}
				if err := ledger.Unlink(id); err != nil {
					t.Errorf("Unlink(%d): %v", id, err)
					return
				}
				_ = ledger.Used()
				_ = ledger.OrphanIDs()
			}
		}(worker)
	}

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		for i := 0; i < 20; i++ {
			_ = ledger.Crash()
		}
	}()

	waitGroup.Wait()

	if used := ledger.Used(); used < 0 || used > 1000 {
		t.Fatalf("Used() = %d, want within [0,1000]", used)
	}
	if orphans := ledger.OrphanIDs(); len(orphans) > 20 {
		t.Fatalf("orphans = %v, length exceeds 20", orphans)
	}
}
